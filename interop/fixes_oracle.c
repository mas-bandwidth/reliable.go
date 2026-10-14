/*
    fixes_oracle.c

    Reference side of the differential check. It includes reliable.c from the
    checkout named by RELIABLE_C_DIR (a checkout of mas-bandwidth/reliable
    holding the config fixes), runs one of two modes, and prints a transcript
    the Go side (fixes_diff_test.go) byte-compares.

    Packet mode (no arguments): reads a stream of length-prefixed packets from
    stdin, feeds each in turn to one default endpoint (in-memory callbacks only,
    no socket, no clock), and prints one line per input:

        <input> <refused|accepted> <process callback ran 0|1> <the eleven counters>

    Config mode (`config <spec>...`): each argument is one config as a
    comma-separated list of `field=value` settings applied over
    reliable_default_config. For each it prints:

        <field> <value> <created|refused>

    and, for each created config, `ack <sent bytes acknowledged by a peer>`
    after one one-byte send through a default peer.

    The Go side builds this file against the same checkout:

        cc -O2 -ffp-contract=off -I$RELIABLE_C_DIR -o oracle fixes_oracle.c -lm

    and byte-compares the two transcripts. Keep the line format and the
    accepted/callback conventions in sync with fixes_diff_test.go.
*/

#include "reliable.c"

#include <stdio.h>
#include <stdint.h>
#include <inttypes.h>
#include <string.h>
#include <stdlib.h>

static int callback_ran;
static uint16_t delivered_sequence;
static int delivered_bytes;

static void transmit_packet( void * context, uint64_t id, uint16_t sequence, uint8_t * packet_data, int packet_bytes )
{
    (void) context;
    (void) id;
    (void) sequence;
    (void) packet_data;
    (void) packet_bytes;
}

static int process_packet( void * context, uint64_t id, uint16_t sequence, uint8_t * packet_data, int packet_bytes )
{
    (void) context;
    (void) id;
    (void) packet_data;
    callback_ran = 1;
    delivered_sequence = sequence;
    delivered_bytes = packet_bytes;
    return 1;
}

static struct reliable_endpoint_t * config_sender;
static struct reliable_endpoint_t * config_receiver;

static void config_transmit( void * context, uint64_t id, uint16_t sequence, uint8_t * packet_data, int packet_bytes )
{
    (void) context;
    (void) sequence;
    if ( id == 0 && config_receiver )
    {
        reliable_endpoint_receive_packet( config_receiver, packet_data, packet_bytes );
    }
    else if ( id == 1 && config_sender )
    {
        reliable_endpoint_receive_packet( config_sender, packet_data, packet_bytes );
    }
}

static int config_process( void * context, uint64_t id, uint16_t sequence, uint8_t * packet_data, int packet_bytes )
{
    (void) context;
    (void) id;
    (void) sequence;
    (void) packet_data;
    (void) packet_bytes;
    return 1;
}

static int read_uint32_le( uint32_t * out )
{
    unsigned char b[4];
    size_t got = 0;
    while ( got < 4 )
    {
        size_t n = fread( b + got, 1, 4 - got, stdin );
        if ( n == 0 )
            return 0;
        got += n;
    }
    *out = (uint32_t) b[0] | ( (uint32_t) b[1] << 8 ) | ( (uint32_t) b[2] << 16 ) | ( (uint32_t) b[3] << 24 );
    return 1;
}

// prints the spec with ',' and '=' turned into spaces, so a config reads as a
// list of field value pairs
static void config_description( const char * spec, char * out, size_t out_size )
{
    size_t j = 0;
    size_t i = 0;
    for ( i = 0; spec[i] != 0 && j + 1 < out_size; ++i )
    {
        char c = spec[i];
        out[j] = ( c == ',' || c == '=' ) ? ' ' : c;
        j++;
    }
    out[j] = 0;
}

static int config_apply_field( struct reliable_config_t * config, const char * name, long long value )
{
    if ( strcmp( name, "sent_packets_buffer_size" ) == 0 )
        config->sent_packets_buffer_size = (int) value;
    else if ( strcmp( name, "received_packets_buffer_size" ) == 0 )
        config->received_packets_buffer_size = (int) value;
    else if ( strcmp( name, "fragment_reassembly_buffer_size" ) == 0 )
        config->fragment_reassembly_buffer_size = (int) value;
    else if ( strcmp( name, "packet_header_size" ) == 0 )
        config->packet_header_size = (int) value;
    else if ( strcmp( name, "max_packet_size" ) == 0 )
        config->max_packet_size = (int) value;
    else
        return 0;
    return 1;
}

static int run_config_spec( const char * spec )
{
    struct reliable_config_t config;
    reliable_default_config( &config );
    reliable_copy_string( config.name, "under-test", sizeof( config.name ) );
    config.transmit_packet_function = &config_transmit;
    config.process_packet_function = &config_process;

    char buffer[512];
    if ( strlen( spec ) >= sizeof( buffer ) )
        return -1;
    strcpy( buffer, spec );

    char * saveptr = NULL;
    char * token = strtok_r( buffer, ",", &saveptr );
    while ( token )
    {
        char name[64];
        long long value = 0;
        if ( sscanf( token, "%63[^=]=%lld", name, &value ) != 2 )
            return -1;
        if ( !config_apply_field( &config, name, value ) )
            return -1;
        token = strtok_r( NULL, ",", &saveptr );
    }

    char description[512];
    config_description( spec, description, sizeof( description ) );

    struct reliable_endpoint_t * endpoint = reliable_endpoint_create( &config, 100.0 );
    if ( !endpoint )
    {
        printf( "%s refused\n", description );
        return 0;
    }

    struct reliable_config_t peer_config;
    reliable_default_config( &peer_config );
    reliable_copy_string( peer_config.name, "peer", sizeof( peer_config.name ) );
    peer_config.transmit_packet_function = &config_transmit;
    peer_config.process_packet_function = &config_process;
    peer_config.id = 1;

    config_sender = endpoint;
    config_receiver = reliable_endpoint_create( &peer_config, 100.0 );
    if ( !config_receiver )
    {
        config_sender = NULL;
        reliable_endpoint_destroy( endpoint );
        return -1;
    }

    uint8_t byte = 0;
    reliable_endpoint_send_packet( endpoint, &byte, 1 );
    reliable_endpoint_send_packet( config_receiver, &byte, 1 );

    RELIABLE_CONST uint64_t * counters = reliable_endpoint_counters( endpoint );
    int acknowledged = counters[RELIABLE_ENDPOINT_NUM_PACKETS_ACKED] > 0;

    printf( "%s created\n", description );
    printf( "ack %d\n", acknowledged ? config.packet_header_size + 1 : 0 );

    reliable_endpoint_destroy( config_receiver );
    reliable_endpoint_destroy( endpoint );
    config_receiver = NULL;
    config_sender = NULL;
    return 0;
}

static int run_packet_mode( void )
{
    struct reliable_config_t config;
    reliable_default_config( &config );
    reliable_copy_string( config.name, "receiver", sizeof( config.name ) );
    config.transmit_packet_function = &transmit_packet;
    config.process_packet_function = &process_packet;

    struct reliable_endpoint_t * endpoint = reliable_endpoint_create( &config, 100.0 );
    if ( !endpoint )
    {
        fprintf( stderr, "oracle: could not create endpoint\n" );
        return 1;
    }

    unsigned long n = 0;
    for ( ;; )
    {
        uint32_t packet_bytes_u32;
        if ( !read_uint32_le( &packet_bytes_u32 ) )
        {
            break;
        }

        n++;

        if ( packet_bytes_u32 > 1024 * 1024 )
        {
            fprintf( stderr, "oracle: input %lu too large\n", n );
            return 1;
        }

        static uint8_t packet_data[1024 * 1024];

        size_t remaining = (size_t) packet_bytes_u32;
        while ( remaining > 0 )
        {
            size_t got = fread( packet_data + ( packet_bytes_u32 - remaining ), 1, remaining, stdin );
            if ( got == 0 )
            {
                fprintf( stderr, "oracle: unexpected EOF on input %lu\n", n );
                return 1;
            }
            remaining -= got;
        }

        callback_ran = 0;
        delivered_sequence = 0;
        delivered_bytes = 0;

        reliable_endpoint_receive_packet( endpoint, packet_data, (int) packet_bytes_u32 );

        RELIABLE_CONST uint64_t * counters = reliable_endpoint_counters( endpoint );

        printf( "%lu %s %d", n, callback_ran ? "accepted" : "refused", callback_ran ? 1 : 0 );

        int i;
        for ( i = 0; i < RELIABLE_ENDPOINT_NUM_COUNTERS; ++i )
        {
            printf( " %" PRIu64, counters[i] );
        }
        printf( "\n" );
    }

    reliable_endpoint_destroy( endpoint );
    return 0;
}

int main( int argc, char ** argv )
{
    if ( argc >= 2 && strcmp( argv[1], "config" ) == 0 )
    {
        int i;
        for ( i = 2; i < argc; ++i )
        {
            if ( run_config_spec( argv[i] ) != 0 )
            {
                fprintf( stderr, "oracle: could not run config spec %s\n", argv[i] );
                return 1;
            }
        }
        return 0;
    }

    return run_packet_mode();
}


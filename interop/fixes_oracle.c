/*
    fixes_oracle.c

    Reference side of the differential check. It includes reliable.c from the
    checkout named by RELIABLE_C_DIR (a checkout of mas-bandwidth/reliable
    holding the fixes), runs one of three modes, and prints a transcript
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

    Reassembly mode (`reassembly <byte budget> <start step>`): reads a stream of
    length-prefixed packets from stdin, feeds each in turn to one receiver with
    fragment_reassembly_byte_budget set to the budget (0 = unbounded), and
    prints one line per input:

        <step> <fragment refused|accepted> <outstanding reassembly bytes> <the eleven counters>

    "fragment refused" means the receive incremented num_fragments_invalid; the
    outstanding bytes are the unfinished reassembly buffers, the same total the
    endpoint's own budget check derives from the live entries.

    Stats mode (`stats`): no stdin. Three endpoints, in-memory callbacks only.
    A fresh endpoint before any sample; an endpoint whose only samples have a
    round trip of 10000 ms, 12000 ms and 20000 ms; an endpoint that exchanges
    200 packets with scripted loss and round-trip times, then Reset. One line
    per step, every float as its raw 32-bit pattern:

        <step> rtt <f> rtt_min <f> rtt_max <f> rtt_avg <f> jitter_avg <f> jitter_max <f> jitter_stddev <f> loss <f> sent_kbps <f> received_kbps <f> acked_kbps <f>

    The Go side builds this file against the same checkout:

        cc -O2 -ffp-contract=off -I$RELIABLE_C_DIR -o oracle fixes_oracle.c -lm

    and byte-compares the two transcripts. Keep the line formats, the
    accepted/refused conventions and the counter order in sync with
    fixes_diff_test.go.
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
    int acknowledged = counters[RELIABLE_ENDPOINT_COUNTER_NUM_PACKETS_ACKED] > 0;

    printf( "%s created\n", description );
    printf( "ack %d\n", acknowledged ? config.packet_header_size + 1 : 0 );

    reliable_endpoint_destroy( config_receiver );
    reliable_endpoint_destroy( endpoint );
    config_receiver = NULL;
    config_sender = NULL;
    return 0;
}

// reads one length-prefixed packet into packet_data. returns the length, or -1 at end of stream.
static int read_packet( uint8_t * packet_data, uint32_t capacity )
{
    uint32_t packet_bytes_u32;
    if ( !read_uint32_le( &packet_bytes_u32 ) )
        return -1;

    if ( packet_bytes_u32 > capacity )
    {
        fprintf( stderr, "oracle: input %u exceeds the %u byte buffer\n", packet_bytes_u32, capacity );
        exit( 1 );
    }

    size_t remaining = (size_t) packet_bytes_u32;
    while ( remaining > 0 )
    {
        size_t got = fread( packet_data + ( packet_bytes_u32 - remaining ), 1, remaining, stdin );
        if ( got == 0 )
        {
            fprintf( stderr, "oracle: unexpected EOF on a %u byte input\n", packet_bytes_u32 );
            exit( 1 );
        }
        remaining -= got;
    }
    return (int) packet_bytes_u32;
}

// creates the receiver endpoint every stdin-driven mode shares, with the given
// fragment reassembly byte budget (the default when budget is the default).
static struct reliable_endpoint_t * create_receiver( int byte_budget )
{
    struct reliable_config_t config;
    reliable_default_config( &config );
    reliable_copy_string( config.name, "receiver", sizeof( config.name ) );
    config.transmit_packet_function = &transmit_packet;
    config.process_packet_function = &process_packet;
    config.fragment_reassembly_byte_budget = byte_budget;

    struct reliable_endpoint_t * endpoint = reliable_endpoint_create( &config, 100.0 );
    if ( !endpoint )
    {
        fprintf( stderr, "oracle: could not create endpoint\n" );
        exit( 1 );
    }
    return endpoint;
}

static int run_packet_mode( void )
{
    struct reliable_endpoint_t * endpoint = create_receiver( 256 * 1024 );

    // a large packet is at most max_packet_size + the packet and fragment headers.
    static uint8_t packet_data[ 64 * 1024 ];

    unsigned long n = 0;
    for ( ;; )
    {
        int packet_bytes = read_packet( packet_data, (uint32_t) sizeof( packet_data ) );
        if ( packet_bytes < 0 )
            break;

        n++;
        callback_ran = 0;
        delivered_sequence = 0;
        delivered_bytes = 0;

        reliable_endpoint_receive_packet( endpoint, packet_data, packet_bytes );

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

// the reassembly byte budget mode: one line per input with the outstanding bytes.

static int run_reassembly_mode( int byte_budget, unsigned long start_step )
{
    struct reliable_endpoint_t * endpoint = create_receiver( byte_budget );

    static uint8_t packet_data[ 64 * 1024 ];

    unsigned long step = start_step;
    for ( ;; )
    {
        int packet_bytes = read_packet( packet_data, (uint32_t) sizeof( packet_data ) );
        if ( packet_bytes < 0 )
            break;

        RELIABLE_CONST uint64_t * counters = reliable_endpoint_counters( endpoint );
        const uint64_t before_invalid = counters[RELIABLE_ENDPOINT_COUNTER_NUM_FRAGMENTS_INVALID];

        reliable_endpoint_receive_packet( endpoint, packet_data, packet_bytes );

        counters = reliable_endpoint_counters( endpoint );
        const int refused = counters[RELIABLE_ENDPOINT_COUNTER_NUM_FRAGMENTS_INVALID] != before_invalid;

        const size_t unfinished_bytes = reliable_endpoint_unfinished_reassembly_bytes( endpoint );

        printf( "%lu %s %zu", step, refused ? "refused" : "accepted", unfinished_bytes );

        int i;
        for ( i = 0; i < RELIABLE_ENDPOINT_NUM_COUNTERS; ++i )
        {
            printf( " %" PRIu64, counters[i] );
        }
        printf( "\n" );

        step++;
    }

    reliable_endpoint_destroy( endpoint );
    return 0;
}

static struct reliable_endpoint_t * stats_sender;
static struct reliable_endpoint_t * stats_receiver;
static int stats_drop;

static void stats_transmit( void * context, uint64_t id, uint16_t sequence, uint8_t * packet_data, int packet_bytes )
{
    (void) context;
    (void) sequence;
    if ( stats_drop )
        return;
    if ( id == 0 && stats_receiver )
        reliable_endpoint_receive_packet( stats_receiver, packet_data, packet_bytes );
    else if ( id == 1 && stats_sender )
        reliable_endpoint_receive_packet( stats_sender, packet_data, packet_bytes );
}

static int stats_process( void * context, uint64_t id, uint16_t sequence, uint8_t * packet_data, int packet_bytes )
{
    (void) context;
    (void) id;
    (void) sequence;
    (void) packet_data;
    (void) packet_bytes;
    return 1;
}

static uint32_t float_bits( float value )
{
    uint32_t bits = 0;
    memcpy( &bits, &value, sizeof( bits ) );
    return bits;
}

static void print_stats( const char * step, struct reliable_endpoint_t * endpoint )
{
    float sent = 0.0f;
    float received = 0.0f;
    float acked = 0.0f;
    reliable_endpoint_bandwidth( endpoint, &sent, &received, &acked );
    printf( "%s rtt %08x rtt_min %08x rtt_max %08x rtt_avg %08x jitter_avg %08x jitter_max %08x jitter_stddev %08x loss %08x sent_kbps %08x received_kbps %08x acked_kbps %08x\n",
        step,
        float_bits( reliable_endpoint_rtt( endpoint ) ),
        float_bits( reliable_endpoint_rtt_min( endpoint ) ),
        float_bits( reliable_endpoint_rtt_max( endpoint ) ),
        float_bits( reliable_endpoint_rtt_avg( endpoint ) ),
        float_bits( reliable_endpoint_jitter_avg_vs_min_rtt( endpoint ) ),
        float_bits( reliable_endpoint_jitter_max_vs_min_rtt( endpoint ) ),
        float_bits( reliable_endpoint_jitter_stddev_vs_avg_rtt( endpoint ) ),
        float_bits( reliable_endpoint_packet_loss( endpoint ) ),
        float_bits( sent ),
        float_bits( received ),
        float_bits( acked ) );
}

static void stats_create_pair( double time )
{
    struct reliable_config_t sender_config;
    struct reliable_config_t receiver_config;
    reliable_default_config( &sender_config );
    reliable_default_config( &receiver_config );
    reliable_copy_string( sender_config.name, "sender", sizeof( sender_config.name ) );
    reliable_copy_string( receiver_config.name, "receiver", sizeof( receiver_config.name ) );
    sender_config.id = 0;
    receiver_config.id = 1;
    // 200 sends sit outside the loss and bandwidth window (half of 1024), so
    // those statistics stay 0 and the float32 smoothing path is not in play.
    // The round trip and the loss pattern are still scripted below.
    sender_config.sent_packets_buffer_size = 1024;
    sender_config.received_packets_buffer_size = 1024;
    receiver_config.sent_packets_buffer_size = 1024;
    receiver_config.received_packets_buffer_size = 1024;
    sender_config.transmit_packet_function = &stats_transmit;
    receiver_config.transmit_packet_function = &stats_transmit;
    sender_config.process_packet_function = &stats_process;
    receiver_config.process_packet_function = &stats_process;

    stats_drop = 0;
    stats_sender = reliable_endpoint_create( &sender_config, time );
    stats_receiver = reliable_endpoint_create( &receiver_config, time );
    if ( !stats_sender || !stats_receiver )
    {
        fprintf( stderr, "oracle: could not create stats endpoints\n" );
        exit( 1 );
    }
}

static void stats_destroy_pair( void )
{
    reliable_endpoint_destroy( stats_sender );
    reliable_endpoint_destroy( stats_receiver );
    stats_sender = NULL;
    stats_receiver = NULL;
}

// the same schedule as fixes_diff_test.go: fresh, three large samples, then
// 200 packets with loss on every 7th send and a round trip of 125 ms.
static int run_stats_mode( void )
{
    double time = 100.0;
    stats_create_pair( time );
    print_stats( "fresh", stats_sender );
    stats_destroy_pair();

    time = 100.0;
    stats_create_pair( time );
    uint8_t small[8];
    memset( small, 0, sizeof( small ) );
    const double large_delays[3] = { 10.0, 12.0, 20.0 };
    int i;
    for ( i = 0; i < 3; ++i )
    {
        reliable_endpoint_update( stats_sender, time );
        reliable_endpoint_update( stats_receiver, time );
        reliable_endpoint_send_packet( stats_sender, small, (int) sizeof( small ) );
        time += large_delays[i];
        reliable_endpoint_update( stats_sender, time );
        reliable_endpoint_update( stats_receiver, time );
        reliable_endpoint_send_packet( stats_receiver, small, (int) sizeof( small ) );
        reliable_endpoint_update( stats_sender, time );
        reliable_endpoint_clear_acks( stats_sender );
        reliable_endpoint_clear_acks( stats_receiver );
    }
    print_stats( "large", stats_sender );
    stats_destroy_pair();

    time = 100.0;
    stats_create_pair( time );
    uint8_t packet[32];
    memset( packet, 0, sizeof( packet ) );
    for ( i = 0; i < 200; ++i )
    {
        reliable_endpoint_update( stats_sender, time );
        reliable_endpoint_update( stats_receiver, time );
        stats_drop = ( i % 7 ) == 0;
        reliable_endpoint_send_packet( stats_sender, packet, (int) sizeof( packet ) );
        stats_drop = 0;
        time += 0.125;
        reliable_endpoint_update( stats_sender, time );
        reliable_endpoint_update( stats_receiver, time );
        reliable_endpoint_send_packet( stats_receiver, packet, (int) sizeof( packet ) );
        time += 0.125;
        reliable_endpoint_update( stats_sender, time );
        reliable_endpoint_update( stats_receiver, time );
        reliable_endpoint_clear_acks( stats_sender );
        reliable_endpoint_clear_acks( stats_receiver );
    }
    print_stats( "exchange", stats_sender );
    reliable_endpoint_reset( stats_sender );
    print_stats( "reset", stats_sender );
    stats_destroy_pair();
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

    if ( argc >= 2 && strcmp( argv[1], "reassembly" ) == 0 )
    {
        const int byte_budget = ( argc >= 3 ) ? (int) strtol( argv[2], NULL, 10 ) : 0;
        const unsigned long start_step = ( argc >= 4 ) ? (unsigned long) strtoul( argv[3], NULL, 10 ) : 1;
        return run_reassembly_mode( byte_budget, start_step );
    }

    if ( argc >= 2 && strcmp( argv[1], "stats" ) == 0 )
        return run_stats_mode();

    return run_packet_mode();
}


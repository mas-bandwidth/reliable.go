/*
    fixes_oracle.c

    Reference side of the differential check for the fragment window advance fix.
    It is driven by arguments and stdin bytes:

        oracle <label> <fragment sequence> <num preamble> <num fragment>

    and reads length-prefixed packets from stdin in order: the preamble packets,
    then the fragment packet(s), then the genuine packet(s) as the remaining
    stream. It feeds them to a fresh reliable endpoint (default config,
    in-memory callbacks only, no socket, no clock) and prints one line:

        <label> <fragment seq> <refused|accepted> <genuine delivered 0|1> <c0> .. <c10>

    The Go side (fixes_diff_test.go) pipes the identical bytes and compares the
    two transcripts byte for byte. Build against the checkout named by
    RELIABLE_C_DIR:

        cc -O2 -ffp-contract=off -I$RELIABLE_C_DIR -o oracle fixes_oracle.c -lm

    Keep the line format, the disposition rule and the counter order in sync with
    fixes_diff_test.go. Include reliable.c so a single translation unit is built.
*/

#include "reliable.c"

#include <stdio.h>
#include <stdint.h>
#include <inttypes.h>
#include <stdlib.h>
#include <string.h>

/* reliable.h counter order: num_fragments_invalid is index 9. */
#define COUNTER_NUM_FRAGMENTS_INVALID 9

static int delivered;

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
    (void) sequence;
    (void) packet_data;
    (void) packet_bytes;
    delivered = 1;
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

/* reads one length-prefixed packet. returns its length, 0 on clean EOF, -1 on a
   short read. */
static int read_packet( uint8_t * packet_data, uint32_t capacity )
{
    uint32_t packet_bytes;
    if ( !read_uint32_le( &packet_bytes ) )
        return 0;
    if ( packet_bytes > capacity )
    {
        fprintf( stderr, "oracle: packet %u too large\n", packet_bytes );
        return -1;
    }
    size_t got = 0;
    while ( got < packet_bytes )
    {
        size_t n = fread( packet_data + got, 1, packet_bytes - got, stdin );
        if ( n == 0 )
        {
            fprintf( stderr, "oracle: unexpected EOF\n" );
            return -1;
        }
        got += n;
    }
    return (int) packet_bytes;
}

int main( int argc, char ** argv )
{
    if ( argc < 5 )
    {
        fprintf( stderr, "usage: oracle <label> <fragment sequence> <num preamble> <num fragment>\n" );
        return 1;
    }

    const char * label = argv[1];
    unsigned fragment_sequence = (unsigned) strtoul( argv[2], NULL, 10 );
    int num_preamble = atoi( argv[3] );
    int num_fragment = atoi( argv[4] );

    struct reliable_config_t config;
    reliable_default_config( &config );
    reliable_copy_string( config.name, "oracle", sizeof( config.name ) );
    config.transmit_packet_function = &transmit_packet;
    config.process_packet_function = &process_packet;

    struct reliable_endpoint_t * endpoint = reliable_endpoint_create( &config, 100.0 );
    if ( !endpoint )
    {
        fprintf( stderr, "oracle: could not create endpoint\n" );
        return 1;
    }

    static uint8_t packet_data[1024 * 1024];

    int i;
    for ( i = 0; i < num_preamble; ++i )
    {
        int n = read_packet( packet_data, sizeof( packet_data ) );
        if ( n <= 0 )
        {
            fprintf( stderr, "oracle: missing preamble packet\n" );
            reliable_endpoint_destroy( endpoint );
            return 1;
        }
        delivered = 0;
        reliable_endpoint_receive_packet( endpoint, packet_data, n );
    }

    RELIABLE_CONST uint64_t * before = reliable_endpoint_counters( endpoint );
    uint64_t fragments_invalid_before = before[COUNTER_NUM_FRAGMENTS_INVALID];

    for ( i = 0; i < num_fragment; ++i )
    {
        int n = read_packet( packet_data, sizeof( packet_data ) );
        if ( n <= 0 )
        {
            fprintf( stderr, "oracle: missing fragment packet\n" );
            reliable_endpoint_destroy( endpoint );
            return 1;
        }
        reliable_endpoint_receive_packet( endpoint, packet_data, n );
    }

    RELIABLE_CONST uint64_t * after = reliable_endpoint_counters( endpoint );
    const char * disposition = ( after[COUNTER_NUM_FRAGMENTS_INVALID] != fragments_invalid_before ) ? "refused" : "accepted";

    delivered = 0;
    for ( ;; )
    {
        int n = read_packet( packet_data, sizeof( packet_data ) );
        if ( n == 0 )
            break;
        if ( n < 0 )
        {
            reliable_endpoint_destroy( endpoint );
            return 1;
        }
        reliable_endpoint_receive_packet( endpoint, packet_data, n );
    }

    RELIABLE_CONST uint64_t * counters = reliable_endpoint_counters( endpoint );

    printf( "%s %u %s %d", label, fragment_sequence, disposition, delivered ? 1 : 0 );
    for ( i = 0; i < RELIABLE_ENDPOINT_NUM_COUNTERS; ++i )
        printf( " %" PRIu64, counters[i] );
    printf( "\n" );

    reliable_endpoint_destroy( endpoint );
    return 0;
}

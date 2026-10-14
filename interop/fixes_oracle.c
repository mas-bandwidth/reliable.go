/*
    fixes_oracle.c

    Reference side of the differential check. It includes reliable.c from the
    checkout named by RELIABLE_C_DIR (branch sprint/reliable-audit-fixes at or
    after c8995dc, where a zero length packet is refused and counted invalid
    before any byte is read and unfinished reassemblies are held within
    fragment_reassembly_byte_budget), reads a stream of length-prefixed packets
    from stdin, feeds each in turn to one endpoint (in-memory callbacks only, no
    socket, no clock), and prints one line per input.

    Two modes, selected by the first argument:

        oracle
            feeds a default endpoint and prints, per input:

                <input> <refused|accepted> <process callback ran 0|1> <the eleven counters>

        oracle reassembly <byte budget> <start step>
            creates one receiver with fragment_reassembly_byte_budget set to the
            budget (0 = unbounded) and prints, per input:

                <step> <fragment refused|accepted> <outstanding reassembly bytes> <the eleven counters>

    "fragment refused" means the receive incremented num_fragments_invalid; the
    outstanding bytes are the live reassembly buffers, the same total the
    endpoint's own budget check uses.

    The Go side (fixes_diff_test.go) builds this file against the same checkout:

        cc -O2 -ffp-contract=off -I$RELIABLE_C_DIR -o oracle fixes_oracle.c -lm

    and byte-compares the two transcripts. Keep the line formats, the
    accepted/refused conventions and the counter order in sync with
    fixes_diff_test.go.
*/

#include "reliable.c"

#include <stdio.h>
#include <stdint.h>
#include <inttypes.h>
#include <stdlib.h>
#include <string.h>

/* reliable.h counter order: num_fragments_invalid is index 9. */
#define COUNTER_NUM_FRAGMENTS_INVALID 9

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

/* reads one length-prefixed packet into packet_data. returns the length, or -1 at end of stream. */
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

static struct reliable_endpoint_t * create_endpoint( int byte_budget )
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

/* the default endpoint mode: one line per input with the process disposition. */

static int run_default( void )
{
    struct reliable_endpoint_t * endpoint = create_endpoint( 256 * 1024 );

    /* a large packet is at most max_packet_size + the packet and fragment headers. */
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

/* the reassembly byte budget mode: one line per input with the outstanding bytes. */

static int run_reassembly( int byte_budget, unsigned long start_step )
{
    struct reliable_endpoint_t * endpoint = create_endpoint( byte_budget );

    static uint8_t packet_data[ 64 * 1024 ];

    unsigned long step = start_step;
    for ( ;; )
    {
        int packet_bytes = read_packet( packet_data, (uint32_t) sizeof( packet_data ) );
        if ( packet_bytes < 0 )
            break;

        RELIABLE_CONST uint64_t * counters = reliable_endpoint_counters( endpoint );
        const uint64_t before_invalid = counters[COUNTER_NUM_FRAGMENTS_INVALID];

        reliable_endpoint_receive_packet( endpoint, packet_data, packet_bytes );

        counters = reliable_endpoint_counters( endpoint );
        const int refused = counters[COUNTER_NUM_FRAGMENTS_INVALID] != before_invalid;

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

int main( int argc, char ** argv )
{
    if ( argc >= 2 && strcmp( argv[1], "reassembly" ) == 0 )
    {
        const int byte_budget = ( argc >= 3 ) ? (int) strtol( argv[2], NULL, 10 ) : 0;
        const unsigned long start_step = ( argc >= 4 ) ? (unsigned long) strtoul( argv[3], NULL, 10 ) : 1;
        return run_reassembly( byte_budget, start_step );
    }
    return run_default();
}

/*
    fixes_oracle.c

    Reference side of the differential check. It includes reliable.c from the
    checkout named by RELIABLE_C_DIR (branch sprint/reliable-audit-fixes at or
    after e515523c, where a zero length packet is refused and counted invalid
    before any byte is read), reads a stream of length-prefixed packets from
    stdin, feeds each in turn to one default endpoint (in-memory callbacks only,
    no socket, no clock), and prints one line per input:

        <input> <refused|accepted> <process callback ran 0|1> <the eleven counters>

    The Go side (fixes_diff_test.go) builds this file against the same checkout:

        cc -O2 -ffp-contract=off -I$RELIABLE_C_DIR -o oracle fixes_oracle.c -lm

    and byte-compares the two transcripts. Keep the line format and the
    accepted/callback conventions in sync with fixes_diff_test.go.
*/

#include "reliable.c"

#include <stdio.h>
#include <stdint.h>
#include <inttypes.h>
#include <string.h>

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

int main( void )
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

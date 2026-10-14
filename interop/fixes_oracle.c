/*
    fixes_oracle.c

    Reference side of the differential check: it reads a stream of length-prefixed
    packets from stdin, feeds each to a fresh reliable endpoint (default config,
    in-memory callbacks only, no socket, no clock), and prints one line per input
    in the same form as the Go side (fixes_diff_test.go):

        <n> <accepted|refused> <counter name or none> <seq:len or -> <c0> .. <c10>

    The Go test builds this file against the C checkout named by RELIABLE_C_DIR:

        cc -O2 -ffp-contract=off -I$RELIABLE_C_DIR -o oracle fixes_oracle.c $RELIABLE_C_DIR/reliable.c -lm

    and compares the transcripts byte for byte. Keep the line format and the
    "accepted"/"counter"/"delivered" conventions in sync with fixes_diff_test.go.
*/

#include "reliable.h"

#include <stdio.h>
#include <stdint.h>
#include <string.h>

static const char * counter_names[RELIABLE_ENDPOINT_NUM_COUNTERS] = {
    "num_packets_sent",
    "num_packets_received",
    "num_packets_acked",
    "num_packets_stale",
    "num_packets_invalid",
    "num_packets_too_large_to_send",
    "num_packets_too_large_to_receive",
    "num_fragments_sent",
    "num_fragments_received",
    "num_fragments_invalid",
    "num_packets_duplicate",
};

static int delivered;
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
    if ( !delivered )
    {
        delivered = 1;
        delivered_sequence = sequence;
        delivered_bytes = packet_bytes;
    }
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
    reliable_copy_string( config.name, "oracle", sizeof( config.name ) );
    config.transmit_packet_function = &transmit_packet;
    config.process_packet_function = &process_packet;

    struct reliable_endpoint_t * endpoint = reliable_endpoint_create( &config, 100.0 );
    if ( !endpoint )
    {
        fprintf( stderr, "oracle: could not create endpoint\n" );
        return 1;
    }

    // send preamble: populate sent_packets so a later received packet carrying
    // acks exercises the ack walk and its counters. identical on the Go side.
    reliable_endpoint_send_packet( endpoint, (uint8_t*) "abc", 3 );
    reliable_endpoint_send_packet( endpoint, (uint8_t*) "def", 3 );

    uint64_t previous[RELIABLE_ENDPOINT_NUM_COUNTERS];
    memcpy( previous, reliable_endpoint_counters( endpoint ), sizeof( previous ) );

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

        delivered = 0;

        reliable_endpoint_receive_packet( endpoint, packet_data, (int) packet_bytes_u32 );

        RELIABLE_CONST uint64_t * counters = reliable_endpoint_counters( endpoint );

        const char * counter = "none";
        int i;
        for ( i = 0; i < RELIABLE_ENDPOINT_NUM_COUNTERS; ++i )
        {
            if ( counters[i] != previous[i] )
            {
                counter = counter_names[i];
                break;
            }
        }

        printf( "%lu %s %s ", n, delivered ? "accepted" : "refused", counter );

        if ( delivered )
        {
            printf( "%u:%d", (unsigned) delivered_sequence, delivered_bytes );
        }
        else
        {
            printf( "-" );
        }

        for ( i = 0; i < RELIABLE_ENDPOINT_NUM_COUNTERS; ++i )
        {
            printf( " %" PRIu64, counters[i] );
        }
        printf( "\n" );

        memcpy( previous, counters, sizeof( previous ) );
    }

    reliable_endpoint_destroy( endpoint );
    return 0;
}

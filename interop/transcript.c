/*
    transcript.c

    Deterministic wire transcript generator for the C reliable library.

    This is the reference side of the wire compatibility check: it prints every
    transmitted packet as hex, the acks reported each frame, and the endpoint
    counters at the end. The Go port implements exactly the same scenario in
    generateTranscript (wire_compat_test.go) and must reproduce this output
    byte for byte.

    Both endpoints are primed so their sequence numbers are close to the
    65535 -> 0 wrap before the recorded frames begin. The sender transmits every
    frame and the receiver every third frame, so ack deltas cross the 8 bit /
    16 bit encoding boundary. Most packets are regular (below fragment_above)
    with multi-fragment packets interspersed, and the loss pattern leaves runs
    of received packets long enough for ack-bits bytes to be elided.

    Keep the two implementations of the scenario in sync. Regenerate the golden
    transcript with interop/regenerate.sh whenever the scenario changes.
*/

#include "reliable.h"
#include <stdio.h>
#include <inttypes.h>

#define MAX_PACKET_BYTES 4096

#define PRIME_SENDS 65520
#define NUM_ITERATIONS 600

#define SENDER_SEND_INTERVAL 1
#define RECEIVER_SEND_INTERVAL 3

#define LOSS_INTERVAL 17
#define DUPLICATE_INTERVAL 11

static struct reliable_endpoint_t * sender;
static struct reliable_endpoint_t * receiver;

static int recording = 0;
static int transmit_count = 0;

static void transmit_packet( void * context, uint64_t id, uint16_t sequence, uint8_t * packet_data, int packet_bytes )
{
    (void) context;

    if ( !recording )
    {
        return;
    }

    transmit_count++;

    printf( "T %" PRIu64 " %d ", id, (int) sequence );
    for ( int i = 0; i < packet_bytes; i++ )
    {
        printf( "%02x", packet_data[i] );
    }
    printf( "\n" );

    // deterministic packet loss

    if ( ( transmit_count % LOSS_INTERVAL ) == 0 )
    {
        return;
    }

    struct reliable_endpoint_t * destination = ( id == 0 ) ? receiver : sender;

    reliable_endpoint_receive_packet( destination, packet_data, packet_bytes );

    // deterministic duplication

    if ( ( transmit_count % DUPLICATE_INTERVAL ) == 0 )
    {
        reliable_endpoint_receive_packet( destination, packet_data, packet_bytes );
    }
}

static int process_packet( void * context, uint64_t id, uint16_t sequence, uint8_t * packet_data, int packet_bytes )
{
    (void) context;
    (void) id;
    (void) sequence;
    (void) packet_data;
    (void) packet_bytes;
    return 1;
}

static int sender_packet_bytes( int frame )
{
    if ( ( frame % 13 ) == 0 )
    {
        return 1025 + ( frame * 197 ) % 3000;
    }
    return 2 + ( frame * 37 ) % 499;
}

static int receiver_packet_bytes( int send_index )
{
    if ( ( send_index % 7 ) == 0 )
    {
        return 1025 + ( send_index * 331 ) % 3000;
    }
    return 2 + ( send_index * 53 ) % 499;
}

static int generate_packet_data( uint16_t sequence, int packet_bytes, uint8_t * packet_data )
{
    packet_data[0] = (uint8_t) ( sequence & 0xFF );
    packet_data[1] = (uint8_t) ( (sequence>>8) & 0xFF );
    for ( int i = 2; i < packet_bytes; i++ )
    {
        packet_data[i] = (uint8_t) ( ( i + sequence ) % 256 );
    }
    return packet_bytes;
}

static void print_acks( struct reliable_endpoint_t * endpoint, int id )
{
    int num_acks;
    RELIABLE_CONST uint16_t * acks = reliable_endpoint_get_acks( endpoint, &num_acks );
    printf( "A %d", id );
    for ( int i = 0; i < num_acks; i++ )
    {
        printf( " %d", (int) acks[i] );
    }
    printf( "\n" );
}

int main()
{
    double time = 100.0;

    struct reliable_config_t sender_config;
    struct reliable_config_t receiver_config;

    reliable_default_config( &sender_config );
    reliable_default_config( &receiver_config );

    sender_config.fragment_above = 500;
    receiver_config.fragment_above = 500;

    reliable_copy_string( sender_config.name, "sender", sizeof( sender_config.name ) );
    sender_config.id = 0;
    sender_config.transmit_packet_function = &transmit_packet;
    sender_config.process_packet_function = &process_packet;

    reliable_copy_string( receiver_config.name, "receiver", sizeof( receiver_config.name ) );
    receiver_config.id = 1;
    receiver_config.transmit_packet_function = &transmit_packet;
    receiver_config.process_packet_function = &process_packet;

    sender = reliable_endpoint_create( &sender_config, time );
    receiver = reliable_endpoint_create( &receiver_config, time );

    uint8_t packet_data[MAX_PACKET_BYTES];

    // prime both sequence numbers close to the wrap

    for ( int i = 0; i < PRIME_SENDS; i++ )
    {
        int packet_bytes = generate_packet_data( reliable_endpoint_next_packet_sequence( sender ), 4, packet_data );
        reliable_endpoint_send_packet( sender, packet_data, packet_bytes );
    }
    for ( int i = 0; i < PRIME_SENDS; i++ )
    {
        int packet_bytes = generate_packet_data( reliable_endpoint_next_packet_sequence( receiver ), 4, packet_data );
        reliable_endpoint_send_packet( receiver, packet_data, packet_bytes );
    }

    recording = 1;

    int receiver_send_index = 0;

    for ( int frame = 0; frame < NUM_ITERATIONS; frame++ )
    {
        if ( ( frame % SENDER_SEND_INTERVAL ) == 0 )
        {
            int packet_bytes = generate_packet_data( reliable_endpoint_next_packet_sequence( sender ), sender_packet_bytes( frame ), packet_data );
            reliable_endpoint_send_packet( sender, packet_data, packet_bytes );
        }

        if ( ( frame % RECEIVER_SEND_INTERVAL ) == 0 )
        {
            int packet_bytes = generate_packet_data( reliable_endpoint_next_packet_sequence( receiver ), receiver_packet_bytes( receiver_send_index ), packet_data );
            reliable_endpoint_send_packet( receiver, packet_data, packet_bytes );
            receiver_send_index++;
        }

        reliable_endpoint_update( sender, time );
        reliable_endpoint_update( receiver, time );

        print_acks( sender, 0 );
        print_acks( receiver, 1 );

        reliable_endpoint_clear_acks( sender );
        reliable_endpoint_clear_acks( receiver );

        time += 0.01;
    }

    RELIABLE_CONST uint64_t * sender_counters = reliable_endpoint_counters( sender );
    RELIABLE_CONST uint64_t * receiver_counters = reliable_endpoint_counters( receiver );

    for ( int i = 0; i < RELIABLE_ENDPOINT_NUM_COUNTERS; i++ )
    {
        printf( "C 0 %d %" PRIu64 "\n", i, sender_counters[i] );
    }
    for ( int i = 0; i < RELIABLE_ENDPOINT_NUM_COUNTERS; i++ )
    {
        printf( "C 1 %d %" PRIu64 "\n", i, receiver_counters[i] );
    }

    reliable_endpoint_destroy( sender );
    reliable_endpoint_destroy( receiver );

    return 0;
}

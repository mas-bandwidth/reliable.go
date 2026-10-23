; reliable.go roadmap: the data ROADMAP.md is generated from (nova-roadmap, a nova-tools release binary;
; make roadmap renders, make roadmap-check fails on drift). Read as data, never evaluated.
; A cell is done only when its task is :done with :evidence, a test or a commit;
; :tested-revision is the main commit whose CI passed with that evidence.
(:schema 1
 :root "goal"
 :page (:title "reliable.go roadmap"
        :roadmaps ("acks" "packets" "stats" "compat" "quality"))
 :nodes
 ((:id "goal" :type :work-set :children ("acks" "packets" "stats" "compat" "quality"))
  (:id "acks" :type :roadmap :title "Acks"
   :text "Knowing which of the packets you sent were received by the other side."
   :rows (("acks" "acks for packets received")
          ("acks-loss" "acks stay correct under packet loss")
          ("ack-bits" "32 acks redundantly in every packet header")
          ("ack-overflow" "a full ack buffer drops extra acks and logs")
          ("next-sequence" "ask the sequence of the next packet, to map acks to contents")
          ("refuse-ack" "the receiver can decline to ack a packet"))
   :columns (("done" "done"))
   :cells (("acks" "done" "acks/acks")
           ("acks-loss" "done" "acks/acks-loss")
           ("ack-bits" "done" "acks/ack-bits")
           ("ack-overflow" "done" "acks/ack-overflow")
           ("next-sequence" "done" "acks/next-sequence")
           ("refuse-ack" "done" "acks/refuse-ack")))
  (:id "acks/acks" :type :task :title "acks for packets received" :state :done :evidence ("reliable_test.go: TestAcks" "example_test.go: Example") :tested-revision "23e05fb5e411777a034bf695e081dc5eb001a60e")
  (:id "acks/acks-loss" :type :task :title "acks stay correct under packet loss" :state :done :evidence ("reliable_test.go: TestAcksPacketLoss") :tested-revision "23e05fb5e411777a034bf695e081dc5eb001a60e")
  (:id "acks/ack-bits" :type :task :title "32 acks redundantly in every packet header" :state :done :evidence ("reliable_test.go: TestGenerateAckBits" "reliable_test.go: TestPacketHeader") :tested-revision "23e05fb5e411777a034bf695e081dc5eb001a60e")
  (:id "acks/ack-overflow" :type :task :title "a full ack buffer drops extra acks and logs" :state :done :evidence ("reliable_test.go: TestAckBufferOverflow") :tested-revision "23e05fb5e411777a034bf695e081dc5eb001a60e")
  (:id "acks/next-sequence" :type :task :title "ask the sequence of the next packet, to map acks to contents" :state :done :evidence ("reliable_test.go: TestPackets" "reliable_test.go: TestDuplicatePackets") :tested-revision "23e05fb5e411777a034bf695e081dc5eb001a60e")
  (:id "acks/refuse-ack" :type :task :title "the receiver can decline to ack a packet" :state :unknown
   :note "ProcessPacketFunction returning false skips the ack (README), but no test returns false; a test asserting the packet is not acked makes this done.")
  (:id "packets" :type :roadmap :title "Packets"
   :text "Sending and receiving packets over your own UDP socket, including packets larger than the MTU."
   :rows (("packets" "send and receive packets")
          ("fragmentation" "fragmentation and reassembly of large packets")
          ("fragment-cleanup" "incomplete fragmented packets are cleaned up")
          ("fragment-config" "fragment configurations that cannot fit a packet are refused")
          ("duplicates" "duplicate packets detected and dropped")
          ("stale" "stale packets dropped")
          ("rollover" "sequence numbers wrap around safely")
          ("reset" "reset an endpoint to its initial state")
          ("malformed" "malformed packets refused, fuzzed"))
   :columns (("done" "done"))
   :cells (("packets" "done" "packets/packets")
           ("fragmentation" "done" "packets/fragmentation")
           ("fragment-cleanup" "done" "packets/fragment-cleanup")
           ("fragment-config" "done" "packets/fragment-config")
           ("duplicates" "done" "packets/duplicates")
           ("stale" "done" "packets/stale")
           ("rollover" "done" "packets/rollover")
           ("reset" "done" "packets/reset")
           ("malformed" "done" "packets/malformed")))
  (:id "packets/packets" :type :task :title "send and receive packets" :state :done :evidence ("reliable_test.go: TestPackets") :tested-revision "23e05fb5e411777a034bf695e081dc5eb001a60e")
  (:id "packets/fragmentation" :type :task :title "fragmentation and reassembly of large packets" :state :done :evidence ("reliable_test.go: TestLargePackets") :tested-revision "23e05fb5e411777a034bf695e081dc5eb001a60e")
  (:id "packets/fragment-cleanup" :type :task :title "incomplete fragmented packets are cleaned up" :state :done :evidence ("reliable_test.go: TestFragmentCleanup") :tested-revision "23e05fb5e411777a034bf695e081dc5eb001a60e")
  (:id "packets/fragment-config" :type :task :title "fragment configurations that cannot fit a packet are refused" :state :done :evidence ("reliable_test.go: TestNewEndpointFragmentConfigValidation" "commit 5ae3e1b Reject unsafe fragment configurations (#13)") :tested-revision "23e05fb5e411777a034bf695e081dc5eb001a60e"
   :github-issue "12"
   :github-pr "13")
  (:id "packets/duplicates" :type :task :title "duplicate packets detected and dropped" :state :done :evidence ("reliable_test.go: TestDuplicatePackets") :tested-revision "23e05fb5e411777a034bf695e081dc5eb001a60e")
  (:id "packets/stale" :type :task :title "stale packets dropped" :state :done :evidence ("reliable_test.go: TestStalePackets") :tested-revision "23e05fb5e411777a034bf695e081dc5eb001a60e")
  (:id "packets/rollover" :type :task :title "sequence numbers wrap around safely" :state :done :evidence ("reliable_test.go: TestSequenceBufferRollover" "reliable_test.go: TestSequenceBuffer") :tested-revision "23e05fb5e411777a034bf695e081dc5eb001a60e")
  (:id "packets/reset" :type :task :title "reset an endpoint to its initial state" :state :done :evidence ("reliable_test.go: TestEndpointReset") :tested-revision "23e05fb5e411777a034bf695e081dc5eb001a60e")
  (:id "packets/malformed" :type :task :title "malformed packets refused, fuzzed" :state :done :evidence ("fuzz_test.go: FuzzReceivePacket" ".github/workflows/ci.yml: test job, Fuzz step") :tested-revision "23e05fb5e411777a034bf695e081dc5eb001a60e")
  (:id "stats" :type :roadmap :title "Network stats"
   :text "Estimates of round trip time, jitter, packet loss and bandwidth, updated each frame."
   :rows (("counters" "counters for sent, received, acked, stale and duplicate packets")
          ("rtt" "RTT estimates (current, min, max, average)")
          ("jitter" "jitter estimates")
          ("packet-loss" "packet loss estimate")
          ("bandwidth" "sent, received and acked bandwidth"))
   :columns (("done" "done"))
   :cells (("counters" "done" "stats/counters")
           ("rtt" "done" "stats/rtt")
           ("jitter" "done" "stats/jitter")
           ("packet-loss" "done" "stats/packet-loss")
           ("bandwidth" "done" "stats/bandwidth")))
  (:id "stats/counters" :type :task :title "counters for sent, received, acked, stale and duplicate packets" :state :done :evidence ("reliable_test.go: TestDuplicatePackets" "reliable_test.go: TestStalePackets" "reliable_test.go: TestAckBufferOverflow") :tested-revision "23e05fb5e411777a034bf695e081dc5eb001a60e")
  (:id "stats/rtt" :type :task :title "RTT estimates (current, min, max, average)" :state :unknown
   :note "RTT, RTTMin, RTTMax and RTTAvg exist (endpoint.go) but no test asserts their values; a test with a known delay makes this done.")
  (:id "stats/jitter" :type :task :title "jitter estimates" :state :unknown
   :note "JitterAvgVsMinRTT, JitterMaxVsMinRTT and JitterStddevVsAvgRTT exist but no test asserts their values.")
  (:id "stats/packet-loss" :type :task :title "packet loss estimate" :state :unknown
   :note "PacketLoss exists but no test asserts its value; a test with deterministic loss makes this done.")
  (:id "stats/bandwidth" :type :task :title "sent, received and acked bandwidth" :state :unknown
   :note "Bandwidth exists but no test asserts its values.")
  (:id "compat" :type :roadmap :title "Compatibility"
   :text "Wire compatibility with the C reference library, mas-bandwidth/reliable, which is normative."
   :rows (("wire" "wire format byte-identical to C (golden transcript)")
          ("golden-regen" "the golden transcript is rebuilt from C on every run")
          ("standard" "wire standard (STANDARD.md) kept in sync with upstream")
          ("c-latest" "verified against the latest C release (1.4.5)"))
   :columns (("done" "done"))
   :cells (("wire" "done" "compat/wire")
           ("golden-regen" "done" "compat/golden-regen")
           ("standard" "done" "compat/standard")
           ("c-latest" "done" "compat/c-latest")))
  (:id "compat/wire" :type :task :title "wire format byte-identical to C (golden transcript)" :state :done :evidence ("wire_compat_test.go: TestWireCompatibility" "commit 69261a7 Lock in wire compatibility with the C library") :tested-revision "23e05fb5e411777a034bf695e081dc5eb001a60e")
  (:id "compat/golden-regen" :type :task :title "the golden transcript is rebuilt from C on every run" :state :done :evidence (".github/workflows/ci.yml: wire-compatibility job") :tested-revision "23e05fb5e411777a034bf695e081dc5eb001a60e")
  (:id "compat/standard" :type :task :title "wire standard (STANDARD.md) kept in sync with upstream" :state :done :evidence (".github/workflows/ci.yml: spec-sync job (STANDARD.md matches upstream)" "commit e0dd5cf Vendor STANDARD.md from mas-bandwidth/reliable, with a drift check (#3)") :tested-revision "23e05fb5e411777a034bf695e081dc5eb001a60e")
  (:id "compat/c-latest" :type :task :title "verified against the latest C release (1.4.5)" :state :todo
   :note "interop/regenerate.sh pins C reliable 1.4.4 (d4b6fe0); C released 1.4.5 on 2026-09-14 (reassembled packets never carry stale heap bytes; endpoint names cannot be over-read). Bump the pin, regenerate, and port what 1.4.5 implies.")
  (:id "quality" :type :roadmap :title "Quality"
   :text "How the library is checked on every change."
   :rows (("platforms" "Linux, macOS and Windows, Go 1.22 and stable")
          ("race" "race detector on every change")
          ("soak" "soak test")
          ("lint" "lint and vulnerability scan")
          ("no-alloc" "sending and receiving do not allocate"))
   :columns (("done" "done"))
   :cells (("platforms" "done" "quality/platforms")
           ("race" "done" "quality/race")
           ("soak" "done" "quality/soak")
           ("lint" "done" "quality/lint")
           ("no-alloc" "done" "quality/no-alloc")))
  (:id "quality/platforms" :type :task :title "Linux, macOS and Windows, Go 1.22 and stable" :state :done :evidence (".github/workflows/ci.yml: test job, matrix ubuntu-latest, macos-latest, windows-latest x Go 1.22, stable, green on 23e05fb") :tested-revision "23e05fb5e411777a034bf695e081dc5eb001a60e")
  (:id "quality/race" :type :task :title "race detector on every change" :state :done :evidence (".github/workflows/ci.yml: test job, go test -race") :tested-revision "23e05fb5e411777a034bf695e081dc5eb001a60e")
  (:id "quality/soak" :type :task :title "soak test" :state :done :evidence ("cmd/soak: go run ./cmd/soak --quiet 10000 in the ci.yml test job") :tested-revision "23e05fb5e411777a034bf695e081dc5eb001a60e")
  (:id "quality/lint" :type :task :title "lint and vulnerability scan" :state :done :evidence (".github/workflows/ci.yml: lint job (gofmt, vet, staticcheck, govulncheck)") :tested-revision "23e05fb5e411777a034bf695e081dc5eb001a60e")
  (:id "quality/no-alloc" :type :task :title "sending and receiving do not allocate" :state :unknown
   :note "README claims it and the benchmarks report allocations (reliable_bench_test.go), but nothing asserts zero; a test using testing.AllocsPerRun makes this done.")))

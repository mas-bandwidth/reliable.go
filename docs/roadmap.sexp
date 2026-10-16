; The reliable.go roadmap: the data ROADMAP.md is generated from.
; Same shape as nova-sprint's docs/roadmap.sexp (internal/roadmapdoc's package comment):
; data, never evaluated. Edit this file, then regenerate ROADMAP.md with nova-sprint's
; generator, from a nova-sprint checkout:
;   go build -o /tmp/roadmap ./tools/roadmap && (cd <this repo> && /tmp/roadmap --file docs/roadmap.sexp --out ROADMAP.md)
; Never edit ROADMAP.md by hand. Created 2026-10-14 when this repository's open GitHub issues and
; unfinished pull requests were moved into it.
(roadmap "v1"
 :title "reliable.go roadmap"
 :text "Planned work for reliable.go. The items below were open GitHub issues and pull requests, moved here on 2026-10-14 when roadmaps became the standard for future work (Glenn, 2026-10-14). Each item names its issue or pull request; the branches of moved pull requests are kept to resume from. Landed work is in git."

 :groups
 (
  (group "github-issues" :title "Moved from GitHub issues on 2026-10-14"
   :text "Open GitHub issues of mas-bandwidth/reliable.go, recorded here on 2026-10-14 when roadmaps became the standard for future work (Glenn, 2026-10-14). Each item names its issue; the issue is closed with a pointer here.")
 )

 :items
 (
  (item "issue-12" :group "github-issues" :area "reliable.go"
   :title "NewEndpoint does not require MaxFragments to cover MaxPacketSize; send truncates the count to uint8"
   :text "C reliable 1.4.x refuses to create an endpoint unless max_fragments * fragment_size covers max_packet_size. This Go port does not. NewEndpoint only checks MaxFragments ∈ [1,256] and FragmentSize > 0 (endpoint.go:125–136). On send, a packet above FragmentAbove is split with"
   :date "2026-10-14"
   :origin "mas-bandwidth/reliable.go issue #12, opened 2026-09-07 by gafferongames; moved here 2026-10-14"
   :kept (:github-issue "12" :labels ""))
 ))

// Package chainreplication implements a coordinator for chain replication:
// writes enter only at the head, propagate node by node toward the tail,
// and the tail commits and acknowledges them; reads are served solely by
// the committed state at the tail.
//
// # Sequence and commit rules
//
// The head assigns globally consecutive sequence numbers starting at 1 and
// records each accepted write. Every node applies writes strictly in
// sequence order: a message for a future seq is buffered (leaving a hole),
// and duplicate deliveries (already applied or already buffered) are
// discarded. Application of a write at the tail is the commit point; the
// tail returns a cumulative ACK covering its entire applied prefix. ACKs
// travel backward to the head; as they pass a node, that node's writes at
// or below the ACK cease to be pending confirmation. When the cumulative
// ACK reaches the head, each covered write gets its single "committed"
// result. Reads at the tail expose only the contiguous committed prefix, so
// an uncommitted (buffered/in-flight) write is never visible.
//
// # Failures and reorganisation
//
// On Fail(id) every message the failed node sent and every in-flight
// message touching it is discarded (the injected Network does this via
// FailNode), then the alive chain is repaired depending on position:
//
//   - Tail failure: the predecessor becomes the tail and immediately commits
//     all writes it currently holds (its whole pending-confirmation set),
//     issuing the cumulative ACK upstream. With one survivor that node is
//     both head and tail.
//   - Middle failure: the predecessor bridges directly to the failed node's
//     successor and resends exactly the writes whose sequence is greater
//     than the new successor's maximum applied seq and at most its own
//     maximum applied seq — the missing gap only, never duplicates.
//   - Head failure: the successor becomes the head. Writes the new head has
//     not applied are reported uncommitted (exactly once), buffered orphan
//     values behind such holes are dropped, and the new head resends held
//     writes beyond its successor's watermark so they can still commit. If
//     the surviving tail had already committed further writes whose ACKs
//     were lost with the old head, the tail re-issues its cumulative ACK.
//
// After head failure the global sequence keeps increasing (numbers of
// uncommitted writes are not reused). When only one node remains it acts as
// both head and tail: a write applies and commits synchronously with no
// network hop.
//
// # Rejections
//
// The following are rejected wholesale, with distinct sentinel errors, and
// change no state: empty chain at construction; empty or duplicate node id;
// writing at a non-head node; reading at a non-tail node; announcing an
// unknown node, an already-failed node, or failing the last alive node.
//
// # Concurrency and determinism
//
// Write, Read, Deliver and Fail are all safe to call concurrently (a single
// mutex serialises protocol state). At every instant the applied sequence
// set at a later node is a prefix of that at every earlier node; every
// accepted write receives exactly one final result, committed or
// uncommitted; and replaying the same operation and delivery script
// produces identical results. The coordinator logs every input, output and
// the rationale for each decision (INPUT/OUTPUT/DECIDE/REJECT/DISCARD).
package chainreplication

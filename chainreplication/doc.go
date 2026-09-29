// Package chainreplication implements a chain replication coordinator.
//
// Writes enter only at the head, which assigns sequence numbers starting at 1
// consecutively. A write message travels head -> ... -> tail through the
// injected Network, which may delay, reorder or duplicate it. Each node
// applies writes strictly in sequence order: a message past a gap is buffered,
// a duplicate (seq already applied, or already buffered) is discarded.
// Application at the tail is the commit point: the tail answers with an ack
// that travels back upstream; every node clears its unacknowledged writes with
// sequence not greater than the ack. The write's outcome channel is resolved
// exactly once (committed when the ack reaches the head, uncommitted only when
// the write can provably never reach a tail).
//
// Failure handling (all messages sent by or addressed to a failed node, and
// everything still in flight to/from it, are discarded):
//   - Tail fails: its predecessor becomes tail and immediately commits every
//     applied-but-unacknowledged write; acks then propagate upstream.
//   - Middle node fails: the predecessor retransmits to the new successor
//     exactly the writes whose sequence is greater than the successor's
//     largest applied sequence (and not greater than the predecessor's own).
//   - Head fails: the successor becomes head; writes the new head never
//     applied are reported uncommitted, stale buffered copies are purged, and
//     new numbering continues without gaps above its applied prefix.
//
// With a single survivor that node is both head and tail. Reads are served
// only by the tail and only expose committed (tail-applied) state, so a read
// can never return an uncommitted write. Every public method is safe for
// concurrent use; the same sequence of operations and deliveries replays to
// identical results.
package chainreplication

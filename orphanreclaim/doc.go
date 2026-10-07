// Package orphanreclaim implements generational garbage collection of orphan
// objects across heterogeneous link types on an ontology platform.
//
// Each configured link type contributes to retention in exactly one of two
// ways:

//   - independent retention: a single inbound edge of that type keeps the
//     object regardless of any other type;
//   - joint retention: edges are sufficient only when every type of a named
//     group is present at the same time.
//
// Orphan classification always checks the independent layer first and the
// joint layer second; the exact same classifier is used for entering and for
// leaving a reclaim queue. Orphans first enter generation 1, are promoted to
// generation 2 only after the generation-1 grace period elapses while still
// orphaned, and are purged only after the (typically shorter) generation-2
// grace period elapses. Re-referenced objects are removed from whichever
// queue they occupy with all generation memory cleared, so a later orphaning
// always restarts at generation 1.
//
// All state mutations are serialized by one mutex, which is what makes the
// implementation equivalent to some total order of concurrent operations.
// Classification reads only per-link-type counters, so the number of checks
// is bounded by the configuration size and never grows with an object's
// inbound-edge count (see TestDecisionChecksBoundedByConfigNotEdgeCount).
package orphanreclaim

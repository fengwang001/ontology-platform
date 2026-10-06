// Package refupdate implements a code-hosting style reference update
// transaction processor: a push carries multiple reference update
// instructions, each adjudicated against the commit graph, protected-ref
// rules and pusher permissions, and the whole batch commits or aborts.
//
// # Concepts
//
// Commits form an append-only DAG (Graph); references map names in the
// "refs/heads/" (branch) and "refs/tags/" (tag) namespaces to commits.
// A push is a slice of Instruction values passed to Store.Push, which returns
// one PushReport containing a per-instruction ItemResult.
//
// # Verdicts
//
// Rejected instructions carry the first applicable RejectReason in precedence
// order (see the RejectReason constants). Individually valid instructions in a
// batch that fails for another reason carry SkippedDueToOtherReject, or
// RejectBatchConflict when they themselves participate in a batch conflict.
// A rejected push changes no references, audit sequences or audit records.
//
// # Guarantees
//
// Pushes are strictly serialized by Store, so concurrent pushes against the
// same old value cannot both succeed and readers never see a partial batch.
// Fast-forward checks cost O(size of the new commit's ancestor cone) and rule
// matching costs O(number of segments in the reference name); both claims are
// verified by counter-based tests.
package refupdate

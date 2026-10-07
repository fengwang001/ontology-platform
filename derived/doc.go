// Package derived implements transitive derived property indexes for the
// ontology platform.
//
// A Declaration binds an index name to a link type and a source property on
// the linked object type. A downstream instance's entry for the declaration
// is derived, at all times, purely from the current link structure and base
// property values: the entry is never stored as independently mutable state.
// SourceProperty may name another declaration, which yields multi-level
// transitive derivation.
//
// # Consistency model
//
// Every mutation (property write, link add/delete, instance delete) is one
// strict-serializable processing unit. The unit builds a deep-copied
// candidate snapshot, validates structural preconditions, recomputes every
// affected derived entry, and publishes the snapshot by a single pointer
// swap only if all updates succeed. Readers observe either the old or the new
// complete snapshot, never a mix, so concurrent readers are equivalent to
// reads at one consistent linearization point.
//
// Entries carry an explicit state: INDEXED or one of the non-indexable
// states NO_LINK, NOT_UNIQUE, NO_VALUE, SOURCE_GONE. Uniqueness-breaking link
// changes flip the entry to NOT_UNIQUE inside the link's own unit, so the
// transition can never be observed later than the link change.
//
// # Affected-set computation
//
// The set of entries a unit must touch is computed by a reverse value-flow
// BFS over in-adjacency along declaration links, with per-(instance,
// property) visit marks that guarantee each downstream instance is counted
// exactly once and no extra instance is touched. The result is cross-checked
// in tests against an independent oracle (NaiveModel).
//
// # Cycle handling
//
// A new link is refused before it becomes visible if, in conjunction with a
// declaration chain, it would close an instance-level transitive value loop.
// The check models resolution states (instance, declaration) and searches
// for a path that would traverse the hypothetical edge again.
//
// # Error priority
//
// SourceNotFound > UnsupportedLinkType > NotUnique > CycleDetected >
// DownstreamUpdateFailed; when multiple conditions hold the highest-priority
// error is reported.
package derived

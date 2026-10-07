// Package ontology implements an ontology object store whose central
// feature is an atomic, externally linearizable batch update that may span
// several object instances of different object types.
//
// A batch either commits as a whole or is rejected as a whole. Within a
// batch every item is checked, in order, for:
//
//  1. duplicate write declarations (a batch-local programming error),
//  2. optimistic-version conflicts against the declared baseline,
//  3. per-object-type validation hooks,
//  4. link-cardinality constraints evaluated on the post-batch image.
//
// Concurrency control is deterministic ordered per-instance locking, which
// is equivalent to some serial execution order for every set of batches.
package ontology

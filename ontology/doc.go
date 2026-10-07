// Package ontology implements an in-memory ontology store with atomic
// multi-instance, multi-type batch updates.
//
// A Batch either commits as one externally indivisible unit or leaves every
// version, link and the logical clock untouched. Commit decisions follow a
// fixed, mutually exclusive failure order:
//
//	duplicate write -> version conflict -> validation hook -> cardinality
//
// Per-instance base versions are checked individually; cardinality is
// validated once on the batch's final link state rather than on any
// intermediate state. See docs/DESIGN.md for the concurrency-control
// rationale, cost-bound evidence and verification approach.
package ontology

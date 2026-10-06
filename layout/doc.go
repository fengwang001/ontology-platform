// Package layout implements composite-type memory layout calculation and
// binary-compatibility checking for a compiler backend.
//
// # Model
//
// Clients register basic types (fixed size/alignment) and composite types
// made of named fields. A field is either embedded (laid out inline; the
// target must be registered and the embedding graph acyclic) or indirect (a
// configured-size pointer; the target may be undefined or participate in a
// cycle). Fields may be compact (no offset alignment, excluded from the
// type's alignment), and a type may cap field alignment with MaxAlign.
//
// Modifying a type produces a new version and propagates recomputation only
// through direct embedders; indirect referrers are untouched. Propagation
// stops on any branch whose recomputed layout is fully compatible. All
// candidate layouts are computed before any state change, so a modification
// that would oversize a dependent is rejected atomically.
//
// The entry points are NewRegistry, Registry.RegisterBasic,
// Registry.Register, Registry.Modify, Registry.Get and Registry.View. See
// DESIGN.md for the module breakdown and complexity arguments.
package layout

// Package lifecycle implements the object lifecycle state-machine
// subsystem with lazily settled, time-driven ("due") transitions.
//
// Due transitions do not require a background scanner: every Read or Action
// first settles every due transition that should already have happened
// (including multi-step chains and declared cross-instance cascades) and
// only then returns state or evaluates the explicit action's preconditions.
//
// Settlement is backed by per-instance materialized state plus an
// append-only History. StateAt re-derives a state at an arbitrary past time
// from that history alone. An independent naive periodic Scanner is provided
// for randomized differential testing.
//
// See DESIGN.md for the full rationale, rejected alternatives and the local
// verification procedure.
package lifecycle

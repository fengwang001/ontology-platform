// Package microgrid implements a deterministic dispatch controller for one
// storage unit in a microgrid.
//
// It manages state of charge, charge/discharge power limits, critical-load
// reserve, grid-tied and island modes, per-slot dispatch plans with
// suffix revocation after forecast updates, post-execution deviation
// revalidation, and cumulative-throughput maintenance locking.
//
// All energy quantities are integers; time advances by increasing slot
// numbers. Every public Controller method is serialized by one mutex, so
// concurrent calls are equivalent to some serial execution and identical
// operation sequences replay to identical SoC trajectories and revocations.
//
// See DESIGN.md for the rationale and verification commands.
package microgrid

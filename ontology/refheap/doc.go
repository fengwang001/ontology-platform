// Package refheap implements a heap-reclamation model with soft, weak and
// phantom (虚) references plus finalization and resurrection.
//
// # Model
//
// Object ids start at 1 and are shared by ordinary objects and reference
// objects; reference queues start at 1. All operations are linearized under
// one mutex, so one Collect is observed as a single atomic step.
//
// # Collect phases (fixed order; within a phase ids ascend)
//
//  1. Strong marking: roots plus objects waiting in the finalization queue are
//     seeds; fields of ordinary objects propagate marks. A marked reference
//     object never traverses to its target. used0 is the total size of marked
//     objects at the end of this phase.
//
//  2. Soft references: for each soft ref marked at the end of phase 1, with an
//     unmarked non-null target, retain it exactly when
//
//     now - timestamp <= floor((C-used0)/U) * M
//
//     free = C-used0 (and therefore the window) is computed once at phase
//     start and never shrinks as targets are retained in this phase. Retained
//     targets propagate marks through ordinary-object fields. Otherwise the
//     target is cleared and the reference is enqueued (if it has a queue).
//
//  3. Weak references: each marked weak ref with an unmarked target clears and
//     enqueues it. This happens before finalization, so a target resurrected
//     in phase 4 still reads back as null.
//
//  4. Finalization: every ordinary object that is unmarked, has its
//     finalizable flag set and is not already queued is selected in one pass
//     (ascending ids appended to the FIFO finalization queue); the whole
//     selection plus everything field-reachable is then marked (resurrected).
//     Selecting before marking guarantees that a finalizable object reached
//     from another selected object joins the same round.
//
//  5. Catch-all: any still-marked reference (of any kind) with an unmarked
//     target clears and enqueues it; this also covers reference objects that
//     only became reachable through phase-4 resurrection.
//
//  6. Sweep: every unmarked object is reclaimed and used drops by its size.
//
// Finalize(n) pops at most n FIFO entries and clears their finalizable flags.
// Objects waiting for finalization are phase-1 seeds, so they survive until
// Finalize runs even when unreachable.
//
// # Rejection order
//
// Each rejected operation reports only the first failure in this order:
// invalid argument -> object/queue not found -> wrong object kind -> clock
// moved backwards -> heap full. Rejected operations mutate no state.
//
// # Verification
//
// See README.md for the local test commands, including 2000 random
// differential cases against an independent naive set-based simulator.
package refheap

// Package topk implements a retractable top-K maintainer.
//
// A Maintainer keeps the K highest-scoring elements of a bounded universe and
// supports three operations:
//
//   - Upsert: insert a new element or overwrite an existing element's score.
//   - Withdraw: remove an element (idempotent; unknown ids are no-ops).
//   - TopK: read the ordered top n elements.
//
// # Ordering key and ties
//
// Every element has a unique rank under the composite key:
//
//	(score DESC, id ASC)
//
// i.e. higher score wins; when scores are equal the lexicographically
// smaller identifier wins. Because the identifier is unique, ranks can never
// be ambiguous, so every result is deterministic and reproducible. When
// several elements share the threshold score, exactly the smallest ids among
// them occupy the last slots.
//
// The maintainer stores every active element (including elements currently
// outside the top-K view) in one canonical sequence ordered by that key, so
// TopK(n) is simply the length-n prefix of that sequence. Withdrawing an
// element inside the view makes the best outside-view element move in
// immediately; withdrawing an outside-view element never changes the view.
// Withdrawing an element also releases its universe slot.
//
// # Error handling
//
// All failures are reported via distinguishable sentinel errors and leave
// state untouched:
//
//   - ErrNonPositiveK: New or TopK received k <= 0.
//   - ErrKExceedsCap: New received capacity < k.
//   - ErrKExceedsLimit: TopK requested more than the configured K.
//   - ErrEmptyID: Upsert received an empty identifier.
//   - ErrInvalidScore: Upsert received a NaN or infinite score.
//   - ErrCapacityFull: Upsert of a new id when the universe is full;
//     overwriting an existing id stays allowed while full.
//
// # Concurrency
//
// TopK, Count and Snapshot take a read lock and return freshly allocated
// copies, so they may run concurrently with each other. All concurrent reads
// observe the same point-in-time state and therefore return elementwise
// identical results.
package topk

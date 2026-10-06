// Package exam implements a question-bank versioning and exam-paper
// assembly constraint engine.
//
// Responsibilities are split across files:
//
//   - question.go: questions, immutable versions, lifecycle states.
//   - mutex.go: mutex groups and their transitive closure, maintained as
//     incrementally updated connected components so a conflict check costs
//     O(paper size) map reads regardless of bank/group totals.
//   - paper.go: papers, assembly constraints, pure constraint checkers.
//   - engine.go: the serialized facade (lifecycle transitions, draft
//     editing, all-or-nothing publish, atomic replacement, queries).
//   - errors.go: the distinguishable error categories with fixed priority.
//
// Every Engine method holds one mutex, so concurrent callers observe
// results equivalent to some serial order. At any serial point: every
// published paper satisfies the constraints as of its publish time (its
// bound versions are frozen), and every invalid paper is invalid exactly
// because it contains a retired question.
package exam

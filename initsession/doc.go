// Package initsession solves package-level variable initialization
// order: callers register variable initialization units and function
// declarations one by one in source order, and a Session derives the
// unique deterministic order in which the units must initialize.
//
// # Model
//
// An identifier is a non-empty string of ASCII letters, digits and
// underscores that does not start with a digit. The single underscore
// "_" is the blank identifier: it may appear on the left-hand side of a
// variable unit only, each occurrence is a distinct throwaway, it can
// never be referenced and never participates in redeclaration checks.
//
// A variable unit binds one or more variables that initialize together
// and carries the set of identifiers directly mentioned by its
// initializer. A function declaration binds a name in the same
// namespace and carries the identifiers directly mentioned by its body;
// functions need no initialization and must not be named "_".
//
// Dependencies are transitive: referencing a variable depends on that
// variable; referencing a function depends on the variables its body
// reaches, following function-to-function references (mutual recursion
// is allowed). Forward references are accepted at registration time and
// resolved at solve time.
//
// # Ordering
//
// Solve repeatedly chooses, among units not yet initialized, the
// source-earliest one whose depended-on variables are all initialized
// (predeclared identifiers count as initialized), marks all of its
// variables initialized, and repeats. The result is a specific
// topological order, not an arbitrary one.
//
// # Errors
//
// Registration rejects invalid arguments before redeclarations, and a
// rejected registration changes no state and occupies no source
// position. Solving first reports undeclared references across every
// registered declaration (earliest declaration in registration order,
// lexicographically smallest bad identifier) and otherwise reports an
// initialization cycle listing every variable that can never
// initialize, in source order and without blanks.
//
// # Concurrency
//
// Sessions are safe for concurrent use. Solve takes a consistent
// immutable snapshot and never mutates session state, so it cannot see
// a half-registered unit.
package initsession

// Package deadcode computes the least fixed point of retained modules and
// declarations for a module graph.
//
// The main workflow is:
//
//  1. register immutable Module values in a concurrency-safe Registry;
//  2. create a Session with immutable entry module identifiers;
//  3. call Session.Solve to analyze the session's read-only snapshot.
//
// Solve returns sorted included modules, sorted retained declarations, and the
// highest-precedence reason for each retained declaration.
package deadcode

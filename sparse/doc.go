// Package sparse implements a sparse-checkout rule engine.
//
// A workspace has a current commit (an immutable file tree), a current
// ruleset version, and local-modification marks on materialized paths.
// Rules are ordered include/exclude entries over three pattern forms:
// exact paths, directory prefixes ("dir/", all descendants), and
// single-level wildcards ("dir/*", direct children only). The last matching
// rule wins; unmatched paths are not materialized. A materialized file
// implies materialized ancestors; a directory with no materialized
// descendant file is itself not materialized.
//
// Engine.Apply switches commit and/or ruleset atomically: if any path to
// dematerialize carries a local modification, the whole change is rejected
// (unless forced), leaving commit, ruleset, materialized set and marks
// untouched. All operations are linearizable under one RWMutex.
//
// Cost model: single-path decisions walk only the path's own trie branch
// (O(depth)); commits and rulesets are Merkle-hashed tries, so a change
// touches only changed scopes and never iterates unaffected materialized
// paths. Engine.Stats exposes work counters that tests use to prove this.
package sparse

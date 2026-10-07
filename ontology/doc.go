// Package ontology implements an ontology link graph with a tri-state
// reachability decision procedure.
//
// Two permission kinds are kept separate:
//
//   - Existence permission controls whether a caller may know that an
//     object instance exists. Without it the object is treated as absent
//     for that caller, and answers never distinguish "missing" from
//     "hidden".
//   - Traversal permission, granted per link type, controls whether links
//     of that type participate in a search. A denied link never affects
//     the independent existence status of its endpoints.
//
// Reachable returns one of three mutually exclusive outcomes: Reachable,
// Unreachable, or Restricted (unknown because visibility or traversal
// cuts prevent proving either direction). All graph and permission
// mutations, together with query snapshots, are linearized through one
// lock, so concurrent histories are equivalent to some serial order.
package ontology

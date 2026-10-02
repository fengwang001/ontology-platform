// Package rbac implements a hierarchical-role RBAC manager with static and
// dynamic separation of duty (SSD/DSD) constraints.
//
// Definitions:
//   - Juniors(r): r itself plus every role reachable from r along inheritance
//     edges (senior, junior). Holding senior implies holding junior's perms.
//   - Auth(u): union of Juniors(a) over all roles directly assigned to u.
//   - A(s): explicitly activated roles of session s; each must be in Auth(owner).
//   - Eff(s): union of Juniors(a) over a in A(s).
//   - SSD (name, RS, n): for every user u, |Auth(u) ∩ RS| < n.
//   - DSD (name, RS, n): for every session s, |Eff(s) ∩ RS| < n.
//
// All operations are serialized by a single mutex, so concurrent calls are
// equivalent to some serial order and every Check observes a consistent state.
// Rejected operations are fully validated before any mutation, hence are
// side-effect free.
package rbac

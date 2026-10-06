/*
Package gradaudit is a deterministic credit-substitution and graduation-audit
engine.

# Concepts

A PlanVersion is an immutable requirement tree: leaf requirements name the
courses that may count plus minimum credits/course count; internal
requirements require a minimum number of satisfied children. Students bind to
one version at enrollment and move only to a newer version via Migrate.

Only non-revoked records at or above PassScore count. Repeated attempts of a
course collapse to the highest score (ties: earliest semester, then earliest
registration). Transferred passing records are admitted in registration order
up to TransferCap; later ones never count.

An approved Substitution (plan-scoped, semester-gated) lets a record claim a
target course with min(own, target) credits; the substituted identity is then
subject to the same repeat and assignment rules. One counted record is
assigned to at most one leaf, except leaf pairs declared in SharedPairs.

# Audit

Audit enumerates every feasible leaf assignment; the tree passes iff some
assignment satisfies the root. On failure it attributes the smallest-coded node
that fails under every assignment, with the gap produced by the most favorable
assignment. Total credits, credit-weighted GPA and unresolved required-course
failures are additional clauses; all must hold for graduation.

Rejected operations return *OpError with a fixed-priority ErrorKind and never
mutate state. All operations are safe for concurrent use.

See DESIGN.md for the full rationale and the test files for usage examples.
*/
package gradaudit

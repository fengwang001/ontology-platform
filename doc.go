// Package rebalance implements a partition-count rebalancer on top of a
// deterministic hash ring.
//
// # Ownership
//
// A record with key k belongs to partition fnv1a64(k) mod N, where N is the
// active partition count. The hash and modulus are fixed, so placement is
// reproducible from the key alone.
//
// # Rebalance plan
//
// BeginRebalance(target) scans the partitions and records exactly the keys
// whose target partition fnv1a64(k) mod target differs from their current
// one. That list is sorted by key (deterministic, reproducible order) and a
// cursor starts at 0.
//
// # Per-record migration
//
// MigrateSteps processes list entries in order. For each key it copies the
// value into the target partition, deletes it from the old partition, and
// only then increments the cursor. A key is therefore present exactly once
// at all times, and MigrateSteps can never process a key twice or skip one,
// including after a crash restart that restores the cursor.
//
// # Dual routing during migration
//
// While phase == migrating, the current owner of key k is decided by the
// cursor and the sorted list:
//
//   - k is in the list with index i < cursor  => target layout: hash(k) mod target
//   - otherwise (i >= cursor, or not in list) => old layout:    hash(k) mod N
//
// Both Get and Put route to that single current partition. Writes of
// previously unknown keys are rejected (ErrNewKeyWhileMigrating); updates to
// existing keys follow the same cursor-based route.
//
// # Commit
//
// Commit is accepted only when cursor == len(list). It then promotes target
// to the active N and clears migration state. Committing earlier returns
// ErrMigrationIncomplete.
//
// # Crash resume
//
// Snapshot serializes partitions, n, target, phase, list and cursor;
// Restore rebuilds an equivalent store. Because routing derives only from
// the list index and cursor, resumed reads are correct immediately and the
// remaining suffix of the list is migrated exactly once.
//
// # Errors are atomic
//
// Every rejected call (invalid target, duplicate begin, migrate/commit
// outside a migration, early commit, new key during migration) returns a
// distinguishable sentinel error and mutates nothing.
//
// # Concurrency
//
// All state is guarded by one RWMutex: migrations and writes take the write
// lock, Get and Progress take the read lock. Every individual key read is
// equivalent to a sequential read at the observed cursor, and the cursor
// only moves forward, so Progress is monotonic.
package rebalance

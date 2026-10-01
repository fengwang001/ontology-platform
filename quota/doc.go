// Package quota implements a concurrent directory-tree quota ledger with
// rename transfers, byte reservations and atomic batches.
//
// # Identifiers
//
// The tree starts with the root directory, id 0. Every successful Mkdir or
// AddFile receives the next consecutive id starting from 1. Rejected
// operations and rolled-back batches never consume an id.
//
// # Usage and effective bytes
//
// For a directory d:
//   - subtree bytes = sum of sizes of all files inside d;
//   - subtree entries = number of files and directories strictly inside d;
//   - own reserve r_d (bytes, initially 0);
//   - subtree reserve R(d) = sum of r over all directories inside d
//     including d itself;
//   - effective bytes E(d) = subtree bytes + R(d).
//
// The bytes dimension of a quota is always checked against E(d); the entries
// dimension against subtree entries. Usage(d) returns (bytes, entries, R(d)).
//
// # Reservation rules
//
// Reserve(d, b) checks E+b against the quotas of d and every ancestor, then
// adds b to r_d. Release(d, b) requires b <= r_d and subtracts it without
// any quota check. AddFile(p, size) first computes c = min(size, r_p), checks
// the net increment size-c in effective bytes and +1 entry along p and its
// ancestors, then consumes c from r_p; E therefore rises by exactly size-c.
// When the reserve exceeds the file size, only size is consumed.
//
// # Rename
//
// Rename(x, p) moves the whole subtree of x under p. The payload is
// subtree-bytes plus R(x) in the bytes dimension (a file simply pays its
// size), and one entry plus all inner entries in the entries dimension.
// Directories that are on the new parent chain but not on x's old parent
// chain receive the payload and are quota-checked; directories only on the
// old chain are refunded; common ancestors neither change nor are checked.
// Reserves move together with their owning directories. Renaming x onto its
// current parent succeeds and changes nothing.
//
// # Batches
//
// Batch applies Mkdir, AddFile, Resize, Remove, Rename, Reserve, Release and
// SetQuota ops in order; later ops may reference ids allocated earlier in
// the same batch. If any op is rejected the whole batch is rolled back
// (usage, reserves, quotas and ids) and the returned error reports the
// failing index and satisfies both ErrBatch and the underlying cause via
// errors.Is. An empty batch is rejected as an invalid argument.
//
// # Rejection order
//
// The first violation, in this order, is reported: invalid argument; node
// not found (x before p); type mismatch; structural error (root, non-empty
// removal, moving a directory into itself or a descendant); insufficient
// reserve; bytes quota exceeded; entries quota exceeded; or a SetQuota
// value below current usage. Quota checks walk nearest-first and, at each
// directory, bytes before entries.
//
// All methods are safe for concurrent use and Rename/Batch are atomic with
// respect to concurrent Usage queries.
package quota

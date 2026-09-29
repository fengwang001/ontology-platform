// Package evolution is a change-capture schema-evolution column mapper.
//
// It projects events written against older schema versions onto a current
// schema version, aligning columns by name (never position) and applying
// decidable int/string type conversions.
//
// Projection rules
//
//   - Columns are matched by name; event key order is irrelevant.
//   - A target column missing from the event is zero-filled when optional
//     (int -> int64(0), string -> "") and rejects the whole event when
//     required (reason required_column_missing).
//   - int -> string always succeeds (strconv.FormatInt). string -> int
//     succeeds only when the content parses fully as a base-10 integer
//     (strconv.ParseInt); otherwise the whole event is rejected with
//     conversion_failed.
//   - Event columns absent from the target schema were deleted and are
//     silently dropped; the drop is still recorded in the report.
//   - All rejections are whole-event/all-or-nothing and carry a
//     distinguishable RejectReason.
//
// # Concurrency
//
// Every version is an immutable Snapshot. Registry reads take RLock and
// return snapshots; evolution takes Lock, builds the new version in a
// private working copy and publishes it only after full validation, so a
// failed Evolve leaves the registry unchanged. ProjectEvent pins the
// current snapshot under one read lock; the projection itself runs
// lock-free against the immutable snapshot, so concurrent projections
// during continuous evolution are always based on one complete version and
// never mix two schemas. The same version with the same values always
// produces identical rows and reports.
package evolution

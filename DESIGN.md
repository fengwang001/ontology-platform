# Incremental Aggregate View Maintenance With Retraction

## 1. Scope
Base-table changes (insert/delete/update) arrive as a versioned change stream.
A grouped aggregate view is maintained in process memory; a CRC32-protected
journal in a local temp directory enables crash recovery. Stdlib only.

## 2. Change model (package change)
Op is Insert/Delete/Update; Update carries (OldKey,OldVal)->(NewKey,NewVal),
i.e. a delete from the old group plus an insert into the new group. Every
change has a strictly increasing Version (uint64). Envelope encoding:
body = 1 byte op | uvarint version | key/value pairs (length-prefixed key,
float64 bits for value); Update encodes both pairs. Record = uvarint
len(body) | body | uint32 CRC32(IEEE) over body. Empty group key is legal; a
truly missing key yields change.ErrMissingKey and is rejected before
journaling. NaN values are rejected (change.ErrNaN). +0/-0 compare equal:
both normalize to the +0.0 bit pattern.

## 3. Aggregator withdrawal analysis (package agg)
An aggregate is incrementally withdrawable iff S' = W(S, v) is uniquely
determined by (S, v) alone.
- Count: S=n, W gives n-1 exactly. Incremental, no members needed.
- Sum: S=sum, W gives sum-v exactly. Incremental, no members needed.
- Min: if v > min it is unchanged; if v == min, S alone cannot reveal the
  runner-up (ties/unique holder indistinguishable from S). Recompute members.
- Max: symmetric to Min; v == max forces recompute.
- DistinctCount: removing v lowers the count only if no other record still
  holds v; multiplicities live in the member multiset, not in S. Recompute.
Each aggregator declares NeedsMembersOnDelete(); Min/Max/DistinctCount=true.
Insert is always incremental.

## 4. View orchestration: Apply -> Recompute -> Commit
Per-group member maps id->record and per-group aggregator states are kept.
Apply folds +delta/-delta into shadow states; a retraction that cannot be
solved from state (deleted value == current Min/Max, or last copy of a
distinct value) marks the group dirty. Recompute rebuilds flagged aggregators
by scanning exactly that group's current members, incrementing non-exported
counters recomputes and membersVisited (bounded by group size, never crossing
groups). Commit publishes shadows under one mutex and advances lastVersion.
Deleting the last member removes the whole group; lookup then returns
ErrGroupNotFound, never a Count=0 empty group. Count/Sum never recompute.

## 5. Version idempotence and ordering
WAL: a change is appended (and synced) before Apply. lastVersion is applied
max. v == lastVersion -> idempotent no-op (fields unchanged). v < lastVersion
-> ErrStaleVersion. v != lastVersion+1 otherwise -> ErrOutOfOrder; both are
rejected and counted, and the view is untouched. Replay stops at the first
incomplete/corrupt frame, so the recovered view equals a full recomputation
over exactly the complete record prefix.

## 6. Journal format and truncation taxonomy
File = magic "ONTJNL01" followed by concatenated frames:
uvarint bodyLen | body | uint32 big-endian CRC32(body).
Classification of any byte prefix shorter than the file:
- header incomplete: fewer than 8 magic bytes.
- length prefix incomplete: EOF while decoding the uvarint.
- body incomplete: fewer than bodyLen body bytes after the prefix.
- crc mismatch: a complete frame shape whose CRC differs. Cutting 1..3 bytes
  off the trailing CRC leaves a frame-shaped prefix whose residual 4-byte
  word (short bytes read as zero) mismatches; the truncation test therefore
  exercises all four classes deterministically across byte positions.

## 7. Crash points
Crashes injected mid-Apply, mid-Recompute, and immediately before Commit all
occur after the change has been journaled. Replay re-runs the deterministic,
idempotent pipeline, so all three recoveries are field-identical to the
non-crashing run.

## 8. Concurrency
One RWMutex guards Commit; RLock readers see a coherent per-group snapshot,
never Count updated without Sum. Concurrent writes to a group recomputing are
coalesced under the same critical section; no write is lost. -race clean.

## 9. Audit
audit independently materializes id->record from the full log, recomputes
every group/aggregator, and compares with math.Float64bits: Sum equality is
IEEE754 bit-exact; any group mismatch is reported.

## 10. File budget
9 .go files: change/change.go, agg/agg.go, journal/journal.go, view/view.go,
audit/audit.go, journal/journal_test.go, view/view_test.go,
audit/audit_test.go, cmd/demo/main.go; each <= 200 lines.

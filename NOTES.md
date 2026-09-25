# Snapshot compaction — derivation

Eight records arrive by version; seg1 = v1–5, seg2 = v6–8; Compact receives [seg2, seg1].

| # | record | key newest | key live | live snapshot |
|---|---|---|---|---|
| 1 | 1 a put x | (1,put,x) | a=x | {a:x} |
| 2 | 2 b put y | (2,put,y) | b=y | {a:x,b:y} |
| 3 | 3 a put x2 | (3,put,x2) | a=x2 | {a:x2,b:y} |
| 4 | 4 c put z | (4,put,z) | c=z | {a:x2,b:y,c:z} |
| 5 | 5 b del | (5,del) | b deleted | {a:x2,c:z} |
| 6 | 6 a del | (6,del) | a deleted | {c:z} |
| 7 | 7 c put z2 | (7,put,z2) | c=z2 | {c:z2} |
| 8 | 8 b put y2 | (8,put,y2) | b=y2 | {b:y2,c:z2} |

(甲) a final: absent (tombstoned at v6). A bug ignoring tombstones, taking max-version put, yields a=x2 (v3): stale data resurrection (数据复活).
(乙) Watermark = 8, the global max version. Bug "version of last key-sorted output record": output is b(v8),c(v7), last is v7 → wrong watermark 7; replay starts at 8 and repeats record v8 (b put y2).
(丙) Later-listed seg1 overrides earlier seg2: b → v5 del beats v8 put y2, b wrongly missing; c → v4 put z beats v7 put z2, c wrongly z.

## Invariants — where guaranteed / pinning test

1. View == batch recompute: api.go Ingest keeps one newest-pointer per key in `latest`, View projects live puts; pinned by api_test.go TestViewMatchesBatch.
2. Compact output self-consistent (≤1 rec/key, key-sorted, no del): comp.go Compact emit loop over sorted keys skipping tombstones; pinned by comp_test.go TestCompactOutputInvariants.
3. Watermark monotone, equals max version ever seen: api.go `maxVer` advanced only in Ingest/Compact; pinned by api_test.go TestWatermarkMonotone.
4. Rejected op leaves no trace: api.go validates all input before any mutation (Ingest pre-check; Compact validates every segment first); pinned by api_test.go TestRejectLeavesNoTrace.

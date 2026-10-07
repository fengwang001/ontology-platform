package reflog

import (
	"fmt"
	"testing"
)

// BenchmarkExpireVsUnexpired proves that one expiry pass costs
// O(log n + expired): the time stays flat as the number of UNEXPIRED
// records grows from 1e3 to 1e6, because expiry is a binary-search
// prefix cut per tier rather than a scan.
func BenchmarkExpireVsUnexpired(b *testing.B) {
	cfg := Config{ReachableRetention: 100, UnreachableRetention: 50, FreshnessGrace: 0}
	const expired = 1000
	const now = 20000
	for _, unexpired := range []int{1_000, 10_000, 100_000, 1_000_000} {
		b.Run(fmt.Sprintf("unexpired=%d", unexpired), func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				b.StopTimer()
				l := &refLog{}
				for j := 0; j < expired; j++ {
					l.unreachable = append(l.unreachable, &Record{Seq: uint64(j), Time: int64(j)})
				}
				for j := 0; j < unexpired; j++ {
					l.unreachable = append(l.unreachable, &Record{Seq: uint64(expired + j), Time: now - 40})
				}
				b.StartTimer()
				l.expire(now, cfg)
				b.StopTimer()
			}
		})
	}
}

// BenchmarkGCVsLogRecords proves that the per-object liveness check
// does not grow with the total number of log records: GC over a
// fixed set of 500 live objects takes the same time whether the log
// holds 0 or 1e6 expired records, because expired prefixes are
// skipped by binary search and liveness is a hash lookup.
func BenchmarkGCVsLogRecords(b *testing.B) {
	cfg := Config{ReachableRetention: 100, UnreachableRetention: 50, FreshnessGrace: 1_000_000_000}
	const now = 5_000_000 // every record below is expired at this time
	for _, records := range []int{0, 1_000, 100_000, 1_000_000} {
		b.Run(fmt.Sprintf("logRecords=%d", records), func(b *testing.B) {
			s := &System{cfg: cfg, store: newStore(), logs: make(map[string]*refLog)}
			for i := 0; i < 500; i++ {
				s.store.putCommit(&Commit{
					ID:             CommitID(fmt.Sprintf("c%d", i)),
					Size:           1,
					FirstWrittenAt: now, // always fresh: stable work per GC
				})
			}
			l := &refLog{}
			for j := 0; j < records; j++ {
				l.unreachable = append(l.unreachable, &Record{
					Seq:  uint64(j),
					Time: int64(j), // all expired at now
					Old:  "x",
					New:  "y",
				})
			}
			s.logs["r"] = l
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				s.gcLocked(now)
			}
		})
	}
}

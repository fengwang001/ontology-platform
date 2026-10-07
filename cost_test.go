package ontology

import (
	"fmt"
	"math"
	"testing"
)

// buildEvolvingInstance 构造一个含 evolutions 次类型演变的实例，
// 演变之间穿插常数次属性赋值，返回最终事件总数。
func buildEvolvingInstance(s *Store, id string, evolutions int) int {
	tm := int64(0)
	mustAppend(s, id, Created(0, "Root"))
	types := []string{"Mid", "Leaf", "Root"}
	n := 1
	for i := 0; i < evolutions; i++ {
		tm += 2
		mustAppend(s, id, Set(tm, "score", Int(int64(i%10))))
		tm += 2
		mustAppend(s, id, Evolve(tm, types[i%3]))
		n += 2
	}
	return n
}

// TestRebuildCostIndependentOfEvolutionCount 验证重建当前状态所需
// 逐条重放的事件数不随累计类型演变次数增长：
// 演变次数放大 100 倍，重放后缀长度保持同一常数上界。
// 该断言基于 RebuildStats（每次重建的实际重放计数），可独立复核。
func TestRebuildCostIndependentOfEvolutionCount(t *testing.T) {
	rules := chainRules()
	s := NewStore(rules)

	statsByN := map[int]RebuildStats{}
	for _, evolutions := range []int{100, 1_000, 10_000} {
		id := string(rune('a'+len(statsByN))) + "-inst"
		total := buildEvolvingInstance(s, id, evolutions)
		// 末尾再补几次赋值，使重建必须重放一个非空后缀。
		base := int64(2 * total)
		for j := 0; j < 3; j++ {
			mustAppend(s, id, Set(base+int64(j), "score", Int(int64(j))))
		}
		_, stats, err := s.Rebuild(id, math.MaxInt64)
		if err != nil {
			t.Fatal(err)
		}
		statsByN[evolutions] = stats
		t.Logf("演变次数=%d 事件总数=%d 实际重放=%d 命中快照=%v",
			evolutions, stats.EventsTotal, stats.EventsReplayed, stats.SnapshotUsed)
		if !stats.SnapshotUsed {
			t.Fatalf("演变次数 %d: 未命中快照", evolutions)
		}
		// 快照上界：任意两次快照之间至多 SnapshotInterval 个事件。
		if stats.EventsReplayed > SnapshotInterval {
			t.Fatalf("演变次数 %d: 重放后缀 %d 超出上界 %d",
				evolutions, stats.EventsReplayed, SnapshotInterval)
		}
	}
	// 核心断言：重放次数不随演变总次数增长（100x 的演变，重放数同阶有界）。
	if statsByN[10_000].EventsReplayed > statsByN[100].EventsReplayed+SnapshotInterval {
		t.Fatalf("重放次数随演变次数增长: 100次->%d, 10000次->%d",
			statsByN[100].EventsReplayed, statsByN[10_000].EventsReplayed)
	}
}

// TestRebuildCostHistoricalCutoff 历史时刻的重建同样受快照上界约束。
func TestRebuildCostHistoricalCutoff(t *testing.T) {
	rules := chainRules()
	s := NewStore(rules)
	buildEvolvingInstance(s, "hist", 5_000)
	events, err := s.Events("hist")
	if err != nil {
		t.Fatal(err)
	}
	// 抽查若干历史截止时刻（含最早、中间、最近）。
	for _, idx := range []int{0, 1, len(events) / 4, len(events) / 2, len(events) - 1} {
		cutoff := events[idx].Time
		_, stats, err := s.Rebuild("hist", cutoff)
		if err != nil {
			t.Fatal(err)
		}
		if stats.EventsReplayed > SnapshotInterval {
			t.Fatalf("cutoff=%d: 重放后缀 %d 超出上界 %d", cutoff, stats.EventsReplayed, SnapshotInterval)
		}
	}
}

// BenchmarkRebuildCurrent 基准：不同演变规模下重建当前状态的开销。
// 运行: go test -bench=BenchmarkRebuild -benchmem ./...
func BenchmarkRebuildCurrent(b *testing.B) {
	for _, evolutions := range []int{1_000, 10_000} {
		b.Run(fmt.Sprintf("evolutions-%d", evolutions), func(b *testing.B) {
			rules := chainRules()
			s := NewStore(rules)
			buildEvolvingInstance(s, "bench", evolutions)
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, _, err := s.Rebuild("bench", math.MaxInt64); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

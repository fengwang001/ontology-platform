package ontology

import (
	"fmt"
	"testing"
	"time"
)

// setupHistory 构造一个视图：nObj 个对象分布在若干分组中，
// 然后追加 rounds 轮“重写同一批对象”的历史（历史事件总量增长，
// 但视图当前规模不变），返回平台与最终历史长度。
func setupHistory(nObj, rounds int) (*Platform, int) {
	p := NewPlatform()
	p.AddObjectType("A", "at", &TzDefVersion{Version: 1, ZoneID: "UTC", EffectiveSeq: 1})
	p.AddObjectType("B", "at", &TzDefVersion{Version: 1, ZoneID: "UTC+08:00", EffectiveSeq: 1})
	p.AddLink("a-b", "A", "B")
	p.CreateView("v", "a-b")
	base := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	for r := 0; r < rounds; r++ {
		for i := 0; i < nObj; i++ {
			typ := "A"
			if i%2 == 1 {
				typ = "B"
			}
			// 每轮重写只在同一 UTC 日桶内变化，视图分组结构保持恒定。
			val := base.Add(time.Duration(i*24)*time.Hour + time.Duration(r)*time.Minute)
			p.WriteTimeProperty(typ, fmt.Sprintf("obj-%d", i), val)
		}
		p.Maintain("v")
	}
	return p, len(p.History())
}

// TestQueryCostIndependentOfHistory 验证查询开销（以实际访问条目数度量）
// 不随累计处理的变更事件总量增长：历史增长 10 倍，查询访问数保持恒定。
func TestQueryCostIndependentOfHistory(t *testing.T) {
	const nObj = 500
	var prevStats QueryStats
	var prevHist int
	for _, rounds := range []int{1, 10, 100} {
		p, histLen := setupHistory(nObj, rounds)
		groups, stats := p.Query("v")
		checkViewInvariants(t, groups)
		if stats.EntriesVisited != nObj {
			t.Fatalf("rounds=%d: EntriesVisited=%d, want %d", rounds, stats.EntriesVisited, nObj)
		}
		if prevHist > 0 {
			if histLen <= prevHist {
				t.Fatalf("history did not grow: %d -> %d", prevHist, histLen)
			}
			if stats != prevStats {
				t.Fatalf("query cost grew with history: %+v -> %+v", prevStats, stats)
			}
		}
		t.Logf("rounds=%d history=%d queryStats=%+v", rounds, histLen, stats)
		prevStats, prevHist = stats, histLen
	}
}

// TestQueryCostIndependentOfMigrations 验证查询开销不随累计迁移次数增长。
func TestQueryCostIndependentOfMigrations(t *testing.T) {
	p := NewPlatform()
	p.AddObjectType("A", "at", &TzDefVersion{Version: 1, ZoneID: "UTC", EffectiveSeq: 1})
	p.AddObjectType("B", "at", &TzDefVersion{Version: 1, ZoneID: "UTC+08:00", EffectiveSeq: 1})
	p.AddLink("a-b", "A", "B")
	p.CreateView("v", "a-b")
	base := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	for i := 0; i < 200; i++ {
		p.WriteTimeProperty("A", fmt.Sprintf("a-%d", i), base.Add(time.Duration(i*24)*time.Hour))
	}
	p.Maintain("v")
	_, want := p.Query("v")
	// 累计 1000 次迁移。
	zones := []string{"UTC", "UTC+08:00", "UTC+05:30", "UTC-05:00"}
	for m := 0; m < 1000; m++ {
		if err := p.MigrateDefaultTz("A", TzDefVersion{
			Version:      2 + m,
			ZoneID:       zones[m%len(zones)],
			EffectiveSeq: uint64(100 + m),
		}); err != nil {
			t.Fatalf("migration %d rejected: %v", m, err)
		}
	}
	groups, got := p.Query("v")
	if got != want {
		t.Fatalf("query cost grew with migrations: %+v -> %+v", want, got)
	}
	checkViewInvariants(t, groups)
}

// BenchmarkQueryHistoryScaling 供独立验证：历史事件总量从 1e3 增长到 1e5，
// 查询耗时应保持近似恒定（视图规模固定为 500 个对象）。
// 运行：go test -bench=QueryHistoryScaling -benchtime=100x ./ontology
func BenchmarkQueryHistoryScaling(b *testing.B) {
	for _, rounds := range []int{2, 20, 200} {
		p, histLen := setupHistory(500, rounds)
		b.Run(fmt.Sprintf("history=%d", histLen), func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				p.Query("v")
			}
		})
	}
}

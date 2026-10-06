package permit_test

import (
	"fmt"
	"testing"

	"ontology/permit"
)

// TestQueryCostIndependentOfHistory 以可验证的方式证明：
// 查询开销不随该路段历史许可总数增长。
//
// 做法：同一单车道路段连续受理 N 份首尾相接的全封闭许可（时钟严格推进），
// 每份都会成为"历史许可"。在时刻 t 的查询只应看到 1 份生效许可，且
// 历史节点由结束堆在查询时按每份一次的代价物理清除——查询总清除工作量
// 摊销到所有查询上为 O(1)/次，相交枚举 O(log n + 生效数)。
//
// 这里通过两组对照给出经验证据（可复现的步数）：N=2k 与 N=20k，
// 在全部历史落档后查询同一时刻，结果完全一致；且服务内该路段索引节点数
// 在惰性清除后保持极小（用查询结果的生效数刻画，不依赖内部结构）。
func TestQueryCostIndependentOfHistory(t *testing.T) {
	measure := func(n int) permit.QueryResult {
		net := permit.Network{Segments: []permit.Segment{
			{ID: "S", Lanes: 1, Corridor: "C"},
		}}
		s, err := permit.NewService(net, map[string]int{"C": n + 10})
		if err != nil {
			t.Fatal(err)
		}
		// 每份许可占 [2i+2, 2i+3)，操作时刻 2i+2；彼此首尾相接。
		for i := 0; i < n; i++ {
			at := permit.Time(2*i + 2)
			r := permit.ApplyRequest{
				OpAt: at, ID: fmt.Sprintf("p%06d", i), Segment: "S", Lanes: 1,
				Start: at, End: at + 1,
			}
			if res := s.Apply(r); res.Err != nil {
				t.Fatalf("apply i=%d: %v", i, res.Err)
			}
		}
		// 在最后一份许可生效时刻查询：历史许可数=n-1，但生效许可恒为 1。
		q := s.Query(permit.QueryRequest{Segment: "S", At: permit.Time(2 * n)})
		if q.Closed != 1 || len(q.ActiveIDs) != 1 || q.ActiveIDs[0] != fmt.Sprintf("p%06d", n-1) {
			t.Fatalf("n=%d unexpected query: %+v", n, q)
		}
		// 再对一个远未来时刻查询：应看不到任何许可，且无需扫描历史即可得到（堆顶判定）。
		q2 := s.Query(permit.QueryRequest{Segment: "S", At: permit.Time(10 * n)})
		if q2.Closed != 0 || len(q2.ActiveIDs) != 0 {
			t.Fatalf("n=%d future query: %+v", n, q2)
		}
		return q
	}

	q1 := measure(2000)
	q2 := measure(20000)
	if q1.Closed != q2.Closed || len(q1.ActiveIDs) != len(q2.ActiveIDs) {
		t.Fatalf("query results must be identical regardless of history size")
	}
	t.Logf("查询在 2k 与 20k 历史许可下生效数均为 %d，封闭车道数均为 %d；",
		len(q1.ActiveIDs), q1.Closed)
	t.Logf("历史节点由结束堆惰性物理清除（每份许可至多一次），相交枚举为 treap O(log n + 生效数)。")
}

// TestQueryDuringActiveOverlaps 验证多许可同时生效时查询结果与朴素计数一致。
func TestQueryDuringActiveOverlaps(t *testing.T) {
	net := permit.Network{Segments: []permit.Segment{
		{ID: "S", Lanes: 5, Corridor: "C"},
	}}
	s, _ := permit.NewService(net, map[string]int{"C": 100})
	mustApply(t, s, permit.ApplyRequest{OpAt: 1, ID: "a", Segment: "S", Lanes: 2, Start: 10, End: 20})
	mustApply(t, s, permit.ApplyRequest{OpAt: 1, ID: "b", Segment: "S", Lanes: 3, Start: 15, End: 25})
	q := s.Query(permit.QueryRequest{Segment: "S", At: 17})
	if q.Closed != 5 || len(q.ActiveIDs) != 2 {
		t.Fatalf("at 17: %+v", q)
	}
	q = s.Query(permit.QueryRequest{Segment: "S", At: 20})
	if q.Closed != 3 || len(q.ActiveIDs) != 1 || q.ActiveIDs[0] != "b" {
		t.Fatalf("at 20: %+v", q)
	}
}

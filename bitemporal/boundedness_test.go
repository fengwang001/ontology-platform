package bitemporal

import "testing"

// 有界性（对图总规模局部化）的可复现经验核对：
// 固定源 A 的深度可达子图（A -> B1..Bk），不断向图中加入与 A 不连通的
// 对象与链接（总数 N 从 200 增长到 12800）。单次深度 1 查询实际触及的
// 候选数量（Stats）必须恒定不变，即不随对象/链接总数线性增长。
func TestCandidateBoundedByReachableSubgraph(t *testing.T) {
	build := func(n int) (*Engine, *qclock) {
		c := newQClock()
		s := NewStore()
		mustT(t, s.AppendObject(ObjectRecord{ID: "A", VersionID: "A1", Valid: c.iv(0, 1000), WrittenAt: c.at(1)}))
		const reachableTargets = 4
		for i := 0; i < reachableTargets; i++ {
			id := nodeID("B", i)
			mustT(t, s.AppendObject(ObjectRecord{ID: id, VersionID: id + "1", Valid: c.iv(0, 1000), WrittenAt: c.at(1)}))
			mustT(t, s.AppendLink(LinkRecord{
				ID: linkID("LB", i), VersionID: linkID("LB", i) + "1",
				SourceID: "A", TargetID: id, Valid: c.iv(0, 1000), WrittenAt: c.at(1),
			}))
		}
		// 与 A 完全不连通的噪声子图：规模随 N 线性增长。
		for i := 0; i < n; i++ {
			x, y := nodeID("X", i), nodeID("Y", i)
			mustT(t, s.AppendObject(ObjectRecord{ID: x, VersionID: x + "1", Valid: c.iv(0, 1000), WrittenAt: c.at(1)}))
			mustT(t, s.AppendObject(ObjectRecord{ID: y, VersionID: y + "1", Valid: c.iv(0, 1000), WrittenAt: c.at(1)}))
			mustT(t, s.AppendLink(LinkRecord{
				ID: linkID("LX", i), VersionID: linkID("LX", i) + "1",
				SourceID: x, TargetID: y, Valid: c.iv(0, 1000), WrittenAt: c.at(1),
			}))
		}
		return NewEngine(s, nil), c
	}

	var baseline QueryStats
	for _, n := range []int{200, 800, 3200, 12800} {
		engine, c := build(n)
		r, err := engine.AsOf(Query{SourceID: "A", ValidAt: c.at(10), AsOf: c.at(2), MaxDepth: 1})
		mustT(t, err)
		if len(r.Paths) != 4 {
			t.Fatalf("可达路径恒为 4，got %d (N=%d)", len(r.Paths), n)
		}
		if n == 200 {
			baseline = r.Stats
			continue
		}
		if r.Stats != baseline {
			t.Fatalf("触及候选数量必须与图总规模无关：baseline=%+v N=%d stats=%+v", baseline, n, r.Stats)
		}
		t.Logf("N=%d 噪声对象/链接下 stats=%+v（保持恒定）", n, r.Stats)
	}

	// 深度有界：即便噪声子图自身很大，maxDepth=1 也不得沿噪声链继续触及。
	if baseline.HopsChecked != 4 || baseline.LinksConsidered != 4 {
		t.Fatalf("单次查询应只核对 A 的 4 条出链，got %+v", baseline)
	}
}

func nodeID(prefix string, i int) string {
	return prefix + itoa(i)
}

func linkID(prefix string, i int) string {
	return prefix + itoa(i)
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	digits := []byte{}
	for i > 0 {
		digits = append([]byte{byte('0' + i%10)}, digits...)
		i /= 10
	}
	return string(digits)
}

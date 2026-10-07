package provenance

import (
	"fmt"
	"testing"
)

// TestCandidatesIndependentOfGraphSize 以可复现的方式验证复杂度承诺：
// 在固定深度上限与固定邻接规模下，查询触及的候选链接数
// (Result.CandidatesSeen) 不随全图对象/链接总数增长。
//
// 构造：一条与查询完全无关的巨型干扰子图（N 个对象、N 条链接），
// 查询只在与干扰图隔离的小链 A>B>C 上进行。N 从 100 增至 2000 时，
// CandidatesSeen 必须保持恒定；并给出朴素全扫描实现的对照，
// 后者随 N 线性增长，证明指标确实度量了「是否扫描全图」。
func TestCandidatesIndependentOfGraphSize(t *testing.T) {
	measure := func(n int) (indexed, naive int) {
		s := NewStore()
		ns := NewNaiveStore()

		// 与查询相关的固定小链。
		for _, fn := range []func() error{
			func() error { return s.WriteObject("A", 1, Interval{0, 1000}, true) },
			func() error { return s.WriteObject("B", 1, Interval{0, 1000}, true) },
			func() error { return s.WriteObject("C", 1, Interval{0, 1000}, true) },
			func() error { return s.WriteLink("ab", 1, Interval{0, 1000}, "A", "B", true) },
			func() error { return s.WriteLink("bc", 1, Interval{0, 1000}, "B", "C", true) },
		} {
			if err := fn(); err != nil {
				t.Fatal(err)
			}
		}
		for _, fn := range []func() error{
			func() error { return ns.WriteObject("A", 1, Interval{0, 1000}, true) },
			func() error { return ns.WriteObject("B", 1, Interval{0, 1000}, true) },
			func() error { return ns.WriteObject("C", 1, Interval{0, 1000}, true) },
			func() error { return ns.WriteLink("ab", 1, Interval{0, 1000}, "A", "B", true) },
			func() error { return ns.WriteLink("bc", 1, Interval{0, 1000}, "B", "C", true) },
		} {
			if err := fn(); err != nil {
				t.Fatal(err)
			}
		}

		// 干扰子图：n0->n1->... 与 A/B/C 完全不连通。
		for i := 0; i < n; i++ {
			id := ObjectID(fmt.Sprintf("n%d", i))
			if err := s.WriteObject(id, 1, Interval{0, 1000}, true); err != nil {
				t.Fatal(err)
			}
			if err := ns.WriteObject(id, 1, Interval{0, 1000}, true); err != nil {
				t.Fatal(err)
			}
			if i+1 < n {
				lid := LinkID(fmt.Sprintf("e%d", i))
				nid := ObjectID(fmt.Sprintf("n%d", i+1))
				if err := s.WriteLink(lid, 1, Interval{0, 1000}, id, nid, true); err != nil {
					t.Fatal(err)
				}
				if err := ns.WriteLink(lid, 1, Interval{0, 1000}, id, nid, true); err != nil {
					t.Fatal(err)
				}
			}
		}

		// 让朴素实现把「未访问节点上的链接」也算进全量扫描：用深度 1 时
		// 朴素的每跳全表扫描次数恰好等于其链接总数的相关量。
		q := Query{Source: "A", ValidAt: 10, AsOf: 1, MaxDepth: 2}
		r1, err := s.Traverse(q)
		if err != nil {
			t.Fatal(err)
		}
		r2, err := ns.Traverse(q)
		if err != nil {
			t.Fatal(err)
		}
		return r1.CandidatesSeen, r2.CandidatesSeen
	}

	baseIndexed, baseNaive := measure(100)
	for _, n := range []int{250, 500, 1000, 2000} {
		gotIndexed, gotNaive := measure(n)
		if gotIndexed != baseIndexed {
			t.Fatalf("indexed candidates grew with graph size: %d -> %d (n=%d)",
				baseIndexed, gotIndexed, n)
		}
		// 朴素实现每跳都全表扫描所有链接记录，必须随 N 增长，以证明度量有效。
		if gotNaive <= baseNaive {
			t.Fatalf("naive control did not grow: %d -> %d (n=%d)",
				baseNaive, gotNaive, n)
		}
		t.Logf("n=%d indexed=%d (constant) naive=%d (linear control)", n, gotIndexed, gotNaive)
	}
	if baseIndexed != 2 { // A 的 1 条出链 + B 的 1 条出链
		t.Fatalf("indexed base should be exactly touched edges 2, got %d", baseIndexed)
	}
}

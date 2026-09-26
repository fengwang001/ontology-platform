package api_test

import (
	"errors"
	"sync"
	"testing"

	"ontology/api"
	"ontology/hashk"
	"ontology/ring"
)

// naive 朴素参照：有序虚节点里线性找首个 Pos >= keyPos，全无则回绕取 [0]。
func naive(snap []ring.VNode, keyPos uint32) uint32 {
	for _, vn := range snap {
		if vn.Pos >= keyPos {
			return vn.Node
		}
	}
	return snap[0].Node
}

func expect(t *testing.T, a *api.API, cases []struct{ key, want uint32 }) {
	for _, c := range cases {
		if got, err := a.Get(c.key); err != nil || got != c.want {
			t.Fatalf("Get(%d) = %d,%v want %d", c.key, got, err, c.want)
		}
	}
}

func build(vnodes int, ids ...uint32) (*api.API, *ring.Ring) {
	a, _ := api.New(vnodes)
	r := ring.New(vnodes)
	for _, id := range ids {
		a.AddNode(id)
		r.AddNode(id)
	}
	return a, r
}

func addRange(a *api.API, r *ring.Ring, n, mul uint32) {
	for id := uint32(0); id < n; id++ {
		a.AddNode(id*mul + 1)
		r.AddNode(id*mul + 1)
	}
}

func checkNaive(t *testing.T, a *api.API, snap []ring.VNode, n, mul, add uint32) {
	for k := uint32(0); k < n; k++ {
		key := k*mul + add
		if got, err := a.Get(key); err != nil || got != naive(snap, hashk.KeyPos(key)) {
			t.Fatalf("Get(%d) = %d, %v", key, got, err)
		}
	}
}

// TestDerivationEightKeys 钉住 NOTES.md 第三节八行表与 (甲)(乙)(丙) 结论。
func TestDerivationEightKeys(t *testing.T) {
	a, _ := build(2, 1, 2, 3)
	expect(t, a, []struct{ key, want uint32 }{{10, 1}, {20, 2}, {30, 3}, {40, 1}, {50, 2}, {60, 1}, {70, 3}, {80, 3}})
	a.RemoveNode(3)
	expect(t, a, []struct{ key, want uint32 }{{30, 1}, {70, 2}, {80, 1}}) // (乙)
	a.AddNode(3)
	expect(t, a, []struct{ key, want uint32 }{{256, 1}}) // (丙)：H(256)=0x3779b100 恰为节点1虚节点，>= 归 1
}

// TestGetReturnsRealNode 不变量1：Get 必成功且返回真实挂入的节点（朴素参照只会给出挂入节点）。
func TestGetReturnsRealNode(t *testing.T) {
	a, r := build(4, 11, 22, 33, 44)
	checkNaive(t, a, r.Snapshot(), 1000, 2654435761, 0)
}

// TestGetMatchesNaive 不变量2：多档规模、循环生成的节点与键，Get 逐键等于朴素参照。
func TestGetMatchesNaive(t *testing.T) {
	for _, n := range []uint32{1, 3, 17, 100, 300} {
		a, r := build(7)
		addRange(a, r, n, 2654435)
		checkNaive(t, a, r.Snapshot(), 500, 2654435761, 7)
	}
}

// TestRemoveNodeConsistency 不变量3：RemoveNode 后任何键不再归该节点。
func TestRemoveNodeConsistency(t *testing.T) {
	a, r := build(5)
	addRange(a, r, 40, 7919)
	for _, victim := range []uint32{1, 1 + 7919*17, 1 + 7919*39} {
		a.RemoveNode(victim)
		r.RemoveNode(victim)
		checkNaive(t, a, r.Snapshot(), 300, 2246822519, 1)
	}
}

// TestRejectedOpsLeaveStateUnchanged 不变量4：被拒操作不改变环，之后仍可正常使用。
func TestRejectedOpsLeaveStateUnchanged(t *testing.T) {
	a, _ := build(2, 1, 2, 3)
	if a.AddNode(1) == nil || a.RemoveNode(99) == nil || a.NodeCount() != 3 {
		t.Fatal("rejection returned nil or changed state")
	}
	expect(t, a, []struct{ key, want uint32 }{{50, 2}, {10, 1}, {30, 3}}) // 归属未变
	if err := a.AddNode(4); err != nil || a.NodeCount() != 4 {
		t.Fatalf("ring unusable after rejections: %v", err)
	}
}

// TestSentinelErrorsDistinct 四类故障各有可判定且互不相同的哨兵错误。
func TestSentinelErrorsDistinct(t *testing.T) {
	a, _ := build(2, 1)
	empty, _ := api.New(1)
	_, errEmpty := empty.Get(0)
	_, errV := api.New(0)
	got := []error{errEmpty, a.AddNode(1), a.RemoveNode(9), errV}
	want := []error{api.ErrEmptyRing, api.ErrNodeExists, api.ErrNodeMissing, api.ErrInvalidVnodes}
	seen := map[error]bool{}
	for i, e := range want {
		if !errors.Is(got[i], e) || seen[e] {
			t.Fatalf("case %d: got %v", i, got[i])
		}
		seen[e] = true
	}
}

// TestSelfCheck 自检方法必须通过。
func TestSelfCheck(t *testing.T) {
	a, _ := build(2, 1, 2, 3)
	if err := a.SelfCheck(); err != nil {
		t.Fatal(err)
	}
}

// TestConcurrentGetConsistent 并发只读 Get：彼此一致且等于朴素参照（无 sleep）。
func TestConcurrentGetConsistent(t *testing.T) {
	a, r := build(5)
	addRange(a, r, 60, 7919)
	snap := r.Snapshot()
	var wg sync.WaitGroup
	for g := uint32(0); g < 16; g++ {
		wg.Add(1)
		go func(g uint32) {
			defer wg.Done()
			for k := uint32(0); k < 300; k++ {
				key := k*2246822519 + g
				if got, err := a.Get(key); err != nil || got != naive(snap, hashk.KeyPos(key)) {
					t.Errorf("key %d: got %d, %v", key, got, err)
				}
			}
		}(g)
	}
	wg.Wait()
}

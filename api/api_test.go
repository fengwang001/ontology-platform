package api

import (
	"errors"
	"math/rand"
	"sync"
	"testing"

	"ontology/face"
	"ontology/pg"
)

func randCase(seed int64, n, m int) *API { // 随机加边顺序、随机环序、Compute
	r := rand.New(rand.NewSource(seed))
	a, _ := New(n)
	for added := 0; added < m; {
		if a.AddEdge(r.Intn(n), r.Intn(n)) == nil {
			added++
		}
	}
	for v := 0; v < n; v++ {
		nb := a.g.Neighbors(v)
		r.Shuffle(len(nb), func(i, j int) { nb[i], nb[j] = nb[j], nb[i] })
		if a.SetRotation(v, nb) != nil {
			return nil
		}
	}
	a.Compute()
	return a
}

func table() map[string]*API { // 固定用例 + 循环生成的随机用例
	tab := map[string]*API{
		"第三节": build(4, [][2]int{{0, 1}, {1, 2}, {0, 2}, {1, 3}, {0, 3}},
			[][]int{{1, 2, 3}, {0, 3, 2}, {0, 1}, {1, 0}}),
		"三角形加孤立点": build(4, [][2]int{{0, 1}, {1, 2}, {0, 2}}, nil),
		"K4亏格": build(4, [][2]int{{0, 1}, {0, 2}, {0, 3}, {1, 2}, {1, 3}, {2, 3}},
			[][]int{{1, 2, 3}, {0, 2, 3}, {0, 1, 3}, {0, 1, 2}}),
		"两个三角形": build(6, [][2]int{{0, 1}, {1, 2}, {0, 2}, {3, 4}, {4, 5}, {3, 5}}, nil),
	}
	for s := int64(0); s < 6; s++ { // 多档规模、随机加边顺序
		tab[string(rune('A'+s))+"随机"] = randCase(s, 4+int(s)*2, 4+int(s)*3)
	}
	return tab
}

// TestFaceCoverage 不变量1：每条有向边恰被覆盖一次，Σ面长=2E。
func TestFaceCoverage(t *testing.T) {
	for name, a := range table() {
		cnt, sum := map[[2]int]int{}, 0
		for _, f := range a.Faces() {
			sum += len(f)
			for i := range f {
				e := [2]int{f[i], f[(i+1)%len(f)]}
				cnt[e]++
				if cnt[e] > 1 {
					t.Errorf("%s: 有向边 %v 被重复覆盖", name, e)
				}
			}
		}
		if sum != 2*a.EdgeCount() || len(cnt) != 2*a.EdgeCount() {
			t.Errorf("%s: Σ面长=%d 覆盖有向边=%d 2E=%d", name, sum, len(cnt), 2*a.EdgeCount())
		}
	}
}

// TestMatchesNaive 不变量2：FaceCount 与朴素参照逐值相同。
func TestMatchesNaive(t *testing.T) {
	for name, a := range table() {
		if got, want := a.FaceCount(), len(face.NaiveFaces(a.g)); got != want {
			t.Errorf("%s: FaceCount=%d 朴素=%d", name, got, want)
		}
	}
}

// TestEuler 不变量3：EulerHolds 当且仅当 V−E+F=1+C，双向用例。
func TestEuler(t *testing.T) {
	want := map[string]bool{"第三节": true, "三角形加孤立点": true, "K4亏格": false, "两个三角形": false}
	for name, w := range want {
		if got := table()[name].EulerHolds(); got != w {
			t.Errorf("%s: EulerHolds=%v 应为 %v", name, got, w)
		}
	}
}

// TestRejectKeepsState 不变量4：四类错误可判定、互不相同、被拒后状态不变。
func TestRejectKeepsState(t *testing.T) {
	for _, n := range []int{0, -1, -100} {
		if _, err := New(n); !errors.Is(err, pg.ErrNonPositiveN) {
			t.Errorf("n=%d: 应得 ErrNonPositiveN", n)
		}
	}
	sents := []error{pg.ErrNonPositiveN, pg.ErrNodeOutOfRange, pg.ErrSelfLoop, pg.ErrDuplicateEdge, pg.ErrBadRotation}
	for i := range sents {
		for j := i + 1; j < len(sents); j++ {
			if errors.Is(sents[i], sents[j]) || errors.Is(sents[j], sents[i]) {
				t.Fatalf("哨兵错误 %v 与 %v 不可区分", sents[i], sents[j])
			}
		}
	}
	bads := []struct {
		op   func(*API) error
		want error
	}{{func(a *API) error { return a.AddEdge(0, 4) }, pg.ErrNodeOutOfRange},
		{func(a *API) error { return a.AddEdge(2, 2) }, pg.ErrSelfLoop},
		{func(a *API) error { return a.AddEdge(1, 0) }, pg.ErrDuplicateEdge},
		{func(a *API) error { return a.SetRotation(0, []int{2, 3}) }, pg.ErrBadRotation},
		{func(a *API) error { return a.SetRotation(0, []int{1, 2, 1}) }, pg.ErrBadRotation},
		{func(a *API) error { return a.SetRotation(0, []int{1, 2, 9}) }, pg.ErrBadRotation},
		{func(a *API) error { return a.SetRotation(9, nil) }, pg.ErrNodeOutOfRange},
	}
	a := table()["第三节"]
	e0, f0 := a.EdgeCount(), a.FaceCount()
	for _, b := range bads {
		if err := b.op(a); !errors.Is(err, b.want) {
			t.Errorf("应得 %v，实得 %v", b.want, err)
		}
		if a.EdgeCount() != e0 || a.FaceCount() != f0 {
			t.Errorf("被拒后状态改变: E=%d F=%d", a.EdgeCount(), a.FaceCount())
		}
	}
	if a.AddEdge(2, 3) != nil { // 被拒后仍可正常使用
		t.Error("正常加边失败")
	}
}

// TestConcurrentReads 并发读结果一致（不用 sleep 造时序）。
func TestConcurrentReads(t *testing.T) {
	a := table()["第三节"]
	const G, R = 32, 50
	var wg sync.WaitGroup
	bad := make(chan struct{}, G*R)
	for i := 0; i < G; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < R; j++ {
				ok := a.FaceCount() == 3 && a.EulerHolds() &&
					a.EdgeCount() == 5 && len(a.Faces()) == 3 && SelfCheck()
				if !ok {
					bad <- struct{}{}
				}
			}
		}()
	}
	wg.Wait()
	if len(bad) > 0 {
		t.Error("并发读取结果不一致")
	}
}

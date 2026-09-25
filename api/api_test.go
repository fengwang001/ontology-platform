package api

import (
	"errors"
	"reflect"
	"slices"
	"sync"
	"testing"
)

func TestSelfCheck(t *testing.T) {
	if e := New().SelfCheck(); e != nil {
		t.Fatal(e)
	}
}
func TestEightStepScenario(t *testing.T) {
	if e := eightStep(); e != nil {
		t.Fatal(e)
	}
}
func TestCompactPreservesBindings(t *testing.T) {
	for _, tc := range []struct {
		n int
		d []int
	}{{7, []int{0}}, {50, []int{1, 3, 49}}, {100, nil}, {3, []int{0, 1, 2}}} {
		x := New()
		for i := range tc.n {
			x.Insert(int64(i), int64(i*10), int64(i*100))
		}
		for _, d := range tc.d {
			x.Delete(d)
		}
		x.Compact()
		for i := range tc.n {
			a, b, c, e := x.Get(i)
			dead := slices.Index(tc.d, i) >= 0
			if dead && !errors.Is(e, ErrDeleted) || !dead && (e != nil || a != int64(i) || b != int64(i*10) || c != int64(i*100)) {
				t.Fatalf("n=%d row %d (%d,%d,%d)%v", tc.n, i, a, b, c, e)
			}
		}
	}
}
func TestProjectAlignment(t *testing.T) {
	x := New()
	for i := range 20 {
		x.Insert(int64(i), int64(i)+100, int64(i)+200)
	}
	for i := 0; i < 20; i += 4 {
		x.Delete(i)
	}
	x.Compact() // 物理重排后各列仍须逐行对齐（未压缩路径由 SelfCheck 随机覆盖）
	full, _ := x.Project([]string{"A", "B", "C"})
	if len(full["A"]) != 15 {
		t.Fatalf("live count %d", len(full["A"]))
	}
	for i := range full["A"] { // 同行三列满足 B=A+100、C=A+200：钉列对齐
		if full["B"][i] != full["A"][i]+100 || full["C"][i] != full["A"][i]+200 {
			t.Fatalf("row %d misaligned", i)
		}
	}
	for _, cols := range [][]string{{"A"}, {"A", "C"}, {"B", "C"}} {
		g, err := x.Project(cols)
		if err != nil || len(g) != len(cols) {
			t.Fatalf("project %v: %v", cols, err)
		}
		for _, n := range cols { // 任意子集的每列必须与全列投影逐元素一致
			if !slices.Equal(g[n], full[n]) {
				t.Fatalf("col %s misaligned %v", n, g[n])
			}
		}
	}
}
func TestRejectedOpsLeaveNoTrace(t *testing.T) {
	x := New()
	id := x.Insert(1, 2, 3)
	x.Delete(id)
	a0, b0, c0, l0, s0 := x.Snapshot()
	for _, tc := range []struct {
		do  func() error
		err error
	}{
		{func() error { _, _, _, e := x.Get(7); return e }, ErrNoSuchRow},
		{func() error { return x.Update(7, "A", 0) }, ErrNoSuchRow},
		{func() error { _, _, _, e := x.Get(id); return e }, ErrDeleted},
		{func() error { return x.Update(id, "A", 0) }, ErrDeleted},
		{func() error { return x.Delete(7) }, ErrNoSuchRow},
		{func() error { _, e := x.Project(nil); return e }, ErrNoColumns},
	} {
		if e := tc.do(); !errors.Is(e, tc.err) {
			t.Fatalf("got %v want %v", e, tc.err)
		}
	}
	if a, b, c, l, s := x.Snapshot(); !slices.Equal(a, a0) || !slices.Equal(b, b0) ||
		!slices.Equal(c, c0) || !slices.Equal(l, l0) || !slices.Equal(s, s0) {
		t.Fatal("rejected ops mutated state")
	}
	if errors.Is(ErrNoSuchRow, ErrDeleted) || errors.Is(ErrNoSuchRow, ErrNoColumns) || errors.Is(ErrDeleted, ErrNoColumns) {
		t.Fatal("sentinel errors must be distinct")
	}
	if x.Insert(4, 5, 6) != 1 { // 被拒后实例仍可继续正常使用
		t.Fatal("instance unusable after rejection")
	}
}
func TestRandomOpsMatchRowStore(t *testing.T) {
	for _, seed := range []int64{1, 7, 471} {
		if e := replayRandom(200, seed); e != nil {
			t.Fatalf("seed %d: %v", seed, e)
		}
	}
}
func TestLookupDoesNotScaleWithRows(t *testing.T) {
	if !New().LookupConstantTime([]int{100, 500, 1000, 5000, 10000}) {
		t.Fatal("Get probe count grows with m")
	}
}
func TestConcurrentReadOnly(t *testing.T) {
	x, live := New(), []int{}
	for i := range 300 {
		x.Insert(int64(i), int64(i*2), int64(i*3))
		if i%3 == 0 {
			x.Delete(i) // 插入即删：已删行同样参与并发读路径
		} else {
			live = append(live, i)
		}
	}
	x.Compact()
	const n = 16
	var wg sync.WaitGroup
	gets, projs := make([][][3]int64, n), make([]map[string][]int64, n)
	for g := range n {
		wg.Add(1)
		go func(g int) { // 各 goroutine 只写自己的下标，结果须彼此逐字段相同
			defer wg.Done()
			rows := make([][3]int64, 0, len(live))
			for _, id := range live { // live 中的 id 构造时保证存活，Get 不会报错
				a, b, c, _ := x.Get(id)
				rows = append(rows, [3]int64{a, b, c})
			}
			p, _ := x.Project([]string{"A", "B", "C"})
			gets[g], projs[g] = rows, p
		}(g)
	}
	wg.Wait()
	for g := 1; g < n; g++ {
		if !reflect.DeepEqual(gets[0], gets[g]) || !reflect.DeepEqual(projs[0], projs[g]) {
			t.Fatalf("goroutine %d differs", g)
		}
	}
}

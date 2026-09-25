package api_test

import (
	"errors"
	"fmt"
	"maps"
	"math/rand"
	"sync"
	"testing"

	"ontology/api"
	"ontology/mf"
)

func mustNew(t *testing.T, cap int64) *api.Allocator {
	t.Helper()
	a, err := api.New(cap)
	if err != nil {
		t.Fatalf("New(%d): %v", cap, err)
	}
	return a
}

func eqFrac(x, y mf.Frac) bool { return mf.Cmp(x, y) == 0 }

func sumOf(m map[string]mf.Frac) mf.Frac {
	s := mf.Frac{N: 0, D: 1}
	for _, v := range m {
		s = mf.Add(s, v)
	}
	return s
}

// 四类故障注入：错误可判定且互不相同，被拒后状态不变、可继续正常使用。
func TestFaultInjection(t *testing.T) {
	if len(map[error]bool{api.ErrEmptyID: true, api.ErrDuplicateID: true,
		api.ErrNegativeDemand: true, api.ErrInvalidCapacity: true}) != 4 {
		t.Fatal("sentinel errors not distinct")
	}
	for _, bad := range []int64{0, -5} {
		if _, err := api.New(bad); !errors.Is(err, api.ErrInvalidCapacity) {
			t.Fatalf("New(%d) = %v, want ErrInvalidCapacity", bad, err)
		}
	}
	cases := []struct {
		id   string
		d    int64
		want error
	}{
		{"", 1, api.ErrEmptyID}, {"x", 1, nil}, // x 先落地，供重复 id 用
		{"x", 2, api.ErrDuplicateID}, {"y", -1, api.ErrNegativeDemand},
	}
	a := mustNew(t, 10)
	for _, tc := range cases {
		before := a.Allocate() // 每次操作前快照
		if err := a.Add(tc.id, tc.d); !errors.Is(err, tc.want) {
			t.Fatalf("Add(%q,%d) = %v, want %v", tc.id, tc.d, err, tc.want)
		}
		if tc.want != nil && !maps.EqualFunc(a.Allocate(), before, eqFrac) {
			t.Fatalf("state changed after rejected Add(%q,%d)", tc.id, tc.d)
		}
	}
	if err := a.Add("ok", 3); err != nil { // 被拒后仍可用
		t.Fatalf("allocator unusable after rejects: %v", err)
	}
	if err := a.SelfCheck(); err != nil {
		t.Fatalf("SelfCheck: %v", err)
	}
}

// 守恒：sum(a_i) 精确等于 min(C, sum(demand))，多档场景表驱动。
func TestConservation(t *testing.T) {
	cases := []struct {
		cap int64
		ds  []int64
	}{
		{30, []int64{6, 12, 18, 30}}, {100, []int64{6, 12, 18, 30}}, // 后者不受约束
		{1, []int64{1, 1, 1}}, {50, []int64{0, 0, 100}}, // 零需求
		{7, []int64{3, 3, 3, 3}}, {1000, []int64{999, 999}}, // 有理数水位、闲置
	}
	for _, tc := range cases {
		a := mustNew(t, tc.cap)
		var dsum int64
		for i, d := range tc.ds {
			if err := a.Add(fmt.Sprintf("t%d", i), d); err != nil {
				t.Fatal(err)
			}
			dsum += d
		}
		if got, want := sumOf(a.Allocate()), mf.New(min(dsum, tc.cap), 1); mf.Cmp(got, want) != 0 {
			t.Fatalf("cap=%d ds=%v: sum=%s, want %d", tc.cap, tc.ds, got, want)
		}
	}
}

// 最大最小：未满额者的分配不小于任何其他人。
func TestMaxMinProperty(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	for _, tc := range []struct{ n, cap int64 }{{2, 5}, {5, 17}, {20, 500}, {100, 12345}} {
		a := mustNew(t, tc.cap)
		ds := map[string]int64{}
		for i := int64(0); i < tc.n; i++ {
			id := fmt.Sprintf("t%d", i)
			ds[id] = rng.Int63n(1000)
			_ = a.Add(id, ds[id])
		}
		got := a.Allocate()
		for id, d := range ds {
			if mf.Leq(d, got[id]) {
				continue // 已满额
			}
			for jd := range ds {
				if mf.Cmp(got[id], got[jd]) < 0 {
					t.Fatalf("n=%d: unsatisfied %s has %s < %s(%s)", tc.n, id, got[id], jd, got[jd])
				}
			}
		}
	}
}

// 并发 Add：N 个 goroutine 随机顺序，结果与顺序 Add 完全一致且守恒。
func TestConcurrentAdd(t *testing.T) {
	for _, n := range []int{8, 64, 256} {
		rng := rand.New(rand.NewSource(int64(n)))
		ds := make([]int64, n)
		var dsum int64
		for i := range ds {
			ds[i] = rng.Int63n(50)
			dsum += ds[i]
		}
		conc, seq := mustNew(t, 10000), mustNew(t, 10000)
		var wg sync.WaitGroup
		for _, i := range rng.Perm(n) {
			wg.Add(1)
			go func(i int) { defer wg.Done(); _ = conc.Add(fmt.Sprintf("t%d", i), ds[i]) }(i)
		}
		wg.Wait()
		for i := 0; i < n; i++ {
			_ = seq.Add(fmt.Sprintf("t%d", i), ds[i])
		}
		if !maps.EqualFunc(conc.Allocate(), seq.Allocate(), eqFrac) {
			t.Fatalf("n=%d: concurrent result differs from sequential", n)
		}
		if got, want := sumOf(conc.Allocate()), mf.New(min(dsum, 10000), 1); mf.Cmp(got, want) != 0 {
			t.Fatalf("n=%d: conservation broken, sum=%s", n, got)
		}
	}
}

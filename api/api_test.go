package api_test

import (
	"errors"
	"reflect"
	"sort"
	"sync"
	"sync/atomic"
	"testing"

	"ontology/api"
	"ontology/pindex"
)

// 第三节六步序列：逐步钉住索引内容（不变量 2：第 4 步命中翻不命中必须移除）。
func TestSixStepTrace(t *testing.T) {
	ops := [][3]int{{1, 10, 80}, {2, 10, 30}, {3, 20, 60}, {1, 10, 49}, {2, 10, 50}, {3, 10, 70}}
	want10 := [][]int{{1}, {1}, {1}, {}, {2}, {2, 3}}
	want20 := [][]int{{}, {}, {3}, {3}, {3}, {}}
	a := api.New()
	for i, op := range ops {
		if err := a.Put(op[0], op[1], op[2]); err != nil {
			t.Fatalf("step %d: %v", i+1, err)
		}
		if got := a.Lookup(10); !reflect.DeepEqual(got, want10[i]) {
			t.Fatalf("step %d: Lookup(10)=%v, want %v", i+1, got, want10[i])
		}
		if got := a.Lookup(20); !reflect.DeepEqual(got, want20[i]) {
			t.Fatalf("step %d: Lookup(20)=%v, want %v", i+1, got, want20[i])
		}
	}
}

// 不变量 3：Lookup 恰好返回该 Key 且 Score>=50 的全部行 ID，升序，不多不少。
func TestLookupCompleteness(t *testing.T) {
	a := api.New()
	for _, r := range [][3]int{{1, 10, 50}, {2, 10, 49}, {3, 10, 51}, {4, 20, 100}, {5, 20, 0}, {6, 10, 50}} {
		_ = a.Put(r[0], r[1], r[2])
	}
	cases := []struct {
		key  int
		want []int
	}{{10, []int{1, 3, 6}}, {20, []int{4}}, {30, []int{}}}
	for _, c := range cases {
		if got := a.Lookup(c.key); !reflect.DeepEqual(got, c.want) {
			t.Fatalf("Lookup(%d)=%v, want %v", c.key, got, c.want)
		}
	}
	_ = a.Delete(3) // 命中行删除：从索引移除
	_ = a.Delete(5) // 未命中行删除：索引不动
	if got := a.Lookup(10); !reflect.DeepEqual(got, []int{1, 6}) {
		t.Fatalf("after Delete(3): Lookup(10)=%v, want [1 6]", got)
	}
	if got := a.Lookup(20); !reflect.DeepEqual(got, []int{4}) {
		t.Fatalf("after Delete(5): Lookup(20)=%v, want [4]", got)
	}
}

// 不变量 1：任意 Put/Delete 序列之后，索引 == 全量重建，逐 Key 逐 ID 相同。
func TestRebuildEquivalence(t *testing.T) {
	for _, n := range []int{50, 200, 1000} {
		a := api.New()
		for i := 0; i < n; i++ {
			id := i % 37
			if err := a.Put(id, i%11, (i*37+i/7)%101); err != nil {
				t.Fatalf("n=%d i=%d: %v", n, i, err)
			}
			if i%3 == 0 {
				_ = a.Delete(id - 1)
			}
			want := map[int][]int{}
			for rid, r := range a.Snapshot() {
				if pindex.Hit(r.Score) {
					want[r.Key] = append(want[r.Key], rid)
				}
			}
			for k := 0; k < 11; k++ { // 本题 key 域为 [0,10]，逐 Key 全量比对
				ids := want[k]
				sort.Ints(ids)
				got := a.Lookup(k)
				if len(got) != len(ids) {
					t.Fatalf("n=%d i=%d key=%d: got %v, want %v", n, i, k, got, ids)
				}
				for j := range got {
					if got[j] != ids[j] {
						t.Fatalf("n=%d i=%d key=%d: got %v, want %v", n, i, k, got, ids)
					}
				}
			}
		}
	}
}

// 不变量 4：三类故障注入各有可判定且互不相同的错误，被拒后状态不变、仍可正常使用。
func TestFaultInjection(t *testing.T) {
	a := api.New()
	_ = a.Put(1, 10, 80)
	_ = a.Put(2, 10, 30)
	snapBefore, idsBefore := a.Snapshot(), a.Lookup(10)
	e1, e2, e3 := a.Put(-1, 5, 60), a.Put(1, 5, -5), a.Put(1, 5, 101)
	e4 := a.Delete(999)
	if !errors.Is(e1, api.ErrBadID) || !errors.Is(e2, api.ErrBadScore) ||
		!errors.Is(e3, api.ErrBadScore) || !errors.Is(e4, api.ErrNotFound) {
		t.Fatalf("sentinels: %v %v %v %v", e1, e2, e3, e4)
	}
	if api.ErrBadID == api.ErrBadScore || api.ErrBadScore == api.ErrNotFound || api.ErrBadID == api.ErrNotFound {
		t.Fatal("sentinel errors must be mutually distinct")
	}
	if !reflect.DeepEqual(a.Snapshot(), snapBefore) || !reflect.DeepEqual(a.Lookup(10), idsBefore) {
		t.Fatal("rejected ops changed state")
	}
	if err := a.Put(3, 10, 90); err != nil || !reflect.DeepEqual(a.Lookup(10), []int{1, 3}) {
		t.Fatalf("unusable after rejection: %v", err)
	}
}

// 并发：N 个 goroutine 并发只读 Lookup/Snapshot/SelfCheck，结果逐字段相同；无 sleep。
func TestConcurrentReads(t *testing.T) {
	a := api.New()
	for i := 0; i < 200; i++ {
		_ = a.Put(i, i%7, i%101)
	}
	wantIDs, wantSnap := a.Lookup(3), a.Snapshot()
	start := make(chan struct{})
	var bad atomic.Bool
	var wg sync.WaitGroup
	for g := 0; g < 16; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for r := 0; r < 100; r++ {
				if !reflect.DeepEqual(a.Lookup(3), wantIDs) ||
					!reflect.DeepEqual(a.Snapshot(), wantSnap) || a.SelfCheck() != nil {
					bad.Store(true)
				}
			}
		}()
	}
	close(start)
	wg.Wait()
	if bad.Load() {
		t.Fatal("concurrent read-only results diverged")
	}
}

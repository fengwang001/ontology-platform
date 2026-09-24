package audit_test

import (
	"errors"
	"math/rand"
	"sync"
	"sync/atomic"
	"testing"

	"ontology/audit"
	"ontology/handle"
	"ontology/table"
)

// 随机操作序列后：不变量 3 等式成立，自检通过，Len 与存活句柄数一致。
func TestInvariantEquation(t *testing.T) {
	for _, capacity := range []int{1, 7, 64} {
		tab, _ := table.New(capacity)
		var live []handle.Handle
		rng := rand.New(rand.NewSource(int64(capacity)))
		for step := 0; step < 2000; step++ {
			if rng.Intn(2) == 0 || len(live) == 0 {
				if h, err := tab.Insert(step); err == nil {
					live = append(live, h)
				}
			} else {
				i := rng.Intn(len(live))
				if err := tab.Remove(live[i]); err != nil {
					t.Fatalf("容量%d 第%d步 Remove 存活句柄失败: %v", capacity, step, err)
				}
				live = append(live[:i], live[i+1:]...)
			}
		}
		if err := audit.Check(tab, live...); err != nil {
			t.Fatalf("容量%d 自检失败: %v", capacity, err)
		}
		st := audit.Collect(tab)
		if st.Alive+st.Free+st.Exhausted != st.Cap || st.Alive != tab.Len() || tab.Len() != len(live) {
			t.Fatalf("容量%d 等式不成立: %+v Len=%d live=%d", capacity, st, tab.Len(), len(live))
		}
	}
}

// 把已失效句柄谎报为存活句柄时，自检必须检出。
func TestCheckRejectsBadLiveSet(t *testing.T) {
	tab, _ := table.New(2)
	h, _ := tab.Insert("x")
	if err := audit.Check(tab, h); err != nil {
		t.Fatalf("真实存活句柄应通过自检: %v", err)
	}
	_ = tab.Remove(h)
	if err := audit.Check(tab, h); !errors.Is(err, audit.ErrInvariant) {
		t.Fatalf("失效句柄混入存活集合应检出自检错误, got %v", err)
	}
}

// ABA 专项：一个 goroutine 反复复用槽位，N 个 goroutine 持失效句柄 Get，
// 老句柄一次都不得成功；结束后自检通过。
func TestConcurrentABA(t *testing.T) {
	tab, _ := table.New(1)
	h, _ := tab.Insert("v0")
	if err := tab.Remove(h); err != nil {
		t.Fatal(err)
	}
	var bad, done atomic.Int32
	var wg, readers sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for done.Load() == 0 {
			if nh, err := tab.Insert("x"); err == nil {
				_ = tab.Remove(nh)
			}
		}
	}()
	for i := 0; i < 8; i++ {
		readers.Add(1)
		go func() {
			defer readers.Done()
			for j := 0; j < 5000; j++ {
				if _, err := tab.Get(h); err == nil {
					bad.Add(1)
				} else if !errors.Is(err, table.ErrStale) {
					bad.Add(1)
				}
			}
		}()
	}
	readers.Wait()
	done.Store(1)
	wg.Wait()
	if bad.Load() != 0 {
		t.Fatalf("失效句柄在并发复用下成功了 %d 次", bad.Load())
	}
	if err := audit.Check(tab); err != nil {
		t.Fatalf("并发结束后自检失败: %v", err)
	}
	st := audit.Collect(tab)
	if st.Alive+st.Free+st.Exhausted != st.Cap {
		t.Fatalf("并发结束后等式不成立: %+v", st)
	}
}

// 多 goroutine 混合 Insert/Get/Remove/Len，race 下干净且自检通过。
func TestConcurrentMixed(t *testing.T) {
	tab, _ := table.New(16)
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(seed int64) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(seed))
			var mine []handle.Handle
			for i := 0; i < 500; i++ {
				switch rng.Intn(3) {
				case 0:
					if h, err := tab.Insert(i); err == nil {
						mine = append(mine, h)
					}
				case 1:
					if len(mine) > 0 {
						h := mine[rng.Intn(len(mine))]
						_, _ = tab.Get(h)
					}
				default:
					if len(mine) > 0 {
						j := rng.Intn(len(mine))
						_ = tab.Remove(mine[j])
						mine = append(mine[:j], mine[j+1:]...)
					}
				}
				_ = tab.Len()
			}
		}(int64(g))
	}
	wg.Wait()
	if err := audit.Check(tab); err != nil {
		t.Fatalf("混合并发结束后自检失败: %v", err)
	}
}

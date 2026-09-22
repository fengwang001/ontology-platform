package equivalence_test

import (
	"fmt"
	"sync"
	"sync/atomic"
	"testing"

	"ontology/equivalence"
)

// 并发 Union 把所有元素连成一条逻辑链，结束后必须恰好 1 个类。
func TestConcurrentUnionNoLostMerge(t *testing.T) {
	const n = 2000
	uf := equivalence.New()

	var wg sync.WaitGroup
	for worker := 0; worker < 16; worker++ {
		wg.Add(1)
		go func(offset int) {
			defer wg.Done()
			for i := offset; i < n-1; i += 16 {
				a := fmt.Sprintf("n%d", i)
				b := fmt.Sprintf("n%d", i+1)
				if _, _, err := uf.Union(a, b); err != nil {
					t.Errorf("Union: %v", err)
					return
				}
			}
		}(worker)
	}
	wg.Wait()

	if count := uf.ClassCount(); count != 1 {
		t.Fatalf("并发合并后类数 = %d，期望恰好 1（存在丢失合并）", count)
	}
	for i := 0; i < n; i++ {
		rep, err := uf.Find(fmt.Sprintf("n%d", i))
		if err != nil {
			t.Fatal(err)
		}
		if rep != "n0" {
			t.Fatalf("n%d 代表元 = %q，期望 n0", i, rep)
		}
	}
}

// 连通性只允许 false -> true 单向变化；并发读不得观察到「合并后又断开」。
func TestMonotonicConnectivityUnderConcurrency(t *testing.T) {
	uf := equivalence.New()
	uf.Add("a")
	uf.Add("b")

	var stop atomic.Bool
	var wg sync.WaitGroup

	for reader := 0; reader < 8; reader++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			everTrue := false
			for !stop.Load() {
				connected, err := uf.Equivalent("a", "b")
				if err != nil {
					t.Errorf("Equivalent 返回错误: %v", err)
					return
				}
				if everTrue && !connected {
					t.Error("观察到 true 之后又变回 false")
					return
				}
				if connected {
					everTrue = true
				}
			}
		}()
	}

	// 制造一些同类重复合并，让读者在 true 状态下持续读取。
	for i := 0; i < 1000; i++ {
		if _, _, err := uf.Union("a", "b"); err != nil {
			t.Fatal(err)
		}
	}
	stop.Store(true)
	wg.Wait()

	connected, err := uf.Equivalent("a", "b")
	if err != nil || !connected {
		t.Fatalf("最终连通性 = (%v,%v)", connected, err)
	}
}

// 混合并发 Find/Add/Union/Classes/ClassCount 只要求不 panic、不竞争、状态自洽。
func TestMixedConcurrentAccess(t *testing.T) {
	uf := equivalence.New()
	var wg sync.WaitGroup

	for worker := 0; worker < 12; worker++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for k := 0; k < 500; k++ {
				x := fmt.Sprintf("w%d-e%d", id, k%50)
				y := fmt.Sprintf("w%d-e%d", (id+1)%12, k%50)
				_, _, _ = uf.Union(x, y)
				_, _ = uf.Find(x)
				_ = uf.ClassCount()
				_ = uf.Classes()
			}
		}(worker)
	}
	wg.Wait()

	totalMembers := 0
	for _, class := range uf.Classes() {
		totalMembers += len(class)
	}
	if totalMembers != 12*50 {
		t.Fatalf("成员总数 = %d，期望 %d", totalMembers, 12*50)
	}
}

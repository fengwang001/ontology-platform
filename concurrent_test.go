package ontology

import (
	"fmt"
	"sync"
	"testing"
)

// TestConcurrentUnionsConvergeToOneClass 并发把 n 个元素两两并成环，
// 结束后必须恰好是 1 个类（不得丢失合并）。
func TestConcurrentUnionsConvergeToOneClass(t *testing.T) {
	const n = 2000
	u := New()
	for i := 0; i < n; i++ {
		u.Add(fmt.Sprintf("n%04d", i))
	}
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			u.Union(fmt.Sprintf("n%04d", i), fmt.Sprintf("n%04d", (i+1)%n))
		}(i)
	}
	wg.Wait()
	if got := u.ClassCount(); got != 1 {
		t.Fatalf("并发合并后 ClassCount()=%d, 期望 1", got)
	}
	rep, err := u.Find("n0000")
	if err != nil {
		t.Fatalf("Find 出错: %v", err)
	}
	for i := 1; i < n; i++ {
		got, err := u.Find(fmt.Sprintf("n%04d", i))
		if err != nil || got != rep {
			t.Fatalf("Find(n%04d)=%q, %v; 期望统一代表元 %q", i, got, err, rep)
		}
	}
}

// TestConcurrentConnectedMonotonic 并发读写期间，
// Connected(a,b) 一旦为真不得再变回假。
func TestConcurrentConnectedMonotonic(t *testing.T) {
	const n = 500
	u := New()
	for i := 0; i < n; i++ {
		u.Add(fmt.Sprintf("m%04d", i))
	}
	var wg sync.WaitGroup
	stop := make(chan struct{})
	// 读协程：持续观察 Connected("m0000", "m0001")，为真后不得变假。
	for g := 0; g < 4; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			seen := false
			for {
				select {
				case <-stop:
					return
				default:
				}
				ok, err := u.Connected("m0000", "m0001")
				if err != nil {
					t.Errorf("Connected 出错: %v", err)
					return
				}
				if seen && !ok {
					t.Error("Connected 从 true 变回 false")
					return
				}
				seen = seen || ok
			}
		}()
	}
	// 写协程：并发把所有元素并成一条链。
	var writers sync.WaitGroup
	for i := 0; i < n-1; i++ {
		writers.Add(1)
		go func(i int) {
			defer writers.Done()
			u.Union(fmt.Sprintf("m%04d", i), fmt.Sprintf("m%04d", i+1))
		}(i)
	}
	// 等待写协程全部完成后再通知读协程退出。
	writers.Wait()
	close(stop)
	wg.Wait()
	ok, err := u.Connected("m0000", "m0001")
	if err != nil || !ok {
		t.Fatalf("合并完成后 Connected(m0000,m0001)=%v, %v; 期望 true, nil", ok, err)
	}
}

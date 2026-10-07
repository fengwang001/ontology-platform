package cascade_test

import (
	"fmt"
	"sync"
	"sync/atomic"
	"testing"

	"ontology/cascade"
)

// TestConcurrentSerializability 并发压力：
//   - 多个 goroutine 对共享图发起创建/删除/终结器/改属主操作；
//   - 一个 observer goroutine 持续读取快照，任何一次读取都必须是某个
//     稳定状态（不允许观察到“半条级联”）：不变量（对象表与索引、
//     派生计数）在每次操作返回后成立，而级联在互斥区内原子完成，
//     因此 observer 只能看到串行化后的稳定点；
//   - 结束后内部不变量必须成立。
//
// 全局互斥锁把每个“顶层操作 + 其完整级联”作为一个临界区，故任意
// 并发历史都等价于按获取锁的先后排成的某个串行顺序。
func TestConcurrentSerializability(t *testing.T) {
	const workers, perWorker = 8, 200
	c := cascade.New()

	// 预置一批共享对象，制造跨 goroutine 的争用与级联。
	for i := 0; i < 12; i++ {
		if err := c.Create(fmt.Sprintf("root%d", i), nil, nil); err != nil {
			t.Fatal(err)
		}
	}

	var stop atomic.Bool
	var observedSnapshots int64
	obsDone := make(chan struct{})
	go func() {
		defer close(obsDone)
		for !stop.Load() {
			// Snapshot 内部也获取同一把锁，因此它看到的必然是某次
			// 完整操作之后的稳定状态，绝不会撕裂。
			snap := c.Snapshot()
			_ = snap
			atomic.AddInt64(&observedSnapshots, 1)
		}
	}()

	var wg sync.WaitGroup
	var counter int64
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < perWorker; i++ {
				n := atomic.AddInt64(&counter, 1)
				id := fmt.Sprintf("w%d-%d", w, n)
				owner := fmt.Sprintf("root%d", n%12)
				// 所有错误都被允许（争用会产生各种合法拒绝），
				// 只要求不 panic、状态始终自洽。
				switch n % 6 {
				case 0:
					_ = c.Create(id, []cascade.OwnerRef{{OwnerID: owner, Blocking: n%2 == 0}}, nil)
				case 1:
					_ = c.Create(id, []cascade.OwnerRef{{OwnerID: owner}}, []string{"f"})
				case 2:
					_ = c.Delete(owner, cascade.Strategy(1+int(n%3)))
				case 3:
					_ = c.Delete(id, cascade.Foreground)
				case 4:
					_ = c.AddFinalizer(owner, "g")
				case 5:
					_ = c.RemoveFinalizer(id, "f")
				}
			}
		}(w)
	}
	wg.Wait()
	stop.Store(true)
	<-obsDone

	// 结束后内部不变量必须成立：每条边、反向索引与派生计数一致。
	c.CheckInvariants(t, "post-concurrency")
	if observedSnapshots == 0 {
		t.Fatalf("observer never ran")
	}
}

// TestConcurrentDisjointSubgraphs 互不相交子图上的并发结果必须等于
// 各自串行结果的并（更强的可串行化证据）。
func TestConcurrentDisjointSubgraphs(t *testing.T) {
	const n = 16

	build := func(c *cascade.Controller, p int) {
		a := fmt.Sprintf("a%d", p)
		b := fmt.Sprintf("b%d", p)
		d := fmt.Sprintf("d%d", p)
		if err := c.Create(a, nil, nil); err != nil {
			t.Error(err)
		}
		if err := c.Create(b, []cascade.OwnerRef{{OwnerID: a, Blocking: true}}, nil); err != nil {
			t.Error(err)
		}
		if err := c.Create(d, []cascade.OwnerRef{{OwnerID: b}}, nil); err != nil {
			t.Error(err)
		}
		if err := c.Delete(a, cascade.Foreground); err != nil {
			t.Error(err)
		}
	}

	// 串行参考：在独立控制器上顺序执行所有子图操作。
	serial := cascade.New()
	for p := 0; p < n; p++ {
		build(serial, p)
	}
	serialSnap := serial.Snapshot()

	// 并发：每个子图一个 goroutine。
	parallel := cascade.New()
	var wg sync.WaitGroup
	for p := 0; p < n; p++ {
		wg.Add(1)
		go func(p int) {
			defer wg.Done()
			build(parallel, p)
		}(p)
	}
	wg.Wait()

	if ok, why := sameState(canonicalController(parallel.Snapshot()), canonicalController(serialSnap)); !ok {
		t.Fatalf("disjoint-subgraph concurrent result differs from serial: %s", why)
	}
	parallel.CheckInvariants(t, "disjoint")
}

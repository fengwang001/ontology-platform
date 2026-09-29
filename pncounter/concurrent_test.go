package pncounter

import (
	"fmt"
	"math/big"
	"runtime"
	"sync"
	"testing"
)

// TestConcurrentOps 多个执行体并发进行增减、合并、求值、快照、自检。
// 配合 `go test -race` 验证无数据竞争；最终全量合并后必须收敛到参照值。
func TestConcurrentOps(t *testing.T) {
	const n = 5
	const workers = 8
	c, _ := NewClusterWithLimit(n, 1<<30)

	// 参照计数：每个 (副本, 方向) 成功执行的次数，用互斥保护。
	var refMu sync.Mutex
	refInc := make([]uint64, n)
	refDec := make([]uint64, n)

	var wg sync.WaitGroup
	stop := make(chan struct{})

	// 增减执行体：只改自己所属副本的分量。
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(seed int) {
			defer wg.Done()
			id := seed % n
			r, _ := c.Replica(id)
			i := 0
			for {
				select {
				case <-stop:
					return
				default:
				}
				delta := uint64(1 + (i % 7))
				var err error
				if i%2 == 0 {
					err = r.Increment(delta)
				} else {
					err = r.Decrement(delta)
				}
				if err == nil {
					refMu.Lock()
					if i%2 == 0 {
						refInc[id] += delta
					} else {
						refDec[id] += delta
					}
					refMu.Unlock()
				}
				i++
			}
		}(w)
	}

	// 合并执行体：随机两两合并（含同副本空操作）。
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(seed int) {
			defer wg.Done()
			i := 0
			for {
				select {
				case <-stop:
					return
				default:
				}
				dst := (seed + i) % n
				src := (seed*3 + i*2) % n
				rd, _ := c.Replica(dst)
				rs, _ := c.Replica(src)
				_ = rd.Merge(rs)
				i++
			}
		}(w)
	}

	// 只读执行体：并发求值、快照与自检。
	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func(seed int) {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				r, _ := c.Replica(seed % n)
				_ = r.Value()
				_ = r.Snapshot()
				_ = c.Check()
				runtime.Gosched()
			}
		}(w)
	}

	// 让所有执行体跑一会儿，然后统一停止。
	runtime.Gosched()
	for busy := 0; busy < 200; busy++ {
		for i := 0; i < n; i++ {
			for j := 0; j < n; j++ {
				ri, _ := c.Replica(i)
				rj, _ := c.Replica(j)
				_ = ri.Merge(rj)
			}
		}
	}
	close(stop)
	wg.Wait()

	// 停止后再做全量确定性合并，保证所有操作被传播到所有副本。
	for round := 0; round < n; round++ {
		for i := 0; i < n; i++ {
			for j := 0; j < n; j++ {
				ri, _ := c.Replica(i)
				rj, _ := c.Replica(j)
				if err := ri.Merge(rj); err != nil {
					t.Fatalf("收尾合并失败: %v", err)
				}
			}
		}
	}

	// 朴素参照的收敛值：ΣrefInc - ΣrefDec。
	expected := new(big.Int)
	for _, v := range refInc {
		expected.Add(expected, new(big.Int).SetUint64(v))
	}
	for _, v := range refDec {
		expected.Sub(expected, new(big.Int).SetUint64(v))
	}

	values := c.Values()
	parts := make([]string, n)
	for i, v := range values {
		if v.Cmp(expected) != 0 {
			t.Fatalf("并发收敛错误: R%d=%s, 参照=%s (inc=%v dec=%v)",
				i, v, expected, refInc, refDec)
		}
		// 各副本的增/减分量也必须与参照逐分量一致（合并不丢不重）。
		snap, _ := c.Replica(i)
		s := snap.Snapshot()
		for j := 0; j < n; j++ {
			if s.Inc[j] != refInc[j] || s.Dec[j] != refDec[j] {
				t.Fatalf("R%d 分量[%d] 不一致: inc %d!=%d dec %d!=%d",
					i, j, s.Inc[j], refInc[j], s.Dec[j], refDec[j])
			}
		}
		parts[i] = fmt.Sprintf("R%d=%s", i, v.String())
	}
	if err := c.Check(); err != nil {
		t.Fatalf("并发后自检失败: %v", err)
	}
	t.Logf("输入: %d 个增减执行体 + %d 个合并执行体 + 4 个只读执行体并发交错", workers, workers)
	t.Logf("判定依据: 收尾全量合并后 [%s]，与朴素参照 %s 逐分量一致",
		joinComma(parts), expected)
}

// TestReverseMergeNoDeadlock 专门制造同两副本互逆方向合并同时进行的场景。
// 若加锁顺序不全局一致，该测试会在超时时失败（go test 超时判定为死锁）。
func TestReverseMergeNoDeadlock(t *testing.T) {
	c, _ := NewCluster(4)
	_ = mustReplica(c, 1).Increment(100)
	_ = mustReplica(c, 2).Decrement(40)

	done := make(chan struct{})
	go func() {
		r1 := mustReplica(c, 1)
		r2 := mustReplica(c, 2)
		for i := 0; i < 10000; i++ {
			_ = r1.Merge(r2)
		}
		close(done)
	}()
	r1 := mustReplica(c, 1)
	r2 := mustReplica(c, 2)
	for i := 0; i < 10000; i++ {
		_ = r2.Merge(r1)
	}
	<-done

	if r1.Value().Cmp(r2.Value()) != 0 {
		t.Fatalf("互逆合并后两副本应相等: %s vs %s", r1.Value(), r2.Value())
	}
	if r1.Value().Cmp(bigInt(60)) != 0 {
		t.Fatalf("应收敛到 100-40=60, 得 %s", r1.Value())
	}
	t.Log("判定依据[互逆合并不死锁]: R1<->R2 双向各 10000 次合并后均为 60")
}

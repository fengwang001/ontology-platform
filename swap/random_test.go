package swap

import (
	"errors"
	"fmt"
	"math/rand/v2"
	"sync"
	"sync/atomic"
	"testing"
)

// pickSlot 以较大概率从近期分配的槽位中选取，否则随机
// （含 0 与越界编号，以覆盖 ErrRange 路径）。
func pickSlot(rng *rand.Rand, live []int, n int) int {
	if len(live) > 0 && rng.IntN(10) < 6 {
		return live[rng.IntN(len(live))]
	}
	return rng.IntN(n+2) - 1
}

func pickBatch(rng *rand.Rand, live []int, n int) []int {
	size := rng.IntN(7)
	batch := make([]int, size)
	for i := range batch {
		batch[i] = pickSlot(rng, live, n)
	}
	return batch
}

// TestRandomAgainstNaive 用 2000 组随机操作序列对照朴素模拟，
// 每步比较返回值、错误与完整状态，并校验不变量与计数守恒。
func TestRandomAgainstNaive(t *testing.T) {
	rng := rand.New(rand.NewPCG(1156, 20261003))
	for seq := 0; seq < 2000; seq++ {
		n := 2 + rng.IntN(150)
		k := 1 + rng.IntN(16)
		maxCount := 1 + rng.IntN(4)
		l, err := New(n, k, maxCount)
		if err != nil {
			t.Fatalf("New(%d, %d, %d): %v", n, k, maxCount, err)
		}
		m := newNaive(n, k, maxCount)
		ops := 40 + rng.IntN(160)
		live := make([]int, 0, n)
		refDelta := 0 // 成功的 Dup/Fork 元素数减去 Free/Release 元素数
		history := make([]string, 0, ops)

		fail := func(format string, args ...any) {
			t.Errorf("序列 %d（N=%d K=%d Max=%d）第 %d 步: %s",
				seq, n, k, maxCount, len(history), fmt.Sprintf(format, args...))
			t.Logf("操作历史: %v", history)
			t.FailNow()
		}

		for i := 0; i < ops; i++ {
			switch rng.IntN(10) {
			case 0, 1, 2: // Alloc
				gotS, gotErr := l.Alloc()
				wantS, wantErr := m.alloc()
				history = append(history, fmt.Sprintf("Alloc()->(%d,%v)", gotS, gotErr))
				if gotS != wantS || !errors.Is(gotErr, wantErr) {
					fail("Alloc 不一致：Ledger=(%d,%v) naive=(%d,%v)",
						gotS, gotErr, wantS, wantErr)
				}
				if gotErr == nil {
					live = append(live, gotS)
				}
			case 3: // Dup
				s := pickSlot(rng, live, n)
				gotErr := l.Dup(s)
				wantErr := m.dup(s)
				history = append(history, fmt.Sprintf("Dup(%d)->%v", s, gotErr))
				if !errors.Is(gotErr, wantErr) {
					fail("Dup(%d) 不一致：Ledger=%v naive=%v", s, gotErr, wantErr)
				}
				if gotErr == nil {
					refDelta++
				}
			case 4: // Free
				s := pickSlot(rng, live, n)
				gotErr := l.Free(s)
				wantErr := m.free(s)
				history = append(history, fmt.Sprintf("Free(%d)->%v", s, gotErr))
				if !errors.Is(gotErr, wantErr) {
					fail("Free(%d) 不一致：Ledger=%v naive=%v", s, gotErr, wantErr)
				}
				if gotErr == nil {
					refDelta--
				}
			case 5: // CacheAdd
				s := pickSlot(rng, live, n)
				gotErr := l.CacheAdd(s)
				wantErr := m.cacheAdd(s)
				history = append(history, fmt.Sprintf("CacheAdd(%d)->%v", s, gotErr))
				if !errors.Is(gotErr, wantErr) {
					fail("CacheAdd(%d) 不一致：Ledger=%v naive=%v", s, gotErr, wantErr)
				}
			case 6: // CacheDrop
				s := pickSlot(rng, live, n)
				gotErr := l.CacheDrop(s)
				wantErr := m.cacheDrop(s)
				history = append(history, fmt.Sprintf("CacheDrop(%d)->%v", s, gotErr))
				if !errors.Is(gotErr, wantErr) {
					fail("CacheDrop(%d) 不一致：Ledger=%v naive=%v", s, gotErr, wantErr)
				}
			case 7: // Fork
				batch := pickBatch(rng, live, n)
				gotI, gotErr := l.Fork(batch)
				wantI, wantErr := m.fork(batch)
				history = append(history, fmt.Sprintf("Fork(%v)->(%d,%v)", batch, gotI, gotErr))
				if gotI != wantI || !errors.Is(gotErr, wantErr) {
					fail("Fork(%v) 不一致：Ledger=(%d,%v) naive=(%d,%v)",
						batch, gotI, gotErr, wantI, wantErr)
				}
				if gotErr == nil {
					refDelta += len(batch)
				}
			case 8: // Release
				batch := pickBatch(rng, live, n)
				gotI, gotErr := l.Release(batch)
				wantI, wantErr := m.release(batch)
				history = append(history, fmt.Sprintf("Release(%v)->(%d,%v)", batch, gotI, gotErr))
				if gotI != wantI || !errors.Is(gotErr, wantErr) {
					fail("Release(%v) 不一致：Ledger=(%d,%v) naive=(%d,%v)",
						batch, gotI, gotErr, wantI, wantErr)
				}
				if gotErr == nil {
					refDelta -= len(batch)
				}
			case 9: // 查询一致性
				s := pickSlot(rng, live, n)
				gotC, gotCache := l.Info(s)
				wantC, wantCache := 0, false
				if s >= 1 && s <= n-1 {
					wantC, wantCache = m.count[s], m.cache[s]
				}
				if gotC != wantC || gotCache != wantCache {
					fail("Info(%d) 不一致：Ledger=(%d,%v) naive=(%d,%v)",
						s, gotC, gotCache, wantC, wantCache)
				}
				if l.Used() != m.used || l.FreeSlots() != n-1-m.used {
					fail("Used/FreeSlots 不一致")
				}
			}
			wantSameState(t, l, m)
		}

		// 计数守恒：成功的 Dup/Fork 元素数减去 Free/Release 元素数
		// 等于所有槽位 count 之和。
		sum := 0
		for s := 1; s < n; s++ {
			sum += int(l.count[s])
		}
		if sum != refDelta {
			t.Fatalf("序列 %d：count 之和 %d 与净引用 %d 不一致", seq, sum, refDelta)
		}
		wantInvariants(t, l)
		t.Logf("序列 %d: 输入 N=%d K=%d Max=%d 操作数=%d；输出 末态 used=%d 队列=%v；判定=与朴素模拟逐步一致且不变量成立",
			seq, n, k, maxCount, ops, l.Used(), l.FreeClusters())
	}
}

// TestDeterministicReplay 相同的操作序列重放得到完全相同的
// 分配序列与队列。
func TestDeterministicReplay(t *testing.T) {
	rng := rand.New(rand.NewPCG(7, 7))
	const n, k, maxCount = 40, 3, 3
	type op struct {
		kind  int
		slots []int
	}
	ops := make([]op, 500)
	live := []int{}
	for i := range ops {
		switch rng.IntN(6) {
		case 0, 1:
			ops[i] = op{kind: 0}
		case 2:
			ops[i] = op{kind: 1, slots: []int{pickSlot(rng, live, n)}}
		case 3:
			ops[i] = op{kind: 2, slots: []int{pickSlot(rng, live, n)}}
		case 4:
			ops[i] = op{kind: 3, slots: []int{pickSlot(rng, live, n)}}
		default:
			ops[i] = op{kind: 4, slots: pickBatch(rng, live, n)}
		}
		if ops[i].kind == 0 {
			live = append(live, rng.IntN(n))
		}
	}
	run := func() ([]int, [][]int) {
		l, err := New(n, k, maxCount)
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		allocs := []int{}
		queues := [][]int{}
		for _, o := range ops {
			switch o.kind {
			case 0:
				s, err := l.Alloc()
				if err == nil {
					allocs = append(allocs, s)
				}
			case 1:
				l.Dup(o.slots[0])
			case 2:
				l.Free(o.slots[0])
			case 3:
				l.CacheDrop(o.slots[0])
			case 4:
				l.Fork(o.slots)
			}
			queues = append(queues, l.FreeClusters())
		}
		return allocs, queues
	}
	a1, q1 := run()
	a2, q2 := run()
	if fmt.Sprint(a1) != fmt.Sprint(a2) || fmt.Sprint(q1) != fmt.Sprint(q2) {
		t.Fatalf("重放结果不一致")
	}
}

// TestConcurrent 并发调用所有操作与查询，结束后校验不变量与计数守恒。
func TestConcurrent(t *testing.T) {
	const n, k, maxCount = 4096, 8, 16
	l, err := New(n, k, maxCount)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	var dups, frees atomic.Int64
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(seed uint64) {
			defer wg.Done()
			rng := rand.New(rand.NewPCG(seed, seed+1))
			live := []int{}
			for i := 0; i < 20000; i++ {
				switch rng.IntN(12) {
				case 0, 1, 2:
					if s, err := l.Alloc(); err == nil {
						live = append(live, s)
					}
				case 3:
					if l.Dup(pickSlot(rng, live, n)) == nil {
						dups.Add(1)
					}
				case 4:
					if l.Free(pickSlot(rng, live, n)) == nil {
						frees.Add(1)
					}
				case 5:
					l.CacheAdd(pickSlot(rng, live, n))
				case 6:
					l.CacheDrop(pickSlot(rng, live, n))
				case 7:
					batch := pickBatch(rng, live, n)
					if _, err := l.Fork(batch); err == nil {
						dups.Add(int64(len(batch)))
					}
				case 8:
					batch := pickBatch(rng, live, n)
					if _, err := l.Release(batch); err == nil {
						frees.Add(int64(len(batch)))
					}
				case 9:
					l.Info(pickSlot(rng, live, n))
				case 10:
					l.Used()
					l.FreeSlots()
				case 11:
					l.FreeClusters()
					l.Current()
				}
			}
		}(uint64(g) + 1)
	}
	wg.Wait()

	if got := l.Used() + l.FreeSlots(); got != n-1 {
		t.Fatalf("Used()+FreeSlots() = %d，期望 %d", got, n-1)
	}
	sum := 0
	for s := 1; s < n; s++ {
		sum += int(l.count[s])
	}
	if want := dups.Load() - frees.Load(); int64(sum) != want {
		t.Fatalf("count 之和 %d 与净引用 %d 不一致", sum, want)
	}
	wantInvariants(t, l)
}

// ceilLog2 返回不小于 x 的最小的 2 的幂的指数。
func ceilLog2(x int) int {
	e := 0
	for (1 << e) < x {
		e++
	}
	return e
}

// TestProbesBound 在 N=10^6、K=8 下用非导出计数器 probes 验证：
// 非回退路径考察的槽位数不超过 K，回退路径考察的簇数不超过
// 2*ceil(log2(簇数))+2。
func TestProbesBound(t *testing.T) {
	const n, k = 1_000_000, 8
	l, err := New(n, k, 255)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	clusters := (n-1)/k + 1
	bound := 2*ceilLog2(clusters) + 2

	// 第一次 Alloc 走第 2 步（出队），第二次走第 1 步（非回退）。
	if _, err := l.Alloc(); err != nil {
		t.Fatalf("Alloc: %v", err)
	}
	if _, err := l.Alloc(); err != nil {
		t.Fatalf("Alloc: %v", err)
	}
	if l.probes > k {
		t.Fatalf("非回退路径 probes=%d 超过 K=%d", l.probes, k)
	}

	// 填满整个交换区，使队列变空。
	for l.used < n-1 {
		if _, err := l.Alloc(); err != nil {
			t.Fatalf("Alloc: %v", err)
		}
	}
	// 制造一个低编号空闲槽位，下一次 Alloc 必走回退路径。
	if err := l.CacheDrop(2); err != nil {
		t.Fatalf("CacheDrop(2): %v", err)
	}
	s, err := l.Alloc()
	if err != nil {
		t.Fatalf("Alloc: %v", err)
	}
	if s != 2 {
		t.Fatalf("回退路径 Alloc() = %d，期望 2（全局最小空闲槽位）", s)
	}
	if l.probes > bound {
		t.Fatalf("回退路径 probes=%d 超过上界 %d", l.probes, bound)
	}
	t.Logf("N=%d K=%d 簇数=%d：非回退 probes<=%d，回退 probes=%d（上界 %d）",
		n, k, clusters, k, l.probes, bound)
}

package ftl

import (
	"fmt"
	"sync"
	"testing"
)

// TestVictimSelectionIndependentOfBlockCount 受害块选取开销不随块总数
// 线性增长：完全相同的操作序列在 64 块与 2048 块的闪存上执行，
// 受害块堆的弹出次数必须完全相同（堆操作只与堆中项数对数相关，
// 而项数由写入/失效次数决定，与块总数无关）。
func TestVictimSelectionIndependentOfBlockCount(t *testing.T) {
	pops := make([]int, 0, 2)
	for _, numBlocks := range []int{64, 2048} {
		cfg := Config{
			NumBlocks:     numBlocks,
			PagesPerBlock: 4,
			LogicalPages:  4 * numBlocks,
			LowWatermark:  2,
			HighWatermark: 3,
			EraseLimit:    1 << 30,
			WearThreshold: 1 << 30,
		}
		f := mustNew(t, cfg)
		// 填满除 2 个空闲块外的所有块（达到准入上限）。
		limit := (numBlocks - cfg.LowWatermark) * cfg.PagesPerBlock
		for i := 0; i < limit; i++ {
			mustWrite(t, f, uint64(i))
		}
		mustDiscard(t, f, 0) // 块 0 出现一个失效页：唯一有收益的受害块

		before := f.dbg.victimPops
		mustWrite(t, f, 1) // 覆盖写：空闲=1 触发恰好一轮回收
		pops = append(pops, f.dbg.victimPops-before)
	}
	if pops[0] != pops[1] {
		t.Fatalf("victim pops must not depend on block count: 64 blocks -> %d, 2048 blocks -> %d",
			pops[0], pops[1])
	}
	if pops[0] > 8 {
		t.Fatalf("victim pops per GC = %d, want small constant (got > 8)", pops[0])
	}
	t.Logf("victim pops per GC: blocks=64 -> %d, blocks=2048 -> %d", pops[0], pops[1])
}

// TestWriteIndependentOfBlocksAndMappings 不触发回收的写入，其开销
// 不随块总数与映射条目数增长：不同块数、不同既有映射数下执行相同
// 写入序列，堆操作计数必须完全一致（映射表为 O(1) 哈希查找）。
func TestWriteIndependentOfBlocksAndMappings(t *testing.T) {
	run := func(numBlocks, prefill int) (pushes, pops int) {
		cfg := Config{
			NumBlocks:     numBlocks,
			PagesPerBlock: 4,
			LogicalPages:  100000,
			LowWatermark:  2,
			HighWatermark: 3,
			EraseLimit:    1 << 30,
			WearThreshold: 1 << 30,
		}
		f := mustNew(t, cfg)
		for i := 0; i < prefill; i++ {
			mustWrite(t, f, uint64(i))
		}
		f.dbg = debugCounters{} // 只统计后续 100 次写入
		for i := prefill; i < prefill+100; i++ {
			mustWrite(t, f, uint64(i))
		}
		return f.dbg.heapPushes, f.dbg.victimPops
	}
	p1, v1 := run(64, 0)
	p2, v2 := run(4096, 0)
	p3, v3 := run(4096, 4000)
	if p1 != p2 || p2 != p3 {
		t.Fatalf("heap pushes must not depend on block/mapping count: %d, %d, %d", p1, p2, p3)
	}
	if v1 != 0 || v2 != 0 || v3 != 0 {
		t.Fatalf("no GC expected: victim pops = %d, %d, %d", v1, v2, v3)
	}
	// 100 次写入填满 25 个块，每块写满压入 2 项，总计 50，与规模无关。
	if p1 != 50 {
		t.Fatalf("heap pushes = %d, want 50 (25 filled blocks x 2)", p1)
	}
	t.Logf("heap pushes per 100 writes: (64 blocks)=%d (4096 blocks)=%d (4096 blocks, 4000 mappings)=%d",
		p1, p2, p3)
}

// TestConcurrent 并发调用等价于某个串行顺序：各 goroutine 独占互不相交
// 的逻辑页区间，因此无论交错顺序如何，最终状态确定；配合 -race 验证。
func TestConcurrent(t *testing.T) {
	cfg := Config{
		NumBlocks:     128, // 准入上限 (128-2)*8 = 1008 > 800 个逻辑页
		PagesPerBlock: 8,
		LogicalPages:  4096,
		LowWatermark:  2,
		HighWatermark: 4,
		EraseLimit:    1 << 30,
		WearThreshold: 1 << 30,
	}
	f := mustNew(t, cfg)

	const workers = 8
	const span = 100 // 每个 goroutine 独占的逻辑页数
	const rounds = 200

	type final struct {
		written bool
		data    []byte
	}
	expected := make([][]final, workers)
	for w := range expected {
		expected[w] = make([]final, span)
	}

	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			base := uint64(w * span)
			for i := 0; i < rounds; i++ {
				slot := i % span
				lpn := base + uint64(slot)
				data := []byte{byte(w), byte(i)}
				switch i % 4 {
				case 3:
					if err := f.Discard(lpn); err != nil {
						t.Errorf("Discard(%d): %v", lpn, err)
					}
					expected[w][slot] = final{}
				default:
					if err := f.Write(lpn, data); err != nil {
						t.Errorf("Write(%d): %v", lpn, err)
					}
					expected[w][slot] = final{written: true, data: data}
				}
			}
		}(w)
	}
	wg.Wait()

	// 最终状态必须与"每个区间最后一次操作"完全一致。
	for w := 0; w < workers; w++ {
		for slot := 0; slot < span; slot++ {
			lpn := uint64(w*span + slot)
			got, err := f.Read(lpn)
			want := expected[w][slot]
			if !want.written {
				if err != ErrNotWritten {
					t.Errorf("Read(%d) = %v, want ErrNotWritten", lpn, err)
				}
				continue
			}
			if err != nil || fmt.Sprintf("%v", got) != fmt.Sprintf("%v", want.data) {
				t.Errorf("Read(%d) = %v, %v; want %v", lpn, got, err, want.data)
			}
		}
	}
	// 统计量守恒：并发下不变式仍成立。
	checkInvariants(t, dumpFTL(f), cfg, "concurrent")
}

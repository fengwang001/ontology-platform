package service_test

import (
	"fmt"
	"sync"
	"testing"

	"ontology/atp/order"
	"ontology/atp/service"
)

// 可验证地证明：单行承诺判定考察的预留记录数不随历史订单总数
// 或已失效预留数增长。
//
// 机制：每个 (仓库, 商品) 的预留按到期时刻组成最小堆，另维护
// 滚动和；可承诺量计算只读滚动和，并对堆顶做懒过期清理。每条
// 失效预留最多被弹出考察一次，此后从堆中移除、不再出现。
// 因此总考察次数 <= 历史预留总数 + 扫描次数（每次扫描至多
// 多考察一条“未到期”的堆顶记录）。
func TestReservationExaminationBounded(t *testing.T) {
	s := service.New()
	must(t, s.AddWarehouse(0, "w1", 1))
	must(t, s.AddStock(0, "w1", "skuA", 1<<40))

	const history = 5000
	var created uint64
	// 制造大量已失效预留：ttl=1，随后时钟越过到期时刻。
	for i := 0; i < history; i++ {
		now := int64(2*i + 1)
		_, err := s.Commit(commitReq(fmt.Sprintf("h%d", i), now,
			[]order.Line{{SKU: "skuA", Qty: 1}}, false, 1, 1))
		must(t, err)
		created++
	}
	examinedBefore, sweepsBefore := s.Stats()

	// 在积累了 5000 条已失效预留之后，再做 100 次单行承诺，
	// 每次考察的预留记录数应为常数级（不随历史增长）。
	const extra = 100
	for i := 0; i < extra; i++ {
		now := int64(2*history + 2*i + 1)
		_, err := s.Commit(commitReq(fmt.Sprintf("x%d", i), now,
			[]order.Line{{SKU: "skuA", Qty: 1}}, false, 100000, 1))
		must(t, err)
		created++
	}
	examinedAfter, sweepsAfter := s.Stats()

	examined := examinedAfter - examinedBefore
	sweeps := sweepsAfter - sweepsBefore
	t.Logf("extra commits=%d examined=%d sweeps=%d per-commit=%.2f",
		extra, examined, sweeps, float64(examined)/extra)
	// 每次承诺只触发一次扫描，至多考察：已弹出的失效记录
	// （摊还每条一次）+ 一条未到期堆顶。这里历史失效记录已在
	// 前若干次扫描中弹出完毕，之后每次承诺考察数应为常数。
	if per := examined / extra; per > 4 {
		t.Fatalf("examined per commit = %d, grows with history", per)
	}
	// 全局上界：总考察次数 <= 总预留数 + 总扫描次数。
	totalExamined, totalSweeps := s.Stats()
	if totalExamined > created+totalSweeps {
		t.Fatalf("examined %d exceeds created %d + sweeps %d", totalExamined, created, totalSweeps)
	}
	t.Logf("total: created=%d examined=%d sweeps=%d (examined <= created+sweeps holds)",
		created, totalExamined, totalSweeps)
}

// 所有操作可并发调用，结果等价于某个串行顺序：
// 并发承诺同一有限库存，成功承诺的总量不得超过供给，
// 且不变量（现货非负、有效预留不超可承诺上限）始终成立。
func TestConcurrentCommits(t *testing.T) {
	s := service.New()
	must(t, s.AddWarehouse(0, "w1", 1))
	must(t, s.AddWarehouse(0, "w2", 2))
	must(t, s.AddStock(0, "w1", "skuA", 500))
	must(t, s.AddStock(0, "w2", "skuA", 500))

	const workers = 16
	const perWorker = 50
	var wg sync.WaitGroup
	var mu sync.Mutex
	var committed int64
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < perWorker; i++ {
				// 所有 goroutine 用同一时刻：时钟允许相等，
				// 被拒绝（缺货）的承诺不改变状态。
				res, err := s.Commit(commitReq(
					fmt.Sprintf("w%d-o%d", w, i), 10,
					[]order.Line{{SKU: "skuA", Qty: 3}}, true, 100, 2))
				if err == nil {
					var q int64
					for _, a := range res.Allocations {
						q += a.Qty
					}
					mu.Lock()
					committed += q
					mu.Unlock()
				}
			}
		}(w)
	}
	wg.Wait()
	if committed > 1000 {
		t.Fatalf("committed %d exceeds total supply 1000", committed)
	}
	if err := s.CheckInvariants(); err != nil {
		t.Fatalf("invariant violated: %v", err)
	}
	// 最终可承诺量 + 已承诺量 = 总供给。
	a1, err := s.QueryATP("w1", "skuA", 10)
	must(t, err)
	a2, err := s.QueryATP("w2", "skuA", 10)
	must(t, err)
	if a1+a2+committed != 1000 {
		t.Fatalf("ATP %d + %d + committed %d != 1000", a1, a2, committed)
	}
	t.Logf("committed=%d remaining ATP w1=%d w2=%d", committed, a1, a2)
}

package consignment

import (
	"sync"
	"testing"
)

// TestConcurrentDrawsAndQueries 在竞态检测下并发执行领用与查询：
// 结果须等价于某个串行顺序——总量守恒、批次剩余量恒在 [0, 到货量] 内，
// 且读不到拆分到一半的领用。
func TestConcurrentDrawsAndQueries(t *testing.T) {
	s := New()
	const suppliers = 4
	const item = 1
	const perSupplier = 250
	// 准备：每个供应商到货 250，单价 2，时钟推进到 100。
	for sup := uint64(1); sup <= suppliers; sup++ {
		if err := s.AddPrice(sup, item, 0, 1000, 2, 0); err != nil {
			t.Fatal(err)
		}
		if _, err := s.Arrive(sup, item, perSupplier, 100000, 0); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.SetCap(1, item, 1, 100); err != nil { // 推进时钟到 100
		t.Fatal(err)
	}

	const workers = 8
	const drawsPerWorker = 200
	var wg sync.WaitGroup
	drawn := make([][]Line, workers)
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < drawsPerWorker; i++ {
				// 所有领用携带同一时刻 100（不小于上次被接受时刻，允许）。
				lines, err := s.Draw(item, 1, 100)
				if err == nil {
					drawn[w] = append(drawn[w], lines...)
				}
				// 查询与写并发：必须读到某个已完成操作之后的一致快照。
				for sup := uint64(1); sup <= suppliers; sup++ {
					total, expired := s.OnHand(sup, item)
					if total > perSupplier || expired > total {
						t.Errorf("不一致快照: sup=%d total=%d expired=%d", sup, total, expired)
						return
					}
				}
			}
		}(w)
	}
	wg.Wait()

	// 总量守恒：成功领用总数 + 剩余在库 = 到货总量。
	var drawnTotal uint64
	perBatch := make(map[uint64]uint64)
	for _, lines := range drawn {
		for _, ln := range lines {
			drawnTotal += ln.Qty
			perBatch[ln.BatchID] += ln.Qty
			if ln.Qty != 1 || ln.Amount != 2 {
				t.Fatalf("结算行应为数量 1 金额 2，得到 %+v", ln)
			}
		}
	}
	var remainTotal uint64
	for sup := uint64(1); sup <= suppliers; sup++ {
		total, _ := s.OnHand(sup, item)
		remainTotal += total
	}
	if drawnTotal+remainTotal != suppliers*perSupplier {
		t.Fatalf("总量不守恒: 领用 %d + 在库 %d != %d",
			drawnTotal, remainTotal, suppliers*perSupplier)
	}
	// 任一批次被领总量不得超过到货量。
	for id, q := range perBatch {
		if q > perSupplier {
			t.Fatalf("批次 %d 被领总量 %d 超过到货量 %d", id, q, perSupplier)
		}
	}
	// 继续领用直到库存不足：最终领用总量必等于到货总量。
	for {
		lines, err := s.Draw(item, 1, 100)
		if err != nil {
			if err != ErrInsufficientStock {
				t.Fatalf("库存耗尽后应报库存不足，得到 %v", err)
			}
			break
		}
		drawnTotal += lines[0].Qty
	}
	if drawnTotal != suppliers*perSupplier {
		t.Fatalf("最终领用总量应等于到货总量 %d，得到 %d", suppliers*perSupplier, drawnTotal)
	}
}

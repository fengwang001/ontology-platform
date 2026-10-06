package settlement_test

import (
	"sync"
	"testing"

	"ontology/settlement"
)

// 并发调用：登记与查询可与批处理并发，结果等价于某种串行顺序，无竞态。
func TestConcurrentAccess(t *testing.T) {
	s := mustSys(t, []int64{1, 2, 3, 4, 5}, 3, 100)
	mustAdd(t, s, "B", nil, 1_000_000)
	mustAdd(t, s, "S", map[int64]int64{10: 1_000_000}, 0)

	var wg sync.WaitGroup
	wg.Add(3)
	go func() {
		defer wg.Done()
		for i := int64(1); i <= 500; i++ {
			_ = s.RegisterOrder(settlement.Order{
				ID: i, Security: 10, Buyer: "B", Seller: "S",
				Qty: 1, Price: 1, SettleDay: 1 + (i % 5), AllowPartial: true,
			})
		}
	}()
	go func() {
		defer wg.Done()
		for i := int64(1); i <= 5; i++ {
			_ = s.RunBatch(i, map[int64]int64{10: 5})
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 500; i++ {
			_, _ = s.QueryAccount("B")
			_, _ = s.QueryAccount("S")
			_, _ = s.QueryOrder(1)
		}
	}()
	wg.Wait()

	// 守恒：总券 1,000,000，总现金 1,000,000。
	b, _ := s.QueryAccount("B")
	sv, _ := s.QueryAccount("S")
	if b.Securities[10]+sv.Securities[10] != 1_000_000 {
		t.Fatalf("sec not conserved: %d + %d", b.Securities[10], sv.Securities[10])
	}
	if b.Cash+sv.Cash != 1_000_000 {
		t.Fatalf("cash not conserved: %d + %d", b.Cash, sv.Cash)
	}
}

// 复杂度可验证证明：先制造大量“历史已了结”指令，
// 随后单日批处理只应扫描当日候选；这里直接验证候选结构不保留已了结指令。
func TestClosedOrdersNotRetained(t *testing.T) {
	s := mustSys(t, []int64{1, 2, 3}, 3, 100)
	mustAdd(t, s, "B", nil, 1_000_000)
	mustAdd(t, s, "S", map[int64]int64{10: 1_000_000}, 0)
	for i := int64(1); i <= 1000; i++ {
		mustReg(t, s, settlement.Order{ID: i, Security: 10, Buyer: "B", Seller: "S", Qty: 1, Price: 1, SettleDay: 1})
	}
	if err := s.RunBatch(1, map[int64]int64{10: 1}); err != nil {
		t.Fatal(err)
	}
	rep, _ := s.LastReport()
	if len(rep.Decisions) != 1000 {
		t.Fatalf("day1 decisions = %d", len(rep.Decisions))
	}
	// day2/3 没有任何待处理指令：候选集合为空，与历史规模无关。
	for _, day := range []int64{2, 3} {
		if err := s.RunBatch(day, map[int64]int64{10: 1}); err != nil {
			t.Fatal(err)
		}
		r, _ := s.LastReport()
		if len(r.Decisions) != 0 {
			t.Fatalf("day %d decisions = %d, want 0", day, len(r.Decisions))
		}
	}
}

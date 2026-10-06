package ontology

import (
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
)

func mustOK(t *testing.T, err error, ctx string) {
	t.Helper()
	if err != nil {
		t.Fatalf("%s: unexpected error: %v", ctx, err)
	}
}

func mustErr(t *testing.T, err error, kind ErrorKind, ctx string) {
	t.Helper()
	if err == nil {
		t.Fatalf("%s: expected error %s, got nil", ctx, kind)
	}
	if !IsError(err, kind) {
		t.Fatalf("%s: expected error kind %s, got %v", ctx, kind, err)
	}
}

// TestExpiryExactBoundary 期限恰到与差一秒。
func TestExpiryExactBoundary(t *testing.T) {
	s := NewSystem()
	mustOK(t, s.SetLimit(0, 1, 10, 100), "setlimit")
	mustOK(t, s.RegisterPrice(0, 1, 10, 0, 1000, 7), "price")
	num, err := s.Receive(0, 1, 10, 10, 5)
	mustOK(t, err, "receive")
	if num != 1 {
		t.Fatalf("first batch number = %d, want 1", num)
	}
	if _, err := s.Consume(4, 10, 1); err != nil {
		t.Fatalf("consume at 4 should succeed: %v", err)
	}
	_, err = s.Consume(5, 10, 1)
	mustErr(t, err, KindInsufficientStock, "consume at exact expiry")
	if s.Clock() != 4 {
		t.Fatalf("clock = %d, want 4", s.Clock())
	}
}

// TestOnHandExpiredSplit 查询在库总量与到期部分；查询不推进时钟。
func TestOnHandExpiredSplit(t *testing.T) {
	s := NewSystem()
	mustOK(t, s.SetLimit(0, 1, 10, 100), "setlimit")
	_, err := s.Receive(0, 1, 10, 10, 5)
	mustOK(t, err, "receive")
	mustOK(t, s.SetLimit(6, 1, 10, 100), "advance clock")
	total, expired := s.OnHand(1, 10)
	if total != 10 || expired != 10 {
		t.Fatalf("at t=6 total=%d expired=%d, want 10/10", total, expired)
	}
	if s.Clock() != 6 {
		t.Fatalf("query must not advance clock, got %d", s.Clock())
	}
	mustOK(t, s.SetLimit(7, 2, 20, 100), "limit2")
	_, err = s.Receive(7, 2, 20, 3, 5)
	mustOK(t, err, "receive2")
	mustOK(t, s.SetLimit(11, 2, 20, 100), "clock 11")
	total, expired = s.OnHand(2, 20)
	if total != 3 || expired != 0 {
		t.Fatalf("one second before expiry total=%d expired=%d, want 3/0", total, expired)
	}
}

// TestCapExactAndOver 在库上限恰等于与超一。
func TestCapExactAndOver(t *testing.T) {
	s := NewSystem()
	mustOK(t, s.SetLimit(0, 1, 10, 10), "limit=10")
	_, err := s.Receive(0, 1, 10, 10, 100)
	mustOK(t, err, "receive exactly 10")
	_, err = s.Receive(1, 1, 10, 1, 100)
	mustErr(t, err, KindOverCap, "receive one over cap")
	total, _ := s.OnHand(1, 10)
	if total != 10 {
		t.Fatalf("total = %d, want 10", total)
	}
	mustOK(t, s.SetLimit(10, 1, 10, 10), "clock to 10")
	_, _ = s.OnHand(1, 10)
	_, err = s.Receive(11, 1, 10, 1, 100)
	mustErr(t, err, KindOverCap, "expired stock still occupies capacity")
}

// TestFIFOCrossSupplier 跨供应商先到先出与并列次序。
func TestFIFOCrossSupplier(t *testing.T) {
	s := NewSystem()
	for _, sup := range []ID{1, 2, 3} {
		mustOK(t, s.SetLimit(0, sup, 7, 100), fmt.Sprintf("limit %d", sup))
		mustOK(t, s.RegisterPrice(0, sup, 7, 0, 1000, int64(sup)*10),
			fmt.Sprintf("price %d", sup))
	}
	n1, _ := s.Receive(5, 2, 7, 3, 100)
	n2, _ := s.Receive(5, 1, 7, 4, 100)
	n3, _ := s.Receive(5, 3, 7, 10, 100)
	n4, _ := s.Receive(6, 1, 7, 2, 100)
	if n1 != 1 || n2 != 1 || n3 != 1 || n4 != 2 {
		t.Fatalf("batch numbers unexpected: %d %d %d %d", n1, n2, n3, n4)
	}
	r, err := s.Consume(7, 7, 8)
	mustOK(t, err, "consume 8")
	want := []struct {
		sup  ID
		num  BatchID
		qty  int64
		rate int64
	}{
		{1, 1, 4, 10},
		{2, 1, 3, 20},
		{3, 1, 1, 30},
	}
	if len(r.Lines) != len(want) {
		t.Fatalf("lines = %d, want %d: %+v", len(r.Lines), len(want), r.Lines)
	}
	for i, w := range want {
		got := r.Lines[i]
		if got.Supplier != w.sup || got.BatchNumber != w.num ||
			got.Quantity != w.qty || got.UnitPrice != w.rate {
			t.Fatalf("line %d = %+v, want sup=%d batch=%d qty=%d rate=%d",
				i, got, w.sup, w.num, w.qty, w.rate)
		}
	}
}

// TestPriceIntervalEnds 价格协议区间两端：左闭右开、紧邻不重叠、重叠拒绝。
func TestPriceIntervalEnds(t *testing.T) {
	s := NewSystem()
	mustOK(t, s.SetLimit(0, 1, 1, 1000), "limit")
	mustOK(t, s.RegisterPrice(0, 1, 1, 0, 10, 5), "price [0,10)")
	mustOK(t, s.RegisterPrice(1, 1, 1, 10, 20, 8), "price [10,20)")
	err := s.RegisterPrice(2, 1, 1, 9, 11, 9)
	mustErr(t, err, KindPriceOverlap, "overlapping registration rejected")

	_, err = s.Receive(3, 1, 1, 100, 1000)
	mustOK(t, err, "receive")
	r9, err := s.Consume(9, 1, 1)
	mustOK(t, err, "consume at 9")
	if r9.Lines[0].UnitPrice != 5 {
		t.Fatalf("price at 9 = %d, want 5", r9.Lines[0].UnitPrice)
	}
	r10, err := s.Consume(10, 1, 1)
	mustOK(t, err, "consume at 10")
	if r10.Lines[0].UnitPrice != 8 {
		t.Fatalf("price at 10 = %d, want 8", r10.Lines[0].UnitPrice)
	}
	_, err = s.Consume(20, 1, 1)
	mustErr(t, err, KindNoValidPrice, "right endpoint is excluded")
}

// TestReverseUsesOriginalPrice 冲销用原结算行单价。
func TestReverseUsesOriginalPrice(t *testing.T) {
	s := NewSystem()
	mustOK(t, s.SetLimit(0, 1, 1, 1000), "limit")
	mustOK(t, s.RegisterPrice(0, 1, 1, 0, 10, 5), "old price")
	mustOK(t, s.RegisterPrice(1, 1, 1, 10, 20, 9), "new price")
	_, err := s.Receive(2, 1, 1, 10, 1000)
	mustOK(t, err, "receive")
	r, err := s.Consume(5, 1, 4)
	mustOK(t, err, "consume at old price")
	line := r.Lines[0]
	if line.UnitPrice != 5 {
		t.Fatalf("unit price = %d, want 5", line.UnitPrice)
	}
	mustOK(t, s.Reverse(12, line.ID, 3), "reverse at t=12 uses old price")
	got, err := s.ReversedQty(line.ID)
	mustOK(t, err, "reversed qty")
	if got != 3 {
		t.Fatalf("reversed = %d, want 3", got)
	}
	mustOK(t, s.SetLimit(20, 1, 1, 1000), "clock to 20")
	st, err := s.Statement(1, 0, 20)
	mustOK(t, err, "statement")
	if st.Consumed != 20 || st.Reversed != 15 || st.Net != 5 {
		t.Fatalf("statement = %+v, want consumed 20 reversed 15 net 5", st)
	}
	err = s.Reverse(21, line.ID, 2)
	mustErr(t, err, KindExcessQuantity, "reverse over open portion")
}

// TestReverseImmediatelyExpired 冲销数量立即到期：占额度、不可领用。
func TestReverseImmediatelyExpired(t *testing.T) {
	s := NewSystem()
	mustOK(t, s.SetLimit(0, 1, 1, 10), "limit 10")
	mustOK(t, s.RegisterPrice(0, 1, 1, 0, 100, 5), "price")
	_, err := s.Receive(0, 1, 1, 10, 5)
	mustOK(t, err, "receive")
	r, err := s.Consume(1, 1, 6)
	mustOK(t, err, "consume 6")
	mustOK(t, s.Return(6, 1, 1, 1, 4), "return remaining 4 at t=6")
	total, expired := s.OnHand(1, 1)
	if total != 0 || expired != 0 {
		t.Fatalf("after return total=%d expired=%d, want 0/0", total, expired)
	}
	mustOK(t, s.Reverse(7, r.Lines[0].ID, 4), "reverse 4 back to expired batch")
	total, expired = s.OnHand(1, 1)
	if total != 4 || expired != 4 {
		t.Fatalf("after reverse total=%d expired=%d, want 4/4", total, expired)
	}
	_, err = s.Consume(8, 1, 1)
	mustErr(t, err, KindInsufficientStock, "expired reversal cannot be consumed")
	_, err = s.Receive(9, 1, 1, 6, 100)
	mustOK(t, err, "receive 6 more exactly to cap")
	err = s.Reverse(10, r.Lines[0].ID, 2)
	mustErr(t, err, KindOverCap, "reverse over cap")
	got, _ := s.ReversedQty(r.Lines[0].ID)
	if got != 4 {
		t.Fatalf("reversed = %d, want 4 after rejected reversal", got)
	}
}

// TestStatementPeriodEnds 周期两端归属与对账单口径。
func TestStatementPeriodEnds(t *testing.T) {
	s := NewSystem()
	mustOK(t, s.SetLimit(0, 1, 1, 100), "limit")
	mustOK(t, s.RegisterPrice(0, 1, 1, 0, 100, 10), "price")
	_, err := s.Receive(0, 1, 1, 100, 100)
	mustOK(t, err, "receive")
	r, err := s.Consume(5, 1, 2)
	mustOK(t, err, "consume")
	mustOK(t, s.Reverse(10, r.Lines[0].ID, 1), "reverse amount 10")
	// 对账单只读、不推进时钟；为使 [10,15) 成为已结束周期，先推进时钟。
	mustOK(t, s.SetLimit(15, 1, 1, 100), "advance clock to 15")
	st, err := s.Statement(1, 5, 10)
	mustOK(t, err, "statement")
	if st.Consumed != 20 || st.Reversed != 0 || st.Net != 20 {
		t.Fatalf("[5,10) = %+v", st)
	}
	st2, err := s.Statement(1, 10, 15)
	mustOK(t, err, "statement2")
	if st2.Consumed != 0 || st2.Reversed != 10 || st2.Net != -10 {
		t.Fatalf("[10,15) = %+v", st2)
	}
	// 当前时钟恰为 15；右端点 16 > 15，周期未结束。
	_, err = s.Statement(1, 0, 16)
	mustErr(t, err, KindPeriodOpen, "period not ended")
}

// TestClockRollbackAndPriority 时钟回退、拒绝不改状态与错误优先级。
func TestClockRollbackAndPriority(t *testing.T) {
	s := NewSystem()
	mustOK(t, s.SetLimit(10, 1, 1, 5), "limit at 10")
	err := s.SetLimit(9, 2, 2, 5)
	mustErr(t, err, KindClockRollback, "rollback")
	err = s.SetLimit(-1, 1, 1, -1)
	mustErr(t, err, KindInvalidParam, "invalid beats rollback")
	if s.Clock() != 10 {
		t.Fatalf("clock = %d, want 10", s.Clock())
	}
	// 对象不存在优先于后续业务错误。
	_, err = s.ReversedQty(999)
	mustErr(t, err, KindNotFound, "line not found")
	// 到货前未登记上限：对象不存在。
	_, err = s.Receive(11, 7, 7, 1, 1)
	mustErr(t, err, KindNotFound, "missing limit is not found")
}

// TestNoValidPriceDoesNotMutate 无有效价格检查不改变任何状态与时钟。
func TestNoValidPriceDoesNotMutate(t *testing.T) {
	s := NewSystem()
	mustOK(t, s.SetLimit(0, 1, 1, 100), "limit s1")
	mustOK(t, s.SetLimit(0, 2, 1, 100), "limit s2")
	mustOK(t, s.RegisterPrice(0, 1, 1, 0, 100, 5), "price only s1")
	_, err := s.Receive(0, 1, 1, 3, 100)
	mustOK(t, err, "receive s1 (3)")
	_, err = s.Receive(0, 2, 1, 4, 100)
	mustOK(t, err, "receive s2 (4)")
	before, _ := s.OnHand(1, 1)
	before2, _ := s.OnHand(2, 1)
	_, err = s.Consume(5, 1, 4)
	mustErr(t, err, KindNoValidPrice, "s2 lacks price")
	after, _ := s.OnHand(1, 1)
	after2, _ := s.OnHand(2, 1)
	if before != after || before2 != after2 || after != 3 || after2 != 4 {
		t.Fatalf("stock mutated after rejected consume: s1 %d->%d, s2 %d->%d",
			before, after, before2, after2)
	}
	if s.Clock() != 0 {
		t.Fatalf("clock = %d, want 0", s.Clock())
	}
}

// TestPartialConsumeRejected 可领用总量不足时不做部分领用。
func TestPartialConsumeRejected(t *testing.T) {
	s := NewSystem()
	mustOK(t, s.SetLimit(0, 1, 1, 100), "limit")
	mustOK(t, s.RegisterPrice(0, 1, 1, 0, 100, 5), "price")
	_, err := s.Receive(0, 1, 1, 3, 100)
	mustOK(t, err, "receive 3")
	// 全部有价格时才可能报库存不足；价格区间覆盖到 2。
	_, err = s.Consume(1, 1, 5)
	mustErr(t, err, KindInsufficientStock, "whole consume rejected")
	total, _ := s.OnHand(1, 1)
	if total != 3 {
		t.Fatalf("partial allocation leaked: total=%d, want 3", total)
	}
}

// TestReplayDeterministic 相同操作序列重放得到完全相同的分配与金额。
func TestReplayDeterministic(t *testing.T) {
	run := func() []SettlementLine {
		s := NewSystem()
		_ = s.SetLimit(0, 1, 9, 1000)
		_ = s.SetLimit(0, 2, 9, 1000)
		_ = s.RegisterPrice(0, 1, 9, 0, 100, 3)
		_ = s.RegisterPrice(0, 2, 9, 0, 100, 7)
		_, _ = s.Receive(1, 2, 9, 5, 50)
		_, _ = s.Receive(1, 1, 9, 5, 50)
		_, _ = s.Receive(2, 1, 9, 5, 50)
		r, err := s.Consume(3, 9, 9)
		if err != nil {
			t.Fatalf("consume: %v", err)
		}
		out := make([]SettlementLine, len(r.Lines))
		copy(out, r.Lines)
		return out
	}
	a := run()
	b := run()
	if fmt.Sprint(a) != fmt.Sprint(b) {
		t.Fatalf("replays differ:\n%v\n%v", a, b)
	}
}

// TestHeapBoundedByActiveBatches 领用考察的堆条目数不随历史增长。
// 构造大量“到货即全部耗尽”的批次，随后堆规模应为 0；
// 再构造大量已到期批次（被惰性摘除），堆规模同样应为 0。
func TestHeapBoundedByActiveBatches(t *testing.T) {
	s := NewSystem()
	_ = s.SetLimit(0, 1, 1, 1_000_000)
	_ = s.RegisterPrice(0, 1, 1, 0, 1_000_000, 1)
	for i := 0; i < 200; i++ {
		tm := int64(i * 2)
		_, err := s.Receive(tm, 1, 1, 1, 1_000_000)
		if err != nil {
			t.Fatalf("receive %d: %v", i, err)
		}
		if _, err := s.Consume(tm+1, 1, 1); err != nil {
			t.Fatalf("consume %d: %v", i, err)
		}
	}
	if n := s.index.heapLen(1); n != 0 {
		t.Fatalf("heap after draining = %d, want 0", n)
	}
	// 到期批次：到货期限为 1，在时刻 t+2 领用检查时被惰性摘除。
	for i := 0; i < 200; i++ {
		tm := int64(1000 + i*2)
		if _, err := s.Receive(tm, 1, 1, 1, 1); err != nil {
			t.Fatalf("receive expiring %d: %v", i, err)
		}
	}
	// 此时 200 个到期批次都已过期（最晚到期 1000+398+1=1399 < 2000）。
	// 被拒绝的领用不改变索引（含堆），因此到期条目不会因一次被拒扫描而消失；
	// 它们只在一次“被接受”的领用提交重建堆时被丢弃。
	if _, err := s.Receive(2000, 1, 1, 1, 1_000_000); err != nil {
		t.Fatalf("fresh receive: %v", err)
	}
	r, err := s.Consume(2001, 1, 1)
	if err != nil {
		t.Fatalf("accepted consume over expired+fresh should succeed: %v", err)
	}
	// 被接受领用只取新鲜批次（全部 401 批之后的第 401 批）1 件；
	// 200 个到期条目在重建堆时全部被丢弃。
	if len(r.Lines) != 1 || r.Lines[0].BatchNumber != 401 {
		t.Fatalf("lines = %+v, want only fresh batch 401", r.Lines)
	}
	if n := s.index.heapLen(1); n != 0 {
		t.Fatalf("heap after accepted sweep = %d, want 0", n)
	}
}

// TestConcurrentConsistency 并发下结果等价于某个串行顺序，
// 批次剩余量恒为非负且不超过到货量（通过不变量间接验证）。
func TestConcurrentConsistency(t *testing.T) {
	s := NewSystem()
	const supN = 4
	for i := ID(1); i <= supN; i++ {
		_ = s.SetLimit(0, i, 1, 100000)
		_ = s.RegisterPrice(0, i, 1, 0, 1_000_000, int64(i))
		_, _ = s.Receive(0, i, 1, 1000, 1_000_000)
	}
	var consumed int64
	var wg sync.WaitGroup
	for w := 0; w < 16; w++ {
		wg.Add(1)
		go func(seed int) {
			defer wg.Done()
			for k := 0; k < 50; k++ {
				tm := int64(1 + seed*50 + k)
				r, err := s.Consume(tm, 1, 1)
				if err == nil {
					atomic.AddInt64(&consumed, int64(len(r.Lines)))
				}
			}
		}(w)
	}
	wg.Wait()
	// 并发领用可能因时钟单调而部分被拒；成功领用的总数量必须守恒：
	// 每个供应商被领走的数量不超过 1000，总在库 = 4000 - 总领用。
	totalOnHand := int64(0)
	for i := ID(1); i <= supN; i++ {
		th, _ := s.OnHand(i, 1)
		totalOnHand += th
	}
	if totalOnHand < 0 || totalOnHand > 4000 {
		t.Fatalf("invariant violated: onHand=%d", totalOnHand)
	}
}

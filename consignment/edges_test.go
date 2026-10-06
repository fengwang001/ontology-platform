package consignment

import (
	"errors"
	"testing"
)

// TestPriceIntervalEndpoints 验证价格协议区间两端：左闭右开。
func TestPriceIntervalEndpoints(t *testing.T) {
	s := New()
	mustPrice(t, s, 1, 10, 100, 200, 7, 0)
	mustArrive(t, s, 1, 10, 4, 10000, 0)

	// 左端点之前（t=99）：无有效价格。
	if _, err := s.Draw(10, 1, 99); !errors.Is(err, ErrNoValidPrice) {
		t.Fatalf("t=99 应报无有效价格，得到 %v", err)
	}
	// 恰在左端点（t=100）：生效。
	lines := mustDraw(t, s, 10, 1, 100)
	if lines[0].UnitPrice != 7 {
		t.Fatalf("t=100 应命中单价 7，得到 %d", lines[0].UnitPrice)
	}
	// 恰在右端点（t=200）：失效。
	if _, err := s.Draw(10, 1, 200); !errors.Is(err, ErrNoValidPrice) {
		t.Fatalf("t=200 应报无有效价格，得到 %v", err)
	}
	// 无有效价格的拒绝不改变任何状态：库存应保持 3。
	if total, _ := s.OnHand(1, 10); total != 3 {
		t.Fatalf("无有效价格的拒绝不得改变状态，在库应为 3，得到 %d", total)
	}
}

// TestPriceOverlapRejected 验证同一供应商同一商品的协议区间不得重叠。
func TestPriceOverlapRejected(t *testing.T) {
	s := New()
	mustPrice(t, s, 1, 10, 100, 200, 7, 0)
	cases := []struct{ from, to int64 }{
		{50, 150},  // 右半重叠
		{150, 250}, // 左半重叠
		{100, 200}, // 完全相同
		{120, 180}, // 被包含
		{50, 250},  // 包含
	}
	for _, c := range cases {
		if err := s.AddPrice(1, 10, c.from, c.to, 9, 1); !errors.Is(err, ErrOverlappingAgreement) {
			t.Fatalf("区间 [%d,%d) 应报重叠，得到 %v", c.from, c.to, err)
		}
	}
	// 首尾相接不重叠：允许。
	if err := s.AddPrice(1, 10, 200, 300, 9, 1); err != nil {
		t.Fatalf("首尾相接应允许，得到 %v", err)
	}
	if err := s.AddPrice(1, 10, 0, 100, 9, 1); err != nil {
		t.Fatalf("首尾相接应允许，得到 %v", err)
	}
	// 不同供应商或不同商品互不影响。
	if err := s.AddPrice(2, 10, 100, 200, 9, 1); err != nil {
		t.Fatalf("不同供应商互不影响，得到 %v", err)
	}
	if err := s.AddPrice(1, 11, 100, 200, 9, 1); err != nil {
		t.Fatalf("不同商品互不影响，得到 %v", err)
	}
}

// TestReversalUsesOriginalPrice 验证冲销按原结算行单价计金额，不用冲销时刻价格。
func TestReversalUsesOriginalPrice(t *testing.T) {
	s := New()
	mustPrice(t, s, 1, 10, 0, 100, 5, 0)
	mustPrice(t, s, 1, 10, 100, 200, 50, 0) // 后续单价 50
	mustArrive(t, s, 1, 10, 10, 10000, 0)

	lines := mustDraw(t, s, 10, 6, 50) // 领用时单价 5
	line := lines[0]
	if line.UnitPrice != 5 || line.Amount != 30 {
		t.Fatalf("结算行应为单价 5 金额 30，得到 %+v", line)
	}
	// 在单价已变为 50 的时刻冲销 4：金额仍按 5 计。
	if err := s.Reverse(line.ID, 4, 150); err != nil {
		t.Fatalf("冲销失败: %v", err)
	}
	// 推进时钟到 151，使包含 t=150 冲销的周期 [0,151) 已结束。
	if err := s.SetCap(1, 10, 1000, 151); err != nil {
		t.Fatal(err)
	}
	st, err := s.Statement(1, 0, 151)
	if err != nil {
		t.Fatal(err)
	}
	if st.DrawTotal != 30 || st.ReversalTotal != 20 || st.Net != 10 {
		t.Fatalf("冲销应按原价 5 计 20，得到 %+v", st)
	}
	q, err := s.ReversedQty(line.ID)
	if err != nil || q != 4 {
		t.Fatalf("已冲销数量应为 4，得到 %d, %v", q, err)
	}
}

// TestReversalImmediatelyExpired 验证冲销回到原批次的数量可能立即到期：
// 批次到期仍按其原到货时刻判定，回到库存的数量占库位但不可领用。
func TestReversalImmediatelyExpired(t *testing.T) {
	s := New()
	mustPrice(t, s, 1, 10, 0, 1000, 5, 0)
	b := mustArrive(t, s, 1, 10, 10, 100, 0) // 到期时刻 100
	lines := mustDraw(t, s, 10, 6, 50)
	line := lines[0]

	// t=200 时批次早已到期；冲销 4 仍被接受，数量回到原批次。
	if err := s.Reverse(line.ID, 4, 200); err != nil {
		t.Fatalf("到期批次的冲销应被接受: %v", err)
	}
	total, expired := s.OnHand(1, 10)
	if total != 8 || expired != 8 {
		t.Fatalf("在库 8 应全部到期（4 未领 + 4 冲销回库），得到 total=%d expired=%d", total, expired)
	}
	// 回到库存的到期数量不可再被领用。
	if _, err := s.Draw(10, 1, 201); !errors.Is(err, ErrInsufficientStock) {
		t.Fatalf("到期数量不可领用，应报库存不足，得到 %v", err)
	}
	// 原批次剩余量 = 4 未领 + 4 冲销回库 = 8，可由退回移出。
	if err := s.Return(b, 8, 202); err != nil {
		t.Fatalf("退回失败: %v", err)
	}
	if total, _ := s.OnHand(1, 10); total != 0 {
		t.Fatalf("退回后在库应为 0，得到 %d", total)
	}
}

// TestReversalCapAndOverAmount 验证冲销的超上限与过量判定及优先级。
func TestReversalCapAndOverAmount(t *testing.T) {
	s := New()
	if err := s.SetCap(1, 10, 10, 0); err != nil {
		t.Fatal(err)
	}
	mustPrice(t, s, 1, 10, 0, 1000, 5, 0)
	mustArrive(t, s, 1, 10, 10, 1000, 0)
	lines := mustDraw(t, s, 10, 6, 10) // 在库降至 4
	line := lines[0]

	// 冲销 7：在库 4 + 7 = 11 > 10 超上限；同时 7 > 6 也过量。
	// 优先级：超上限先于过量类。
	if err := s.Reverse(line.ID, 7, 20); !errors.Is(err, ErrOverCap) {
		t.Fatalf("超上限应优先于过量类，得到 %v", err)
	}
	// 冲销 6：恰回到上限 10，允许。
	if err := s.Reverse(line.ID, 6, 21); err != nil {
		t.Fatalf("恰等于上限应允许: %v", err)
	}
	// 放宽上限后再冲销 1：行内已无可冲销部分，报过量。
	if err := s.SetCap(1, 10, 100, 22); err != nil {
		t.Fatal(err)
	}
	if err := s.Reverse(line.ID, 1, 23); !errors.Is(err, ErrOverAmount) {
		t.Fatalf("超出可冲销部分应报过量，得到 %v", err)
	}
	// 被拒绝的冲销不改变任何状态。
	if q, _ := s.ReversedQty(line.ID); q != 6 {
		t.Fatalf("被拒绝的冲销不得改变状态，已冲销应为 6，得到 %d", q)
	}
}

// TestStatementPeriodBoundary 验证对账周期两端归属：
// 恰在左端点归属本周期，恰在右端点归属下一周期。
func TestStatementPeriodBoundary(t *testing.T) {
	s := New()
	mustPrice(t, s, 1, 10, 0, 1000, 10, 0)
	mustArrive(t, s, 1, 10, 100, 10000, 0)
	mustDraw(t, s, 10, 1, 99)          // 周期 [100,200) 之外（左侧）
	left := mustDraw(t, s, 10, 2, 100) // 恰在左端点，归属本周期
	mustDraw(t, s, 10, 3, 150)         // 周期内
	mustDraw(t, s, 10, 4, 200)         // 恰在右端点，归属下一周期

	// 周期未结束：右端点大于当前时钟（200）。
	if _, err := s.Statement(1, 100, 201); !errors.Is(err, ErrPeriodNotEnded) {
		t.Fatalf("右端点超过当前时钟应报周期未结束，得到 %v", err)
	}
	st, err := s.Statement(1, 100, 200)
	if err != nil {
		t.Fatal(err)
	}
	// 本周期领用 = 2*10 + 3*10 = 50；t=99 与 t=200 的均不属于本周期。
	if st.DrawTotal != 50 || st.Net != 50 {
		t.Fatalf("周期 [100,200) 领用合计应为 50，得到 %+v", st)
	}
	// 冲销按冲销时刻归属周期：t=250 冲销 t=100 的结算行，计入 [200,300)。
	if err := s.Reverse(left[0].ID, 2, 250); err != nil {
		t.Fatal(err)
	}
	// 推进时钟使 [200,300) 结束。
	if err := s.SetCap(1, 10, 1000, 300); err != nil {
		t.Fatal(err)
	}
	st, err = s.Statement(1, 200, 300)
	if err != nil {
		t.Fatal(err)
	}
	if st.ReversalTotal != 20 || st.Net != 20 {
		t.Fatalf("冲销应按其发生时刻计入 [200,300)，得到 %+v", st)
	}
	if st.DrawTotal != 40 {
		t.Fatalf("t=200 的领用应归属下一周期，得到 %+v", st)
	}
	// 原周期 [100,200) 不受冲销影响。
	st, err = s.Statement(1, 100, 200)
	if err != nil {
		t.Fatal(err)
	}
	if st.DrawTotal != 50 || st.ReversalTotal != 0 || st.Net != 50 {
		t.Fatalf("原周期不受后续冲销影响，得到 %+v", st)
	}
}

// TestClockRewindAndRejection 验证时钟回退与被拒绝操作不改变状态与时钟。
func TestClockRewindAndRejection(t *testing.T) {
	s := New()
	mustPrice(t, s, 1, 10, 0, 1000, 5, 0)
	mustArrive(t, s, 1, 10, 10, 1000, 100)
	if s.Now() != 100 {
		t.Fatalf("时钟应为 100，得到 %d", s.Now())
	}
	// 时钟回退：t=99 < 100。
	if _, err := s.Arrive(1, 10, 1, 1000, 99); !errors.Is(err, ErrClockRewind) {
		t.Fatalf("应报时钟回退，得到 %v", err)
	}
	if err := s.Return(1, 1, 50); !errors.Is(err, ErrClockRewind) {
		t.Fatalf("退回应报时钟回退，得到 %v", err)
	}
	if _, err := s.Draw(10, 1, 50); !errors.Is(err, ErrClockRewind) {
		t.Fatalf("领用应报时钟回退，得到 %v", err)
	}
	if err := s.AddPrice(2, 10, 0, 500, 6, 50); !errors.Is(err, ErrClockRewind) {
		t.Fatalf("登记价格应报时钟回退，得到 %v", err)
	}
	// 被拒绝的操作不推进时钟。
	if s.Now() != 100 {
		t.Fatalf("被拒绝的操作不得推进时钟，得到 %d", s.Now())
	}
	// 参数非法先于时钟回退：数量为 0 且时刻回退，报参数非法。
	if _, err := s.Arrive(1, 10, 0, 1000, 50); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("参数非法应优先于时钟回退，得到 %v", err)
	}
	// 等时刻操作允许（不小于上一次被接受时刻）。
	mustDraw(t, s, 10, 5, 100)
	if s.Now() != 100 {
		t.Fatalf("等时刻操作后时钟仍为 100，得到 %d", s.Now())
	}
	// 查询不推进时钟。
	_, _ = s.OnHand(1, 10)
	_, _ = s.ReversedQty(1)
	_, _ = s.Statement(1, 0, 100)
	if s.Now() != 100 {
		t.Fatalf("查询不得推进时钟，得到 %d", s.Now())
	}
}

// TestNoPriceBeforeInsufficientStock 验证无有效价格优先于库存不足，
// 且该检查不改变任何状态。
func TestNoPriceBeforeInsufficientStock(t *testing.T) {
	s := New()
	mustPrice(t, s, 1, 10, 0, 100, 5, 0) // 供应商 1 仅在 [0,100) 有价
	// 供应商 2 完全无价。
	mustArrive(t, s, 1, 10, 5, 1000, 0)
	mustArrive(t, s, 2, 10, 5, 1000, 0)

	// t=500：供应商 1 价格已失效，供应商 2 无价；库存 10 充足。
	if _, err := s.Draw(10, 3, 500); !errors.Is(err, ErrNoValidPrice) {
		t.Fatalf("应报无有效价格，得到 %v", err)
	}
	// t=500 领用 20：库存不足且涉及无价供应商，无有效价格优先。
	if _, err := s.Draw(10, 20, 500); !errors.Is(err, ErrNoValidPrice) {
		t.Fatalf("无有效价格应优先于库存不足，得到 %v", err)
	}
	// 状态未被改变：在库仍为 10。
	total1, _ := s.OnHand(1, 10)
	total2, _ := s.OnHand(2, 10)
	if total1 != 5 || total2 != 5 {
		t.Fatalf("被拒绝的领用不得改变状态，得到 total1=%d total2=%d", total1, total2)
	}
	// 时钟未被推进。
	if s.Now() != 0 {
		t.Fatalf("被拒绝的领用不得推进时钟，得到 %d", s.Now())
	}
}

// TestNotFoundAndInvalidParam 验证对象不存在与参数非法。
func TestNotFoundAndInvalidParam(t *testing.T) {
	s := New()
	mustPrice(t, s, 1, 10, 0, 1000, 5, 0)
	b := mustArrive(t, s, 1, 10, 10, 1000, 0)

	if err := s.Return(b+100, 1, 1); !errors.Is(err, ErrNotFound) {
		t.Fatalf("退回不存在的批次应报对象不存在，得到 %v", err)
	}
	if err := s.Reverse(999, 1, 1); !errors.Is(err, ErrNotFound) {
		t.Fatalf("冲销不存在的结算行应报对象不存在，得到 %v", err)
	}
	if _, err := s.ReversedQty(999); !errors.Is(err, ErrNotFound) {
		t.Fatalf("查询不存在的结算行应报对象不存在，得到 %v", err)
	}
	// 对象不存在先于过量类：批次不存在且数量超大，报不存在。
	if err := s.Return(b+100, 1<<40, 1); !errors.Is(err, ErrNotFound) {
		t.Fatalf("对象不存在应优先于过量类，得到 %v", err)
	}
	// 参数非法先于对象不存在：数量为 0 且批次不存在，报参数非法。
	if err := s.Return(b+100, 0, 1); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("参数非法应优先于对象不存在，得到 %v", err)
	}
	// 负时刻、零数量、颠倒区间均为参数非法。
	if _, err := s.Arrive(1, 10, 1, 1000, -1); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("负时刻应报参数非法，得到 %v", err)
	}
	if _, err := s.Draw(10, 0, 1); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("零数量应报参数非法，得到 %v", err)
	}
	if err := s.AddPrice(1, 10, 200, 100, 5, 1); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("颠倒区间应报参数非法，得到 %v", err)
	}
	if _, err := s.Statement(1, 300, 200); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("颠倒周期应报参数非法，得到 %v", err)
	}
}

// TestDrawExaminedComplexity 验证领用考察的批次记录数
// 不随已耗尽批次、已到期批次或历史结算行总数增长。
func TestDrawExaminedComplexity(t *testing.T) {
	s := New()
	mustPrice(t, s, 1, 10, 0, 100000, 1, 0)

	// 制造 500 个已耗尽批次与 500 条历史结算行。
	for i := int64(0); i < 500; i++ {
		mustArrive(t, s, 1, 10, 1, 100000, i)
	}
	mustDraw(t, s, 10, 500, 500) // 全部耗尽，产生 500 条结算行
	// 制造 500 个已到期批次（期限 10，到货 t=501..1000，远早于考察时刻）。
	for i := int64(501); i <= 1000; i++ {
		mustArrive(t, s, 1, 10, 1, 10, i)
	}
	// 再补充 3 个有效批次。
	for i := int64(1001); i <= 1003; i++ {
		mustArrive(t, s, 1, 10, 10, 100000, i)
	}

	base := s.Metrics()
	// 第一次领用：一次性摘除 500 个新到期批次（每批次生命周期内仅此一次），
	// 再考察 1 个有效批次完成分配。
	mustDraw(t, s, 10, 5, 2000)
	m1 := s.Metrics()
	// 分解：领用开始时以当前时钟 1003 物理清理 493 条（到期时刻 <= 1003），
	// 分配阶段逻辑跳过 7 条（到期时刻在 (1003, 2000]），
	// 领用被接受后再物理清理这 7 条。合计 507，且每批次至多被物理清理一次。
	if got := m1.ExpiryScanned - base.ExpiryScanned; got != 507 {
		t.Fatalf("首次领用到期索引扫描应为 507（493 清理 + 7 跳过 + 7 清理），得到 %d", got)
	}
	if got := m1.AllocExamined - base.AllocExamined; got != 1 {
		t.Fatalf("首次领用应只考察 1 个未到期批次，得到 %d", got)
	}
	// 第二次领用：到期批次已被永久摘除，考察记录数与历史规模完全无关。
	mustDraw(t, s, 10, 5, 2001)
	m2 := s.Metrics()
	if got := m2.ExpiryScanned - m1.ExpiryScanned; got != 0 {
		t.Fatalf("第二次领用不应再扫描任何到期索引记录，得到 %d", got)
	}
	if got := m2.AllocExamined - m1.AllocExamined; got != 1 {
		t.Fatalf("第二次领用应只考察 1 个未到期批次，得到 %d", got)
	}
}

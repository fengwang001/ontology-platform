package fee

import (
	"errors"
	"fmt"
	"reflect"
	"sync"
	"testing"
)

func mustCalc(t *testing.T, th []int64, ra []int64, cap, rho int64) *Calculator {
	t.Helper()
	c, err := NewCalculator(th, ra, cap, rho)
	if err != nil {
		t.Fatalf("NewCalculator(%v, %v, %d, %d) 意外失败: %v", th, ra, cap, rho, err)
	}
	return c
}

func mustTrade(t *testing.T, c *Calculator, acct, tid string, amt int64) int64 {
	t.Helper()
	fee, err := c.Trade(acct, tid, amt)
	if err != nil {
		t.Fatalf("Trade(%q, %q, %d) 意外失败: %v", acct, tid, amt, err)
	}
	return fee
}

func mustCancel(t *testing.T, c *Calculator, tid string) (int64, []FeeChange) {
	t.Helper()
	old, changes, err := c.Cancel(tid)
	if err != nil {
		t.Fatalf("Cancel(%q) 意外失败: %v", tid, err)
	}
	return old, changes
}

func mustC0(t *testing.T, c *Calculator, acct string) int64 {
	t.Helper()
	c0, ok := c.C0(acct)
	if !ok {
		t.Fatalf("账户 %q 应已出现", acct)
	}
	return c0
}

func wantErr(t *testing.T, err, want error, what string) {
	t.Helper()
	if !errors.Is(err, want) {
		t.Fatalf("%s: err = %v, 期望 %v", what, err, want)
	}
}

// naiveF 是测试内独立编写的未封顶累计费用 F(c)。
func naiveF(th, ra []int64, cum int64) int64 {
	var total int64
	for k, r := range ra {
		var lo int64
		if k > 0 {
			lo = th[k-1]
		}
		hi := int64(-1)
		if k < len(th) {
			hi = th[k]
		}
		seg := cum - lo
		if seg < 0 {
			seg = 0
		}
		if hi >= 0 && seg > hi-lo {
			seg = hi - lo
		}
		total += seg * r / 10000
	}
	return total
}

func naivePhi(th, ra []int64, cap, cum int64) int64 {
	if f := naiveF(th, ra, cum); f < cap {
		return f
	}
	return cap
}

// 题目给出的完整示例：封顶使第三笔费用为 2 而非 3、两种撤销、结转与新账期。
func TestWorkedExample(t *testing.T) {
	newCalc := func() *Calculator {
		return mustCalc(t, []int64{1000, 5000}, []int64{10, 8, 5}, 3, 5000)
	}
	prefix := func(c *Calculator) {
		t.Helper()
		if fee := mustTrade(t, c, "a", "t1", 900); fee != 0 {
			t.Fatalf("t1 费用 = %d, 期望 0", fee)
		}
		if fee := mustTrade(t, c, "a", "t2", 1300); fee != 1 {
			t.Fatalf("t2 费用 = %d, 期望 1", fee)
		}
		if fee := mustTrade(t, c, "a", "t3", 3000); fee != 2 {
			t.Fatalf("t3 费用 = %d, 期望 2（封顶使其不是 3）", fee)
		}
	}

	// 主线：Cancel(t2) 后 t3 费用 2→3，结转 C0=1950，新账期 t4/t5 与 Cancel(t4)。
	c := newCalc()
	prefix(c)
	old, changes := mustCancel(t, c, "t2")
	if old != 1 {
		t.Fatalf("Cancel(t2) 被撤销费用 = %d, 期望 1", old)
	}
	if want := []FeeChange{{TID: "t3", OldFee: 2, NewFee: 3}}; !reflect.DeepEqual(changes, want) {
		t.Fatalf("Cancel(t2) 变更列表 = %+v, 期望 %+v", changes, want)
	}
	c.NextPeriod()
	if c0 := mustC0(t, c, "a"); c0 != 1950 {
		t.Fatalf("结转后 C0 = %d, 期望 1950", c0)
	}
	if fee := mustTrade(t, c, "a", "t4", 2000); fee != 2 {
		t.Fatalf("t4 费用 = %d, 期望 2", fee)
	}
	if fee := mustTrade(t, c, "a", "t5", 1100); fee != 0 {
		t.Fatalf("t5 费用 = %d, 期望 0", fee)
	}
	old, changes = mustCancel(t, c, "t4")
	if old != 2 {
		t.Fatalf("Cancel(t4) 被撤销费用 = %d, 期望 2", old)
	}
	if want := []FeeChange{{TID: "t5", OldFee: 0, NewFee: 1}}; !reflect.DeepEqual(changes, want) {
		t.Fatalf("Cancel(t4) 变更列表 = %+v, 期望 %+v", changes, want)
	}

	// 支线：Cancel(t1) 时变更列表为空。
	c2 := newCalc()
	prefix(c2)
	old, changes = mustCancel(t, c2, "t1")
	if old != 0 {
		t.Fatalf("Cancel(t1) 被撤销费用 = %d, 期望 0", old)
	}
	if len(changes) != 0 {
		t.Fatalf("Cancel(t1) 变更列表 = %+v, 期望为空", changes)
	}
}

// 累计额恰等于阈值时，下一笔全部落入后一段。
func TestExactThresholdBoundary(t *testing.T) {
	c := mustCalc(t, []int64{1000}, []int64{0, 20}, 1_000_000, 0)
	if fee := mustTrade(t, c, "a", "x1", 1000); fee != 0 {
		t.Fatalf("x1 费用 = %d, 期望 0（累计恰为阈值 1000）", fee)
	}
	if fee := mustTrade(t, c, "a", "x2", 500); fee != 1 {
		t.Fatalf("x2 费用 = %d, 期望 1（500 全部落入第二段：500*20/10000）", fee)
	}
}

// 单笔成交跨两段、跨三段与跨四段。
func TestSingleTradeCrossingSegments(t *testing.T) {
	th := []int64{1000, 5000, 10000}
	ra := []int64{10, 20, 30, 40}
	c := mustCalc(t, th, ra, 1_000_000, 0)
	if fee := mustTrade(t, c, "a", "x1", 3000); fee != 5 {
		t.Fatalf("跨两段费用 = %d, 期望 5", fee)
	}
	c = mustCalc(t, th, ra, 1_000_000, 0)
	if fee := mustTrade(t, c, "a", "x1", 9000); fee != 21 {
		t.Fatalf("跨三段费用 = %d, 期望 21", fee)
	}
	c = mustCalc(t, th, ra, 1_000_000, 0)
	if fee := mustTrade(t, c, "a", "x1", 12000); fee != 32 {
		t.Fatalf("跨四段费用 = %d, 期望 32", fee)
	}
}

// 向下取整使单笔费用为 0，累计后发生进位。
func TestFloorZeroThenCarry(t *testing.T) {
	c := mustCalc(t, []int64{1000}, []int64{10, 0}, 1_000_000, 0)
	if fee := mustTrade(t, c, "a", "x1", 500); fee != 0 {
		t.Fatalf("x1 费用 = %d, 期望 0（floor(500*10/10000)=0）", fee)
	}
	if fee := mustTrade(t, c, "a", "x2", 500); fee != 1 {
		t.Fatalf("x2 费用 = %d, 期望 1（累计 1000 进位）", fee)
	}
}

// 封顶后继续成交费用为 0。
func TestCapReachedFurtherTradesZero(t *testing.T) {
	c := mustCalc(t, []int64{1000}, []int64{100, 100}, 5, 0)
	if fee := mustTrade(t, c, "a", "x1", 2000); fee != 5 {
		t.Fatalf("x1 费用 = %d, 期望 5（封顶）", fee)
	}
	if fee := mustTrade(t, c, "a", "x2", 100); fee != 0 {
		t.Fatalf("x2 费用 = %d, 期望 0", fee)
	}
	if fee := mustTrade(t, c, "a", "x3", 10000); fee != 0 {
		t.Fatalf("x3 费用 = %d, 期望 0", fee)
	}
}

// 起点 C0 的 Φ 已达 CAP 时，本账期全部费用为 0。
func TestC0AlreadyAtCap(t *testing.T) {
	c := mustCalc(t, []int64{1000}, []int64{100, 100}, 5, 10000)
	mustTrade(t, c, "a", "x1", 2000)
	c.NextPeriod()
	if c0 := mustC0(t, c, "a"); c0 != 2000 {
		t.Fatalf("C0 = %d, 期望 2000", c0)
	}
	if fee := mustTrade(t, c, "a", "x2", 500); fee != 0 {
		t.Fatalf("x2 费用 = %d, 期望 0（Φ(C0) 已达 CAP）", fee)
	}
	if fee := mustTrade(t, c, "a", "x3", 600); fee != 0 {
		t.Fatalf("x3 费用 = %d, 期望 0", fee)
	}
}

// CAP 为 0 时全部费用为 0。
func TestCapZero(t *testing.T) {
	c := mustCalc(t, []int64{1000}, []int64{100, 100}, 0, 0)
	if fee := mustTrade(t, c, "a", "x1", 5000); fee != 0 {
		t.Fatalf("费用 = %d, 期望 0（CAP=0）", fee)
	}
}

// 撤销第一笔：后续费用重算，未变的笔不出现在变更列表。
func TestCancelFirst(t *testing.T) {
	c := mustCalc(t, []int64{1000, 5000}, []int64{10, 8, 5}, 1_000_000, 0)
	mustTrade(t, c, "a", "a1", 900)
	mustTrade(t, c, "a", "a2", 1300)
	mustTrade(t, c, "a", "a3", 3000)
	old, changes := mustCancel(t, c, "a1")
	if old != 0 {
		t.Fatalf("被撤销费用 = %d, 期望 0", old)
	}
	// a2 累计 1300 费用仍为 1（不列出）；a3 累计 4300，F=3，费用 3→2。
	if want := []FeeChange{{TID: "a3", OldFee: 3, NewFee: 2}}; !reflect.DeepEqual(changes, want) {
		t.Fatalf("变更列表 = %+v, 期望 %+v", changes, want)
	}
}

// 撤销中间笔。
func TestCancelMiddle(t *testing.T) {
	c := mustCalc(t, []int64{1000, 5000}, []int64{10, 8, 5}, 1_000_000, 0)
	mustTrade(t, c, "a", "b1", 2000)
	mustTrade(t, c, "a", "b2", 2000)
	mustTrade(t, c, "a", "b3", 2000)
	old, changes := mustCancel(t, c, "b2")
	if old != 2 {
		t.Fatalf("被撤销费用 = %d, 期望 2", old)
	}
	// b3 累计变为 4000，F=3，费用 1→2。
	if want := []FeeChange{{TID: "b3", OldFee: 1, NewFee: 2}}; !reflect.DeepEqual(changes, want) {
		t.Fatalf("变更列表 = %+v, 期望 %+v", changes, want)
	}
}

// 撤销最后一笔：无后续成交，变更列表为空。
func TestCancelLast(t *testing.T) {
	c := mustCalc(t, []int64{1000, 5000}, []int64{10, 8, 5}, 1_000_000, 0)
	mustTrade(t, c, "a", "c1", 2000)
	mustTrade(t, c, "a", "c2", 2000)
	mustTrade(t, c, "a", "c3", 2000)
	old, changes := mustCancel(t, c, "c3")
	if old != 1 {
		t.Fatalf("被撤销费用 = %d, 期望 1", old)
	}
	if len(changes) != 0 {
		t.Fatalf("变更列表 = %+v, 期望为空", changes)
	}
}

// 撤销因封顶使后续费用不变：变更列表为空。
func TestCancelUnderCapNoChange(t *testing.T) {
	c := mustCalc(t, []int64{1000}, []int64{10, 10}, 3, 0)
	mustTrade(t, c, "a", "t1", 100)
	mustTrade(t, c, "a", "t2", 10000)
	mustTrade(t, c, "a", "t3", 100)
	old, changes := mustCancel(t, c, "t1")
	if old != 0 {
		t.Fatalf("被撤销费用 = %d, 期望 0", old)
	}
	// t2 累计 10000，F=10，Φ=3，费用仍为 3；t3 费用仍为 0。
	if len(changes) != 0 {
		t.Fatalf("变更列表 = %+v, 期望为空（封顶使后续费用不变）", changes)
	}
}

// 撤销封顶之前的笔，使封顶后的笔费用由 0 变为非 0。
func TestCancelBeforeCapLiftsLaterFee(t *testing.T) {
	c := mustCalc(t, []int64{1000}, []int64{10, 10}, 3, 0)
	mustTrade(t, c, "a", "u1", 5000)
	mustTrade(t, c, "a", "u2", 5000)
	old, changes := mustCancel(t, c, "u1")
	if old != 3 {
		t.Fatalf("被撤销费用 = %d, 期望 3", old)
	}
	if want := []FeeChange{{TID: "u2", OldFee: 0, NewFee: 3}}; !reflect.DeepEqual(changes, want) {
		t.Fatalf("变更列表 = %+v, 期望 %+v", changes, want)
	}
}

// 撤销后各笔费用与把剩余有效成交按原次序从起点 C0 重新登记逐笔相同。
func TestCancelEqualsFreshReplay(t *testing.T) {
	th := []int64{1000, 5000}
	ra := []int64{10, 8, 5}
	setup := func(c *Calculator, ids []string) []int64 {
		t.Helper()
		mustTrade(t, c, "a", ids[0], 900)
		mustTrade(t, c, "a", ids[1], 1300)
		mustTrade(t, c, "a", ids[2], 3000)
		c.NextPeriod() // C0 = floor(5200*5000/10000) = 2600
		fees := []int64{
			mustTrade(t, c, "a", ids[3], 500),
			mustTrade(t, c, "a", ids[4], 1000),
			mustTrade(t, c, "a", ids[5], 2000),
			mustTrade(t, c, "a", ids[6], 500),
		}
		return fees
	}
	c1 := mustCalc(t, th, ra, 1_000_000, 5000)
	setup(c1, []string{"x1", "x2", "x3", "y1", "y2", "y3", "y4"})
	mustCancel(t, c1, "y2")

	// 对照组：相同历史，但第二账期只登记仍有效的成交。
	c3 := mustCalc(t, th, ra, 1_000_000, 5000)
	mustTrade(t, c3, "a", "q1", 900)
	mustTrade(t, c3, "a", "q2", 1300)
	mustTrade(t, c3, "a", "q3", 3000)
	c3.NextPeriod()
	want := []int64{
		mustTrade(t, c3, "a", "w1", 500),
		mustTrade(t, c3, "a", "w3", 2000),
		mustTrade(t, c3, "a", "w4", 500),
	}
	var got []int64
	for _, rec := range c1.Trades("a") {
		if !rec.Cancelled {
			got = append(got, rec.Fee)
		}
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("撤销后费用 %v 与重新登记结果 %v 不一致", got, want)
	}
}

// 已撤销与不存在必须可区分；账期已结单独报错；优先级：已撤销 > 账期已结。
func TestCancelErrorKinds(t *testing.T) {
	c := mustCalc(t, []int64{1000}, []int64{10, 10}, 1_000_000, 0)
	mustTrade(t, c, "a", "k1", 100)
	mustTrade(t, c, "a", "k2", 100)

	_, _, err := c.Cancel("ghost")
	wantErr(t, err, ErrTradeNotFound, "Cancel(不存在)")

	mustCancel(t, c, "k1")
	_, _, err = c.Cancel("k1")
	wantErr(t, err, ErrAlreadyCancelled, "Cancel(已撤销)")

	c.NextPeriod()
	_, _, err = c.Cancel("k2")
	wantErr(t, err, ErrPeriodClosed, "Cancel(旧账期)")

	// 已撤销且账期已结：优先报已撤销。
	_, _, err = c.Cancel("k1")
	wantErr(t, err, ErrAlreadyCancelled, "Cancel(已撤销+旧账期)")
}

// NextPeriod 按向下取整结转；无成交账户逐期复合。
func TestNextPeriodCarryFloorAndCompounding(t *testing.T) {
	c := mustCalc(t, []int64{1000}, []int64{10, 10}, 1_000_000, 3333)
	mustTrade(t, c, "a", "n1", 3999)
	c.NextPeriod()
	if c0 := mustC0(t, c, "a"); c0 != 1332 {
		t.Fatalf("第一次结转 C0 = %d, 期望 floor(3999*3333/10000)=1332", c0)
	}
	// 本账期无成交，仍按原 C0 复合结转。
	c.NextPeriod()
	if c0 := mustC0(t, c, "a"); c0 != 443 {
		t.Fatalf("第二次结转 C0 = %d, 期望 floor(1332*3333/10000)=443", c0)
	}
	if _, ok := c.C0("never-seen"); ok {
		t.Fatal("未出现过的账户不应有 C0")
	}
}

// ρ=0 时结转清零；ρ=10000 时全额结转。
func TestRhoBounds(t *testing.T) {
	c := mustCalc(t, []int64{1000}, []int64{10, 10}, 1_000_000, 0)
	mustTrade(t, c, "a", "r1", 500)
	c.NextPeriod()
	if c0 := mustC0(t, c, "a"); c0 != 0 {
		t.Fatalf("ρ=0 结转后 C0 = %d, 期望 0", c0)
	}

	c = mustCalc(t, []int64{1000}, []int64{10, 10}, 1_000_000, 10000)
	mustTrade(t, c, "a", "r1", 500)
	c.NextPeriod()
	if c0 := mustC0(t, c, "a"); c0 != 500 {
		t.Fatalf("ρ=10000 结转后 C0 = %d, 期望 500", c0)
	}
	// 累计 1000：F = floor(1000*10/10000) = 1，Φ(500)=0，费用 1。
	if fee := mustTrade(t, c, "a", "r2", 500); fee != 1 {
		t.Fatalf("r2 费用 = %d, 期望 1", fee)
	}
}

// 旧账期成交不可撤销；tid 撤销后仍不可重用；tid 跨账户唯一。
func TestTIDUniqueness(t *testing.T) {
	c := mustCalc(t, []int64{1000}, []int64{10, 10}, 1_000_000, 0)
	mustTrade(t, c, "a", "u1", 100)
	mustCancel(t, c, "u1")
	_, err := c.Trade("a", "u1", 100)
	wantErr(t, err, ErrDuplicateTID, "撤销后重用 tid")

	mustTrade(t, c, "b", "u2", 100)
	_, err = c.Trade("a", "u2", 100)
	wantErr(t, err, ErrDuplicateTID, "跨账户重用 tid")

	c.NextPeriod()
	_, err = c.Trade("a", "u2", 100)
	wantErr(t, err, ErrDuplicateTID, "跨账期重用 tid")
}

// 被拒绝的操作不得改变任何状态。
func TestRejectedOpsKeepState(t *testing.T) {
	c := mustCalc(t, []int64{1000}, []int64{10, 10}, 1_000_000, 0)
	if fee := mustTrade(t, c, "a", "v1", 500); fee != 0 {
		t.Fatalf("v1 费用 = %d, 期望 0", fee)
	}
	period := c.Period()

	badTrades := []struct {
		acct, tid string
		amt       int64
		want      error
	}{
		{"", "b1", 100, ErrInvalidParam},
		{"a", "", 100, ErrInvalidParam},
		{"a", "b2", 0, ErrInvalidParam},
		{"a", "b3", -5, ErrInvalidParam},
		{"a", "b4", 1_000_000_001, ErrInvalidParam},
		{"a", "v1", 100, ErrDuplicateTID},
		{"a", "v1", 0, ErrInvalidParam}, // 参数非法优先于编号重复
	}
	for _, bt := range badTrades {
		_, err := c.Trade(bt.acct, bt.tid, bt.amt)
		wantErr(t, err, bt.want, fmt.Sprintf("Trade(%q, %q, %d)", bt.acct, bt.tid, bt.amt))
	}
	if _, _, err := c.Cancel("ghost"); !errors.Is(err, ErrTradeNotFound) {
		t.Fatalf("Cancel(ghost) 应被拒绝: %v", err)
	}

	// 状态未变：后续成交费用与只登记 v1 时一致，账期与 C0 不变。
	if fee := mustTrade(t, c, "a", "v2", 500); fee != 1 {
		t.Fatalf("v2 费用 = %d, 期望 1（被拒绝的操作不得改变状态）", fee)
	}
	if c.Period() != period {
		t.Fatalf("账期变为 %d, 期望 %d", c.Period(), period)
	}
	if c0 := mustC0(t, c, "a"); c0 != 0 {
		t.Fatalf("C0 = %d, 期望 0", c0)
	}
}

// 累计额越限报错；编号重复优先于越限；越限拒绝不改状态。
func TestCumulativeLimit(t *testing.T) {
	c := mustCalc(t, []int64{1000}, []int64{10, 10}, 1_000_000, 10000)
	// 100000 笔 10^9 使累计恰为 10^14，仍合法。
	for i := 0; i < 100000; i++ {
		mustTrade(t, c, "a", fmt.Sprintf("bulk-%d", i), 1_000_000_000)
	}
	// 再登记 1 即越限。
	if _, err := c.Trade("a", "over", 1); !errors.Is(err, ErrCumulativeLimit) {
		t.Fatalf("越限成交 err = %v, 期望 %v", err, ErrCumulativeLimit)
	}
	// 编号重复优先于累计额越限。
	if _, err := c.Trade("a", "bulk-0", 1); !errors.Is(err, ErrDuplicateTID) {
		t.Fatalf("重复+越限 err = %v, 期望 %v", err, ErrDuplicateTID)
	}
	// 参数非法优先于编号重复与越限。
	if _, err := c.Trade("a", "bulk-0", 0); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("非法+重复+越限 err = %v, 期望 %v", err, ErrInvalidParam)
	}
	// 越限拒绝不改状态：结转到下一账期后 C0 = 10^14，仍越限。
	c.NextPeriod()
	if c0 := mustC0(t, c, "a"); c0 != 100_000_000_000_000 {
		t.Fatalf("C0 = %d, 期望 10^14", c0)
	}
	if _, err := c.Trade("a", "over2", 1); !errors.Is(err, ErrCumulativeLimit) {
		t.Fatalf("新账期越限 err = %v, 期望 %v", err, ErrCumulativeLimit)
	}
	if c0 := mustC0(t, c, "a"); c0 != 100_000_000_000_000 {
		t.Fatalf("越限拒绝后 C0 = %d, 期望仍为 10^14", c0)
	}
}

// 构造参数校验：非法整体拒绝，边界值合法。
func TestConstructorValidation(t *testing.T) {
	cases := []struct {
		name    string
		th, ra  []int64
		capRho  [2]int64
		wantErr bool
	}{
		{"空阈值", nil, []int64{1}, [2]int64{0, 0}, true},
		{"费率数量不符", []int64{100}, []int64{1}, [2]int64{0, 0}, true},
		{"阈值非递增-相等", []int64{100, 100}, []int64{1, 1, 1}, [2]int64{0, 0}, true},
		{"阈值非递增-倒退", []int64{200, 100}, []int64{1, 1, 1}, [2]int64{0, 0}, true},
		{"t1小于1", []int64{0}, []int64{1, 1}, [2]int64{0, 0}, true},
		{"tm超过上限", []int64{100_000_000_000_001}, []int64{1, 1}, [2]int64{0, 0}, true},
		{"费率为负", []int64{100}, []int64{-1, 1}, [2]int64{0, 0}, true},
		{"费率超万分比", []int64{100}, []int64{1, 10001}, [2]int64{0, 0}, true},
		{"CAP为负", []int64{100}, []int64{1, 1}, [2]int64{-1, 0}, true},
		{"CAP超上限", []int64{100}, []int64{1, 1}, [2]int64{100_000_000_000_001, 0}, true},
		{"ρ为负", []int64{100}, []int64{1, 1}, [2]int64{0, -1}, true},
		{"ρ超万分比", []int64{100}, []int64{1, 1}, [2]int64{0, 10001}, true},
		{"边界合法", []int64{1, 100_000_000_000_000}, []int64{0, 0, 10000}, [2]int64{0, 0}, false},
		{"最大CAP与ρ", []int64{1}, []int64{10000, 10000}, [2]int64{100_000_000_000_000, 10000}, false},
	}
	for _, tc := range cases {
		_, err := NewCalculator(tc.th, tc.ra, tc.capRho[0], tc.capRho[1])
		if tc.wantErr && !errors.Is(err, ErrInvalidParam) {
			t.Fatalf("%s: err = %v, 期望 ErrInvalidParam", tc.name, err)
		}
		if !tc.wantErr && err != nil {
			t.Fatalf("%s: 意外失败: %v", tc.name, err)
		}
	}
}

// 并发调用等价于某个串行顺序：各 goroutine 只操作自己的账户与 tid，
// 结果确定；NextPeriod 并发执行时每次结转都是原子步骤。
func TestConcurrency(t *testing.T) {
	th := []int64{1000, 5000}
	ra := []int64{10, 8, 5}
	cap := int64(50)
	rho := int64(5000)
	c := mustCalc(t, th, ra, cap, rho)

	const goroutines = 8
	const tradesEach = 200
	const amt = int64(100)

	fees := make([][]int64, goroutines)
	tids := make([][]string, goroutines)
	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			acct := fmt.Sprintf("acct-%d", g)
			for i := 0; i < tradesEach; i++ {
				tid := fmt.Sprintf("g%d-t%d", g, i)
				fee, err := c.Trade(acct, tid, amt)
				if err != nil {
					t.Errorf("Trade(%q, %q, %d): %v", acct, tid, amt, err)
					return
				}
				fees[g] = append(fees[g], fee)
				tids[g] = append(tids[g], tid)
			}
		}(g)
	}
	wg.Wait()

	// 不变式：各账户有效费用之和 = Φ(C0+Σamt) − Φ(C0)，此处 C0=0。
	want := naivePhi(th, ra, cap, tradesEach*amt)
	for g := range fees {
		var sum int64
		for _, f := range fees[g] {
			sum += f
		}
		if sum != want {
			t.Fatalf("账户 acct-%d 费用之和 = %d, 期望 %d", g, sum, want)
		}
	}

	// 并发撤销各自的一半成交，并按变更列表维护本地费用。
	type cancelRes struct {
		idx     int
		old     int64
		changes []FeeChange
	}
	results := make([][]cancelRes, goroutines)
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 1; i < tradesEach; i += 2 {
				old, changes, err := c.Cancel(tids[g][i])
				if err != nil {
					t.Errorf("Cancel(%q): %v", tids[g][i], err)
					return
				}
				results[g] = append(results[g], cancelRes{idx: i, old: old, changes: changes})
			}
		}(g)
	}
	wg.Wait()

	valid := make([][]bool, goroutines)
	for g := 0; g < goroutines; g++ {
		valid[g] = make([]bool, tradesEach)
		for i := range valid[g] {
			valid[g][i] = true
		}
		idxOf := make(map[string]int, tradesEach)
		for i, tid := range tids[g] {
			idxOf[tid] = i
		}
		for _, res := range results[g] {
			if res.old != fees[g][res.idx] {
				t.Fatalf("acct-%d 被撤销费用 = %d, 期望 %d", g, res.old, fees[g][res.idx])
			}
			valid[g][res.idx] = false
			for _, ch := range res.changes {
				i := idxOf[ch.TID]
				if fees[g][i] != ch.OldFee {
					t.Fatalf("acct-%d %s 旧费用 = %d, 变更列表记录 %d", g, ch.TID, fees[g][i], ch.OldFee)
				}
				fees[g][i] = ch.NewFee
			}
		}
		var sum, amtSum int64
		for i := 0; i < tradesEach; i++ {
			if valid[g][i] {
				sum += fees[g][i]
				amtSum += amt
			}
		}
		if want := naivePhi(th, ra, cap, amtSum); sum != want {
			t.Fatalf("撤销后 acct-%d 费用之和 = %d, 期望 %d", g, sum, want)
		}
	}

	// 并发 NextPeriod 与查询：每次结转原子完成。
	const nexts = 4
	for i := 0; i < nexts; i++ {
		wg.Add(2)
		go func() { defer wg.Done(); c.NextPeriod() }()
		go func() {
			defer wg.Done()
			c.Period()
			c.C0("acct-0")
			c.Trades("acct-0")
		}()
	}
	wg.Wait()
	if c.Period() != nexts {
		t.Fatalf("账期 = %d, 期望 %d", c.Period(), nexts)
	}
	wantC0 := int64(tradesEach/2) * amt
	for i := 0; i < nexts; i++ {
		wantC0 = wantC0 * rho / 10000
	}
	for g := 0; g < goroutines; g++ {
		if c0 := mustC0(t, c, fmt.Sprintf("acct-%d", g)); c0 != wantC0 {
			t.Fatalf("acct-%d 结转后 C0 = %d, 期望 %d", g, c0, wantC0)
		}
	}
}

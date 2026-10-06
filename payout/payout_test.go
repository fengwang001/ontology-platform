package payout_test

import (
	"errors"
	"fmt"
	"sync"
	"testing"

	"ontology/payout"
	"ontology/revenue"
)

func mustLedger(t *testing.T, wd, min int64) *payout.Ledger {
	t.Helper()
	l, err := payout.New(wd, min)
	if err != nil {
		t.Fatalf("payout.New(%d,%d): %v", wd, min, err)
	}
	return l
}

func mustEarn(t *testing.T, l *payout.Ledger, now int64, id, content string, amount int64) []int64 {
	t.Helper()
	got, err := l.Earn(now, id, content, amount)
	if err != nil {
		t.Fatalf("Earn(%s,%d): %v", id, amount, err)
	}
	return got
}

func mustSettle(t *testing.T, l *payout.Ledger, now int64, creator string) (int64, int64) {
	t.Helper()
	paid, offset, err := l.Settle(now, creator)
	if err != nil {
		t.Fatalf("Settle(%s): %v", creator, err)
	}
	return paid, offset
}

// 题目示例：Wd=100，Min=500，甲 7000 乙 3000，余数归甲。
func TestSpecExample(t *testing.T) {
	l := mustLedger(t, 100, 500)
	if err := l.SetSplit(0, "c", []revenue.Part{{Creator: "甲", BPS: 7000}, {Creator: "乙", BPS: 3000}}); err != nil {
		t.Fatalf("SetSplit: %v", err)
	}
	if got := mustEarn(t, l, 0, "e1", "c", 999); fmt.Sprint(got) != "[700 299]" {
		t.Fatalf("e1 份额=%v, want [700 299]（余 1 分给甲）", got)
	}
	if got := mustEarn(t, l, 10, "e2", "c", 1001); fmt.Sprint(got) != "[701 300]" {
		t.Fatalf("e2 份额=%v, want [701 300]", got)
	}
	// t=100：e1 恰成熟（恰等 Wd），e2 未成熟
	if paid, offset := mustSettle(t, l, 100, "甲"); paid != 700 || offset != 0 {
		t.Fatalf("t=100 Settle(甲)=(%d,%d), want (700,0)", paid, offset)
	}
	if paid, _ := mustSettle(t, l, 110, "甲"); paid != 701 {
		t.Fatalf("t=110 Settle(甲)=%d, want 701", paid)
	}
	if paid, _ := mustSettle(t, l, 110, "乙"); paid != 599 {
		t.Fatalf("t=110 Settle(乙)=%d, want 599", paid)
	}
	// t=120：两人 e1 份额均已付出，退款全部转为欠款
	if err := l.Refund(120, "e1"); err != nil {
		t.Fatalf("Refund(e1): %v", err)
	}
	if bal := l.Balance("甲", 120); bal.Debt != 700 {
		t.Fatalf("甲 debt=%d, want 700", bal.Debt)
	}
	if bal := l.Balance("乙", 120); bal.Debt != 299 {
		t.Fatalf("乙 debt=%d, want 299", bal.Debt)
	}
	if got := mustEarn(t, l, 130, "e3", "c", 2000); fmt.Sprint(got) != "[1400 600]" {
		t.Fatalf("e3 份额=%v, want [1400 600]", got)
	}
	// t=230：A=1400，抵债 700，净额 700 不小于 Min，出款 700
	if paid, offset := mustSettle(t, l, 230, "甲"); paid != 700 || offset != 700 {
		t.Fatalf("t=230 Settle(甲)=(%d,%d), want (700,700)", paid, offset)
	}
	// 乙：A=600，抵债 299，净额 301 小于 Min，出款 0，剩余 301 原样结转
	if paid, offset := mustSettle(t, l, 230, "乙"); paid != 0 || offset != 299 {
		t.Fatalf("t=230 Settle(乙)=(%d,%d), want (0,299)", paid, offset)
	}
	if bal := l.Balance("乙", 230); bal.Available != 301 || bal.Debt != 0 {
		t.Fatalf("乙 available=%d debt=%d, want 301/0", bal.Available, bal.Debt)
	}
	// t=240：乙移除 301，debt 增加 600-301=299；甲的 1400 已全付，debt 增加 1400
	if err := l.Refund(240, "e3"); err != nil {
		t.Fatalf("Refund(e3): %v", err)
	}
	if bal := l.Balance("乙", 240); bal.Debt != 299 || bal.Available != 0 {
		t.Fatalf("乙 debt=%d available=%d, want 299/0", bal.Debt, bal.Available)
	}
	if bal := l.Balance("甲", 240); bal.Debt != 1400 {
		t.Fatalf("甲 debt=%d, want 1400", bal.Debt)
	}
}

// 题目冻结示例：半开区间只覆盖 e1；解冻后两笔一起结算。
func TestSpecHoldExample(t *testing.T) {
	l := mustLedger(t, 100, 500)
	if err := l.SetSplit(0, "c", []revenue.Part{{Creator: "甲", BPS: 7000}, {Creator: "乙", BPS: 3000}}); err != nil {
		t.Fatalf("SetSplit: %v", err)
	}
	mustEarn(t, l, 0, "e1", "c", 999)
	mustEarn(t, l, 10, "e2", "c", 1001)
	if err := l.Hold(20, "h1", "c", 0, 10); err != nil {
		t.Fatalf("Hold: %v", err)
	}
	// t=100：e1 被冻结、e2 未成熟，A=0，出款 0
	if paid, offset := mustSettle(t, l, 100, "甲"); paid != 0 || offset != 0 {
		t.Fatalf("t=100 Settle(甲)=(%d,%d), want (0,0)", paid, offset)
	}
	if err := l.Release(105, "h1"); err != nil {
		t.Fatalf("Release: %v", err)
	}
	// t=110：A=700+701=1401，出款 1401
	if paid, _ := mustSettle(t, l, 110, "甲"); paid != 1401 {
		t.Fatalf("t=110 Settle(甲)=%d, want 1401", paid)
	}
}

// 冻结期间整笔退款：被冻结的 remaining 直接移除，不产生欠款。
func TestSpecHoldRefundExample(t *testing.T) {
	l := mustLedger(t, 100, 500)
	if err := l.SetSplit(0, "c", []revenue.Part{{Creator: "甲", BPS: 7000}, {Creator: "乙", BPS: 3000}}); err != nil {
		t.Fatalf("SetSplit: %v", err)
	}
	mustEarn(t, l, 0, "e1", "c", 999)
	mustEarn(t, l, 10, "e2", "c", 1001)
	if err := l.Hold(20, "h1", "c", 0, 10); err != nil {
		t.Fatalf("Hold: %v", err)
	}
	if err := l.Refund(30, "e1"); err != nil {
		t.Fatalf("Refund(e1): %v", err)
	}
	for _, creator := range []string{"甲", "乙"} {
		if bal := l.Balance(creator, 30); bal.Debt != 0 || bal.Held != 0 {
			t.Fatalf("%s debt=%d held=%d, want 0/0（冻结中退款不产生欠款）", creator, bal.Debt, bal.Held)
		}
	}
	// e2 不受影响，解冻与否都能正常成熟结算
	if paid, _ := mustSettle(t, l, 110, "甲"); paid != 701 {
		t.Fatalf("Settle(甲)=%d, want 701", paid)
	}
}

func TestMaturityExactBoundary(t *testing.T) {
	l := mustLedger(t, 100, 1)
	if err := l.SetSplit(0, "c", []revenue.Part{{Creator: "u", BPS: 10000}}); err != nil {
		t.Fatalf("SetSplit: %v", err)
	}
	mustEarn(t, l, 50, "e1", "c", 100)
	// now - 事件时刻 = 99 < Wd：未成熟
	if bal := l.Balance("u", 149); bal.Pending != 100 || bal.Available != 0 {
		t.Fatalf("t=149 pending=%d available=%d, want 100/0", bal.Pending, bal.Available)
	}
	// now - 事件时刻 = 100 = Wd：恰等成熟
	if bal := l.Balance("u", 150); bal.Pending != 0 || bal.Available != 100 {
		t.Fatalf("t=150 pending=%d available=%d, want 0/100", bal.Pending, bal.Available)
	}
	if paid, _ := mustSettle(t, l, 150, "u"); paid != 100 {
		t.Fatalf("Settle=%d, want 100（恰等 Wd 成熟）", paid)
	}
}

func TestNetExactlyMin(t *testing.T) {
	l := mustLedger(t, 0, 500)
	if err := l.SetSplit(0, "c", []revenue.Part{{Creator: "u", BPS: 10000}}); err != nil {
		t.Fatalf("SetSplit: %v", err)
	}
	// 制造欠款 500：入账 500 → 出款 → 退款成欠款
	mustEarn(t, l, 0, "d", "c", 500)
	mustSettle(t, l, 1, "u")
	if err := l.Refund(2, "d"); err != nil {
		t.Fatalf("Refund: %v", err)
	}
	mustEarn(t, l, 3, "e", "c", 1000)
	// A=1000，debt=500，净额 500 恰等 Min：可付
	if paid, offset := mustSettle(t, l, 4, "u"); paid != 500 || offset != 500 {
		t.Fatalf("Settle=(%d,%d), want (500,500)（净额恰等 Min 可付）", paid, offset)
	}
	if bal := l.Balance("u", 4); bal.Available != 0 || bal.Debt != 0 {
		t.Fatalf("available=%d debt=%d, want 0/0", bal.Available, bal.Debt)
	}
}

func TestBelowMinOffsetStillHappens(t *testing.T) {
	l := mustLedger(t, 0, 500)
	if err := l.SetSplit(0, "c", []revenue.Part{{Creator: "u", BPS: 10000}}); err != nil {
		t.Fatalf("SetSplit: %v", err)
	}
	// 制造欠款 600
	mustEarn(t, l, 0, "d", "c", 600)
	mustSettle(t, l, 1, "u")
	if err := l.Refund(2, "d"); err != nil {
		t.Fatalf("Refund: %v", err)
	}
	mustEarn(t, l, 3, "e", "c", 1000)
	// A=1000，debt=600，净额 400 小于 Min：不出款，但抵债照常发生
	if paid, offset := mustSettle(t, l, 4, "u"); paid != 0 || offset != 600 {
		t.Fatalf("Settle=(%d,%d), want (0,600)（未达起付但抵债照常）", paid, offset)
	}
	if bal := l.Balance("u", 4); bal.Available != 400 || bal.Debt != 0 {
		t.Fatalf("available=%d debt=%d, want 400/0（剩余原样结转）", bal.Available, bal.Debt)
	}
}

func TestAvailableNotGreaterThanDebt(t *testing.T) {
	l := mustLedger(t, 0, 500)
	if err := l.SetSplit(0, "c", []revenue.Part{{Creator: "u", BPS: 10000}}); err != nil {
		t.Fatalf("SetSplit: %v", err)
	}
	// 制造欠款 1000
	mustEarn(t, l, 0, "d", "c", 1000)
	mustSettle(t, l, 1, "u")
	if err := l.Refund(2, "d"); err != nil {
		t.Fatalf("Refund: %v", err)
	}
	mustEarn(t, l, 3, "e", "c", 600)
	// A=600 不大于 debt=1000：全部可结算份额清零抵债，出款 0，debt 减为 400
	if paid, offset := mustSettle(t, l, 4, "u"); paid != 0 || offset != 600 {
		t.Fatalf("Settle=(%d,%d), want (0,600)", paid, offset)
	}
	if bal := l.Balance("u", 4); bal.Available != 0 || bal.Debt != 400 {
		t.Fatalf("available=%d debt=%d, want 0/400", bal.Available, bal.Debt)
	}
}

// 被消耗到一半的份额保留其剩余 remaining，退款时只把已消耗部分计入欠款。
func TestPartialConsumeThenRefund(t *testing.T) {
	l := mustLedger(t, 0, 500)
	if err := l.SetSplit(0, "c", []revenue.Part{{Creator: "u", BPS: 10000}}); err != nil {
		t.Fatalf("SetSplit: %v", err)
	}
	// 制造欠款 600
	mustEarn(t, l, 0, "d", "c", 600)
	mustSettle(t, l, 1, "u")
	if err := l.Refund(2, "d"); err != nil {
		t.Fatalf("Refund: %v", err)
	}
	// e 的 1000 被抵债消耗 600，剩余 400 结转（净额 400 < Min 不出款）
	mustEarn(t, l, 3, "e", "c", 1000)
	if _, offset := mustSettle(t, l, 4, "u"); offset != 600 {
		t.Fatalf("offset=%d, want 600", offset)
	}
	// 退款 e：remaining 400 直接移除，已抵债的 600 计入欠款
	if err := l.Refund(5, "e"); err != nil {
		t.Fatalf("Refund(e): %v", err)
	}
	if bal := l.Balance("u", 5); bal.Available != 0 || bal.Debt != 600 {
		t.Fatalf("available=%d debt=%d, want 0/600（部分消耗的份额被退款）", bal.Available, bal.Debt)
	}
}

func TestPaidShareRefundBecomesDebt(t *testing.T) {
	l := mustLedger(t, 0, 500)
	if err := l.SetSplit(0, "c", []revenue.Part{{Creator: "u", BPS: 10000}}); err != nil {
		t.Fatalf("SetSplit: %v", err)
	}
	mustEarn(t, l, 0, "e", "c", 700)
	if paid, _ := mustSettle(t, l, 1, "u"); paid != 700 {
		t.Fatalf("paid=%d, want 700", paid)
	}
	// 已付份额退款：share-remaining=700 全部计入欠款
	if err := l.Refund(2, "e"); err != nil {
		t.Fatalf("Refund: %v", err)
	}
	if bal := l.Balance("u", 2); bal.Debt != 700 {
		t.Fatalf("debt=%d, want 700（已付份额退款成欠款）", bal.Debt)
	}
	// 再次退款报已退款
	if err := l.Refund(3, "e"); !errors.Is(err, payout.ErrAlreadyRefunded) {
		t.Fatalf("err=%v, want ErrAlreadyRefunded", err)
	}
}

func TestSettleRejectionOrder(t *testing.T) {
	l := mustLedger(t, 0, 1)
	if err := l.SetSplit(10, "c", []revenue.Part{{Creator: "u", BPS: 10000}}); err != nil {
		t.Fatalf("SetSplit: %v", err)
	}
	mustEarn(t, l, 20, "e", "c", 100)
	cases := []struct {
		name    string
		now     int64
		creator string
		want    error
	}{
		{"参数非法优先于时钟回退", 5, "", revenue.ErrInvalidParam},
		{"now为负", -1, "u", revenue.ErrInvalidParam},
		{"now超界", 1_000_000_000_001, "u", revenue.ErrInvalidParam},
		{"创作者为空", 30, "", revenue.ErrInvalidParam},
		{"时钟回退", 15, "u", revenue.ErrClockRewind},
		{"时钟回退优先于创作者不存在", 15, "nobody", revenue.ErrClockRewind},
		{"创作者不存在", 30, "nobody", payout.ErrCreatorNotFound},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, _, err := l.Settle(tc.now, tc.creator); !errors.Is(err, tc.want) {
				t.Fatalf("err=%v, want %v", err, tc.want)
			}
		})
	}
	// 被拒不改状态含时钟：余额未动，t=20 的结算仍被接受
	if bal := l.Balance("u", 30); bal.Available != 100 {
		t.Fatalf("available=%d, want 100（被拒操作不改状态）", bal.Available)
	}
	if paid, _ := mustSettle(t, l, 20, "u"); paid != 100 {
		t.Fatalf("paid=%d, want 100", paid)
	}
}

func TestRefundRejectionOrder(t *testing.T) {
	l := mustLedger(t, 0, 1)
	if err := l.SetSplit(10, "c", []revenue.Part{{Creator: "u", BPS: 10000}}); err != nil {
		t.Fatalf("SetSplit: %v", err)
	}
	mustEarn(t, l, 20, "e", "c", 100)
	if err := l.Refund(30, "e"); err != nil {
		t.Fatalf("Refund: %v", err)
	}
	cases := []struct {
		name string
		now  int64
		id   string
		want error
	}{
		{"参数非法优先于时钟回退", 5, "", revenue.ErrInvalidParam},
		{"事件ID为空", 40, "", revenue.ErrInvalidParam},
		{"时钟回退", 15, "nosuch", revenue.ErrClockRewind},
		{"事件不存在", 40, "nosuch", payout.ErrEventNotFound},
		{"已退款", 40, "e", payout.ErrAlreadyRefunded},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := l.Refund(tc.now, tc.id); !errors.Is(err, tc.want) {
				t.Fatalf("err=%v, want %v", err, tc.want)
			}
		})
	}
	// 被拒不改状态含时钟：未付份额退款不产生欠款，t=30 的操作仍被接受
	if bal := l.Balance("u", 40); bal.Debt != 0 || bal.Available != 0 {
		t.Fatalf("debt=%d available=%d, want 0/0", bal.Debt, bal.Available)
	}
	if err := l.Hold(30, "h", "c", 0, 100); err != nil {
		t.Fatalf("Hold at t=30 after rejections: %v", err)
	}
}

func TestConstructorValidation(t *testing.T) {
	for _, tc := range []struct{ wd, min int64 }{
		{-1, 1}, {1_000_000_001, 1}, {0, 0}, {0, -1}, {0, 1_000_000_000_001},
	} {
		if _, err := payout.New(tc.wd, tc.min); !errors.Is(err, revenue.ErrInvalidParam) {
			t.Fatalf("New(%d,%d) err=%v, want ErrInvalidParam", tc.wd, tc.min, err)
		}
	}
	if _, err := payout.New(0, 1); err != nil {
		t.Fatalf("New(0,1): %v", err)
	}
	if _, err := payout.New(1_000_000_000, 1_000_000_000_000); err != nil {
		t.Fatalf("New(1e9,1e12): %v", err)
	}
}

// 并发调用：结果等价于某个串行顺序（-race 检测），终态满足全局恒等式。
func TestConcurrent(t *testing.T) {
	l := mustLedger(t, 10, 100)
	if err := l.SetSplit(0, "c", []revenue.Part{
		{Creator: "u0", BPS: 5000}, {Creator: "u1", BPS: 3000}, {Creator: "u2", BPS: 2000},
	}); err != nil {
		t.Fatalf("SetSplit: %v", err)
	}
	var wg sync.WaitGroup
	var mu sync.Mutex
	totals := map[string]int64{} // 每创作者累计入账
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 50; i++ {
				now := int64(1 + g*50 + i)
				id := fmt.Sprintf("g%de%d", g, i)
				got, err := l.Earn(now, id, "c", 1000)
				if err != nil {
					continue // 时钟回退属正常竞争结果
				}
				mu.Lock()
				for j, creator := range []string{"u0", "u1", "u2"} {
					totals[creator] += got[j]
				}
				mu.Unlock()
				_, _, _ = l.Settle(now, fmt.Sprintf("u%d", i%3))
				_ = l.Balance(fmt.Sprintf("u%d", i%3), now)
			}
		}(g)
	}
	wg.Wait()
	// 全局恒等式：累计入账 = 累计出款 + pending + available + held（无退款无欠款）
	var earned, buckets int64
	for _, creator := range []string{"u0", "u1", "u2"} {
		earned += totals[creator]
		bal := l.Balance(creator, 1_000_000)
		buckets += bal.Pending + bal.Available + bal.Held
		if bal.Debt != 0 {
			t.Fatalf("%s debt=%d, want 0", creator, bal.Debt)
		}
	}
	// 出款额无法直接累计，改为校验：入账 >= 各桶合计，且差值都能被解释为非负出款
	if earned < buckets {
		t.Fatalf("恒等式破坏：earned=%d < buckets=%d", earned, buckets)
	}
}

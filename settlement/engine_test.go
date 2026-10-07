package settlement_test

import (
	"errors"
	"math"
	"testing"

	"ontology/settlement"
)

// contiguousCal 生成 [lo, hi] 全为营业日的日历。
func contiguousCal(lo, hi int64) settlement.Calendar {
	days := make([]int64, 0, hi-lo+1)
	for d := lo; d <= hi; d++ {
		days = append(days, d)
	}
	return settlement.NewCalendar(days)
}

func codeOf(t *testing.T, err error) settlement.Code {
	t.Helper()
	var se *settlement.Error
	if !errors.As(err, &se) {
		t.Fatalf("err %v 不是 *settlement.Error", err)
	}
	return se.Code
}

func mustOK(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func mustSettle(t *testing.T, e *settlement.Engine, now int64, id string, d int64) []settlement.PayoutRecord {
	t.Helper()
	recs, err := e.Settle(now, id, d)
	if err != nil {
		t.Fatalf("settle to %d: %v", d, err)
	}
	return recs
}

func mustSnapshot(t *testing.T, e *settlement.Engine, id string) settlement.Snapshot {
	t.Helper()
	snap, err := e.Snapshot(id)
	if err != nil {
		t.Fatalf("snapshot %q: %v", id, err)
	}
	return snap
}

// checkInvariant 校验不变式：累计出款 + 保证金余额 + 结转负余额 == 已结算流水净额，
// 且保证金余额等于全部批次未动用余额之和。
func checkInvariant(t *testing.T, e *settlement.Engine, id string) {
	t.Helper()
	snap := mustSnapshot(t, e, id)
	got := snap.CumulativePayout + snap.ReserveBalance + snap.Carry
	if got != snap.SettledTxSum {
		t.Fatalf("invariant broken for %q: payout %d + reserve %d + carry %d = %d, settled tx sum %d",
			id, snap.CumulativePayout, snap.ReserveBalance, snap.Carry, got, snap.SettledTxSum)
	}
	var batchSum int64
	for _, b := range snap.Batches {
		batchSum += b.Balance
	}
	if batchSum != snap.ReserveBalance {
		t.Fatalf("reserve balance %d != sum of batches %d", snap.ReserveBalance, batchSum)
	}
}

// 流水发生日恰等于可结算边界（t 往前数第 N 个营业日，含）应被结算；
// 晚一个营业日则不应被结算。
func TestSettlementEligibilityBoundary(t *testing.T) {
	cal := contiguousCal(1, 30)
	e := settlement.NewEngine(cal)
	cfg := settlement.Config{DelayDays: 2, ReserveBps: 0, HorizonDays: 1}
	mustOK(t, e.AddMerchant(1, "m", cfg))
	mustOK(t, e.PostTransaction(3, "m", "tx1", 1, 1000))
	mustOK(t, e.PostTransaction(3, "m", "tx2", 2, 500))
	mustOK(t, e.PostTransaction(3, "m", "tx3", 3, 700))

	// 首次结算到第 4 天，依次处理第 1..4 天：
	// 第 3 天 cutoff=1（tx1 到期）；第 4 天 cutoff=2（tx2 恰在边界，到期；
	// tx3 发生日为 3，差一个营业日，未到期）。
	recs := mustSettle(t, e, 4, "m", 4)
	if len(recs) != 4 {
		t.Fatalf("expect 4 records, got %d", len(recs))
	}
	wantPayout := []int64{0, 0, 1000, 500}
	for i, w := range wantPayout {
		if recs[i].Day != int64(i+1) || recs[i].Payout != w {
			t.Errorf("record %d: got day %d payout %d, want day %d payout %d",
				i, recs[i].Day, recs[i].Payout, i+1, w)
		}
	}

	// 结算到第 5 天：cutoff=3，tx3 到期。
	recs = mustSettle(t, e, 5, "m", 5)
	if len(recs) != 1 || recs[0].Payout != 700 {
		t.Fatalf("day 5: got %+v, want single record payout 700", recs)
	}
	checkInvariant(t, e, "m")
}

// 批次恰在留存日之后第 H 个营业日（含该日）释放，早一天不释放。
func TestReserveReleaseExactHorizon(t *testing.T) {
	cal := contiguousCal(1, 30)
	e := settlement.NewEngine(cal)
	cfg := settlement.Config{DelayDays: 1, ReserveBps: 1000, HorizonDays: 2} // 10%，H=2
	mustOK(t, e.AddMerchant(1, "m", cfg))
	mustOK(t, e.PostTransaction(1, "m", "tx1", 1, 10000))

	// 第 2 天：net=10000，留存 1000（批次留存日 2，到期日 = 第 2 天之后第 2 个营业日 = 第 4 天）。
	recs := mustSettle(t, e, 2, "m", 2)
	if len(recs) != 2 || recs[1].Payout != 9000 || recs[1].NewReserve != 1000 {
		t.Fatalf("day 2: got %+v, want payout 9000 reserve 1000", recs)
	}
	snap := mustSnapshot(t, e, "m")
	if len(snap.Batches) != 1 || snap.Batches[0].ReleaseDay != 4 || snap.Batches[0].Balance != 1000 {
		t.Fatalf("batch: got %+v, want release day 4 balance 1000", snap.Batches)
	}

	// 第 3 天：批次未到期（第 3 天是留存日之后第 1 个营业日），出款 0。
	recs = mustSettle(t, e, 3, "m", 3)
	if len(recs) != 1 || recs[0].Payout != 0 || recs[0].Release != 0 {
		t.Fatalf("day 3: got %+v, want payout 0 release 0", recs)
	}

	// 第 4 天：恰为第 H 个营业日，释放 1000。
	recs = mustSettle(t, e, 4, "m", 4)
	if len(recs) != 1 || recs[0].Release != 1000 || recs[0].Payout != 1000 {
		t.Fatalf("day 4: got %+v, want release 1000 payout 1000", recs)
	}
	snap = mustSnapshot(t, e, "m")
	if snap.ReserveBalance != 0 || len(snap.Batches) != 0 {
		t.Fatalf("after release: got reserve %d batches %+v", snap.ReserveBalance, snap.Batches)
	}
	checkInvariant(t, e, "m")
}

// 负净额动用保证金：部分动用后，批次剩余部分到期照常释放。
func TestNegativeNetDrawsReservePartialThenRelease(t *testing.T) {
	cal := contiguousCal(1, 30)
	e := settlement.NewEngine(cal)
	cfg := settlement.Config{DelayDays: 1, ReserveBps: 5000, HorizonDays: 3} // 50%，H=3
	mustOK(t, e.AddMerchant(1, "m", cfg))
	mustOK(t, e.PostTransaction(1, "m", "pay", 1, 10000))

	// 第 2 天：net=10000，留存 5000（到期日 = 第 2 天之后第 3 个营业日 = 第 5 天）。
	recs := mustSettle(t, e, 2, "m", 2)
	if recs[1].Payout != 5000 || recs[1].NewReserve != 5000 {
		t.Fatalf("day 2: got %+v", recs[1])
	}

	// 第 2 天发生退款 -2000，第 3 天结算：net=-2000，动用批次 2000 补足至零。
	mustOK(t, e.PostTransaction(2, "m", "refund", 2, -2000))
	recs = mustSettle(t, e, 3, "m", 3)
	if recs[0].Net != -2000 || recs[0].Drawn != 2000 || recs[0].Payout != 0 || recs[0].CarryAfter != 0 {
		t.Fatalf("day 3: got %+v, want net -2000 drawn 2000 payout 0 carry 0", recs[0])
	}
	snap := mustSnapshot(t, e, "m")
	if len(snap.Batches) != 1 || snap.Batches[0].Balance != 3000 {
		t.Fatalf("batch after draw: got %+v, want balance 3000", snap.Batches)
	}

	// 第 5 天批次到期，释放剩余 3000（第 4 天出款 0）。
	recs = mustSettle(t, e, 5, "m", 5)
	if len(recs) != 2 || recs[0].Payout != 0 || recs[1].Release != 3000 || recs[1].Payout != 3000 {
		t.Fatalf("days 4..5: got %+v, want day4 payout 0, day5 release 3000 payout 3000", recs)
	}
	checkInvariant(t, e, "m")
}

// 保证金不足以弥补负净额时，缺口作为负余额逐日结转，直到被后续正流水清偿。
func TestNegativeCarryMultipleDays(t *testing.T) {
	cal := contiguousCal(1, 30)
	e := settlement.NewEngine(cal)
	cfg := settlement.Config{DelayDays: 1, ReserveBps: 0, HorizonDays: 1}
	mustOK(t, e.AddMerchant(1, "m", cfg))
	mustOK(t, e.PostTransaction(1, "m", "cb", 1, -500)) // 拒付扣回

	// 第 2 天：net=-500，无保证金可动用，结转 -500。
	recs := mustSettle(t, e, 2, "m", 2)
	if recs[1].Net != -500 || recs[1].Payout != 0 || recs[1].CarryAfter != -500 {
		t.Fatalf("day 2: got %+v", recs[1])
	}
	// 第 3、4 天：负余额持续结转，不被丢弃。
	recs = mustSettle(t, e, 4, "m", 4)
	for i, r := range recs {
		if r.Net != -500 || r.CarryAfter != -500 || r.Payout != 0 {
			t.Fatalf("day %d: got %+v, want net -500 carry -500 payout 0", i+3, r)
		}
	}
	// 第 4 天入账 +800，第 5 天结算：net = -500 + 800 = 300，清偿结转。
	mustOK(t, e.PostTransaction(4, "m", "pay", 4, 800))
	recs = mustSettle(t, e, 5, "m", 5)
	if recs[0].Net != 300 || recs[0].Payout != 300 || recs[0].CarryAfter != 0 {
		t.Fatalf("day 5: got %+v, want net 300 payout 300 carry 0", recs[0])
	}
	checkInvariant(t, e, "m")
}

// 到期释放额在可结算净额为负时先入账，释放足以覆盖负净额时出款为正。
func TestReleaseCoversNegativeNet(t *testing.T) {
	cal := contiguousCal(1, 30)
	e := settlement.NewEngine(cal)
	cfg := settlement.Config{DelayDays: 1, ReserveBps: 5000, HorizonDays: 2} // 50%，H=2
	mustOK(t, e.AddMerchant(1, "m", cfg))
	mustOK(t, e.PostTransaction(1, "m", "pay", 1, 10000))
	// 第 2 天：留存 5000，到期日第 4 天。
	mustSettle(t, e, 2, "m", 2)
	// 第 3 天发生退款 -3000，第 4 天结算：net=-3000，释放 5000 先入账，
	// -3000+5000=2000 >= 0，出款 2000，不动用、不结转。
	mustOK(t, e.PostTransaction(3, "m", "refund", 3, -3000))
	recs := mustSettle(t, e, 4, "m", 4)
	day4 := recs[1]
	if day4.Net != -3000 || day4.Release != 5000 || day4.Drawn != 0 || day4.Payout != 2000 || day4.CarryAfter != 0 {
		t.Fatalf("day 4: got %+v, want net -3000 release 5000 drawn 0 payout 2000", day4)
	}
	checkInvariant(t, e, "m")
}

// gappedCal 生成带缺口的营业日日历（跳过 7 的倍数），用于验证非连续营业日。
func gappedCal(lo, hi int64) settlement.Calendar {
	var days []int64
	for d := lo; d <= hi; d++ {
		if d%7 != 0 {
			days = append(days, d)
		}
	}
	return settlement.NewCalendar(days)
}

// 追赶结算（一次结算到远日）与逐日结算产生的出款记录与最终状态必须完全相同。
func TestCatchUpEqualsDailySettlement(t *testing.T) {
	cal := gappedCal(1, 40)
	cfg := settlement.Config{DelayDays: 2, ReserveBps: 1500, HorizonDays: 3}

	build := func() *settlement.Engine {
		e := settlement.NewEngine(cal)
		mustOK(t, e.AddMerchant(1, "m", cfg))
		// 混合正负载流水，覆盖留存、动用、结转路径。
		txs := []struct {
			day    int64
			amount int64
		}{
			{1, 10000}, {2, -3000}, {3, 5000}, {4, -8000}, {5, 2000},
			{6, -500}, {8, 12000}, {9, -20000}, {10, 3000}, {11, 1500},
		}
		for i, tx := range txs {
			mustOK(t, e.PostTransaction(tx.day, "m", txID(i), tx.day, tx.amount))
		}
		return e
	}

	// 逐日结算。
	daily := build()
	var dailyRecs []settlement.PayoutRecord
	for _, d := range calDays(cal, 1, 30) {
		dailyRecs = append(dailyRecs, mustSettle(t, daily, 30, "m", d)...)
	}

	// 一次追赶到第 30 天。
	catchUp := build()
	catchUpRecs := mustSettle(t, catchUp, 30, "m", 30)

	if len(dailyRecs) != len(catchUpRecs) {
		t.Fatalf("record count: daily %d, catch-up %d", len(dailyRecs), len(catchUpRecs))
	}
	for i := range dailyRecs {
		if dailyRecs[i] != catchUpRecs[i] {
			t.Fatalf("record %d differs:\n daily: %+v\ncatchup: %+v", i, dailyRecs[i], catchUpRecs[i])
		}
	}
	ds := mustSnapshot(t, daily, "m")
	cs := mustSnapshot(t, catchUp, "m")
	if ds.CumulativePayout != cs.CumulativePayout || ds.ReserveBalance != cs.ReserveBalance ||
		ds.Carry != cs.Carry || len(ds.Batches) != len(cs.Batches) {
		t.Fatalf("snapshot differs:\n daily: %+v\ncatchup: %+v", ds, cs)
	}
	for i := range ds.Batches {
		if ds.Batches[i] != cs.Batches[i] {
			t.Fatalf("batch %d differs: %+v vs %+v", i, ds.Batches[i], cs.Batches[i])
		}
	}
	checkInvariant(t, daily, "m")
	checkInvariant(t, catchUp, "m")
}

func txID(i int) string { return "tx-" + string(rune('a'+i)) }

// calDays 返回日历中 [lo, hi] 内的营业日，便于逐日结算。
func calDays(cal settlement.Calendar, lo, hi int64) []int64 {
	return cal.DaysBetween(lo-1, hi)
}

// 封账后补录发生日早于已结算营业日的流水被拒；等于最近结算日的补录允许。
func TestClosedBooksRejection(t *testing.T) {
	cal := contiguousCal(1, 30)
	e := settlement.NewEngine(cal)
	cfg := settlement.Config{DelayDays: 1, ReserveBps: 0, HorizonDays: 1}
	mustOK(t, e.AddMerchant(1, "m", cfg))
	mustOK(t, e.PostTransaction(3, "m", "tx1", 3, 100))
	mustSettle(t, e, 5, "m", 5)

	// 发生日 4 < 最近结算日 5：已封账。
	if err := e.PostTransaction(6, "m", "late", 4, 50); codeOf(t, err) != settlement.CodeAlreadyClosed {
		t.Fatalf("expect AlreadyClosed, got %v", err)
	}
	// 发生日等于最近结算日 5：允许（不早于）。
	mustOK(t, e.PostTransaction(6, "m", "edge", 5, 50))
	// 发生日晚于 now：日期非法。
	if err := e.PostTransaction(6, "m", "future", 7, 50); codeOf(t, err) != settlement.CodeInvalidDate {
		t.Fatalf("expect InvalidDate, got %v", err)
	}
	checkInvariant(t, e, "m")
}

// 被拒绝的操作不得改变任何状态与时钟。
func TestRejectedOpsLeaveNoTrace(t *testing.T) {
	cal := contiguousCal(1, 30)
	e := settlement.NewEngine(cal)
	cfg := settlement.Config{DelayDays: 1, ReserveBps: 1000, HorizonDays: 2}
	mustOK(t, e.AddMerchant(1, "m", cfg))
	mustOK(t, e.PostTransaction(2, "m", "tx1", 2, 1000))
	mustSettle(t, e, 3, "m", 3)

	before := mustSnapshot(t, e, "m")

	// 各类被拒绝操作，now 均取更大的 20，若被接受会推进时钟。
	rejected := []error{
		e.PostTransaction(20, "m", "tx1", 3, 1),       // 编号重复
		e.PostTransaction(20, "m", "x", 21, 1),        // 日期非法
		e.PostTransaction(20, "m", "y", 2, 1),         // 已封账
		e.PostTransaction(20, "ghost", "z", 3, 1),     // 商户不存在
		e.AddMerchant(20, "m", cfg),                   // 商户已存在
		e.AddMerchant(20, "bad", settlement.Config{}), // 参数非法
	}
	for i, err := range rejected {
		if err == nil {
			t.Fatalf("rejected op %d unexpectedly succeeded", i)
		}
	}
	// 关键检测：拒绝操作的 now=20 不得推进时钟，now=10（<20 但 >=3）仍应被接受。
	mustOK(t, e.PostTransaction(10, "m", "tx2", 3, 5))

	after := mustSnapshot(t, e, "m")
	// 状态除被接受的 tx2（pending +1）外不变。
	if after.CumulativePayout != before.CumulativePayout ||
		after.ReserveBalance != before.ReserveBalance ||
		after.Carry != before.Carry ||
		after.LastSettledDay != before.LastSettledDay ||
		len(after.Batches) != len(before.Batches) {
		t.Fatalf("state changed by rejected ops:\nbefore %+v\nafter  %+v", before, after)
	}
	if after.PendingTxCount != before.PendingTxCount+1 {
		t.Fatalf("pending count: before %d after %d", before.PendingTxCount, after.PendingTxCount)
	}
}

// 时钟回退被拒，且回退的拒绝操作不推进时钟。
func TestClockRollback(t *testing.T) {
	cal := contiguousCal(1, 30)
	e := settlement.NewEngine(cal)
	cfg := settlement.Config{DelayDays: 1, ReserveBps: 0, HorizonDays: 1}
	mustOK(t, e.AddMerchant(5, "m", cfg))
	if err := e.PostTransaction(4, "m", "tx1", 1, 1); codeOf(t, err) != settlement.CodeClockRollback {
		t.Fatalf("expect ClockRollback, got %v", err)
	}
	if _, err := e.Settle(4, "m", 6); codeOf(t, err) != settlement.CodeClockRollback {
		t.Fatalf("expect ClockRollback, got %v", err)
	}
	// now 等于上次被接受值：允许。
	mustOK(t, e.PostTransaction(5, "m", "tx1", 1, 1))
}

// 错误须可区分并按优先级只报第一个：
// 流水：参数非法 > 时钟回退 > 商户不存在 > 流水编号重复 > 日期非法 > 已封账；
// 结算：参数非法 > 时钟回退 > 商户不存在 > 非营业日 > 重复结算。
func TestErrorPrecedence(t *testing.T) {
	cal := contiguousCal(1, 30)
	e := settlement.NewEngine(cal)
	cfg := settlement.Config{DelayDays: 1, ReserveBps: 0, HorizonDays: 1}
	mustOK(t, e.AddMerchant(10, "m", cfg))
	mustOK(t, e.PostTransaction(10, "m", "dup", 1, 1))
	// 结算到第 20 天（now=10，结算日可以晚于 now），制造"已封账"与"日期非法"并存的条件。
	mustSettle(t, e, 10, "m", 20)

	// 初始状态：未结算过时 LastSettledDay 为 math.MinInt64。
	e2 := settlement.NewEngine(cal)
	mustOK(t, e2.AddMerchant(1, "fresh", cfg))
	if snap := mustSnapshot(t, e2, "fresh"); snap.LastSettledDay != math.MinInt64 {
		t.Fatalf("fresh merchant LastSettledDay = %d, want MinInt64", snap.LastSettledDay)
	}

	txCases := []struct {
		name           string
		now            int64
		merchantID, id string
		day, amount    int64
		want           settlement.Code
	}{
		// 参数非法（空 id）优先于时钟回退、商户不存在。
		{"param>clock", 5, "ghost", "", 25, 1, settlement.CodeInvalidParam},
		// 时钟回退优先于商户不存在。
		{"clock>merchant", 5, "ghost", "a", 1, 1, settlement.CodeClockRollback},
		// 商户不存在优先于编号重复（dup 在 m 中已存在）。
		{"merchant>dup", 10, "ghost", "dup", 1, 1, settlement.CodeMerchantNotFound},
		// 编号重复优先于日期非法。
		{"dup>date", 10, "m", "dup", 25, 1, settlement.CodeDuplicateTxID},
		// 日期非法优先于已封账（day=25 > now=10 且 < 已结算到的 20? 否，25>20；
		// 改用 day=15：15>10 日期非法，15<20 已封账，两者并存）。
		{"date>closed", 10, "m", "new", 15, 1, settlement.CodeInvalidDate},
		// 仅已封账。
		{"closed", 16, "m", "new2", 15, 1, settlement.CodeAlreadyClosed},
	}
	for _, tc := range txCases {
		err := e.PostTransaction(tc.now, tc.merchantID, tc.id, tc.day, tc.amount)
		if codeOf(t, err) != tc.want {
			t.Errorf("%s: got code %v, want %v", tc.name, codeOf(t, err), tc.want)
		}
	}

	settleCases := []struct {
		name       string
		now        int64
		merchantID string
		d          int64
		want       settlement.Code
	}{
		{"param>clock", 5, "", 21, settlement.CodeInvalidParam},
		{"clock>merchant", 5, "ghost", 21, settlement.CodeClockRollback},
		{"merchant>nonbusiness", 16, "ghost", 21, settlement.CodeMerchantNotFound},
		// 非营业日优先于重复结算：d=19 <= 20 且 19 是营业日，不构成并存；
		// 用带缺口日历验证并存场景见下。
		{"duplicate", 16, "m", 19, settlement.CodeDuplicateSettlement},
		{"nonbusiness", 16, "m", 35, settlement.CodeNonBusinessDay},
	}
	for _, tc := range settleCases {
		_, err := e.Settle(tc.now, tc.merchantID, tc.d)
		if codeOf(t, err) != tc.want {
			t.Errorf("%s: got code %v, want %v", tc.name, codeOf(t, err), tc.want)
		}
	}

	// 非营业日 > 重复结算 并存：带缺口日历中，已结算到第 8 天，d=7 非营业日且 <= 8。
	gcal := gappedCal(1, 30) // 7 的倍数非营业日
	e3 := settlement.NewEngine(gcal)
	mustOK(t, e3.AddMerchant(1, "m", cfg))
	mustSettle(t, e3, 9, "m", 8)
	if _, err := e3.Settle(9, "m", 7); codeOf(t, err) != settlement.CodeNonBusinessDay {
		t.Fatalf("nonbusiness>duplicate: got %v", err)
	}
}

// 出款额为零时仍产生记录；相同操作序列重放得到完全相同的结果。
func TestZeroPayoutRecordsAndReplayDeterminism(t *testing.T) {
	cal := gappedCal(1, 40)
	cfg := settlement.Config{DelayDays: 2, ReserveBps: 2500, HorizonDays: 3}

	run := func() ([]settlement.PayoutRecord, settlement.Snapshot) {
		e := settlement.NewEngine(cal)
		mustOK(t, e.AddMerchant(1, "m", cfg))
		mustOK(t, e.PostTransaction(2, "m", "a", 1, 4000))
		mustOK(t, e.PostTransaction(2, "m", "b", 2, -1500))
		recs := mustSettle(t, e, 10, "m", 15)
		// 空区间也逐日产生零出款记录。
		recs = append(recs, mustSettle(t, e, 10, "m", 20)...)
		return recs, mustSnapshot(t, e, "m")
	}

	recs1, snap1 := run()
	recs2, snap2 := run()
	if len(recs1) != len(recs2) {
		t.Fatalf("replay record count differs")
	}
	for i := range recs1 {
		if recs1[i] != recs2[i] {
			t.Fatalf("replay record %d differs: %+v vs %+v", i, recs1[i], recs2[i])
		}
	}
	if snap1.CumulativePayout != snap2.CumulativePayout || snap1.ReserveBalance != snap2.ReserveBalance {
		t.Fatalf("replay snapshot differs")
	}
	// 区间内无流水的营业日也产生了零出款记录。
	zero := 0
	for _, r := range recs1 {
		if r.Payout == 0 {
			zero++
		}
	}
	if zero == 0 {
		t.Fatalf("expect some zero-payout records in %+v", recs1)
	}
}

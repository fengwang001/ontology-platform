package ontology_test

import (
	"errors"
	"fmt"
	"sync"
	"testing"

	ontology "ontology"
	"ontology/hold"
	"ontology/payout"
	"ontology/revenue"
)

type step struct {
	op      string // setsplit | earn | hold | release | settle | refund | balance
	now     int64
	content string
	parts   []revenue.Part
	eventID string
	amount  int64
	holdID  string
	from    int64
	to      int64
	creator string

	wantErr     error
	wantShares  []revenue.Share
	wantPayout  int64
	wantOffset  int64
	wantHeld    int64
	wantPending int64
	wantAvail   int64
	wantDebt    int64
}

type scenario struct {
	name  string
	wd    int64
	min   int64
	steps []step
}

func runScenario(t *testing.T, sc scenario) {
	t.Helper()
	rev, hld, pay, err := ontology.New(sc.wd, sc.min)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	for i, st := range stepsOf(sc) {
		label := fmt.Sprintf("step %d (%s)", i, st.op)
		switch st.op {
		case "setsplit":
			err := rev.SetSplit(st.now, st.content, st.parts)
			checkErr(t, label, err, st.wantErr)
		case "earn":
			shares, err := rev.Earn(st.now, st.eventID, st.content, st.amount)
			checkErr(t, label, err, st.wantErr)
			if st.wantErr == nil {
				if fmt.Sprint(shares) != fmt.Sprint(st.wantShares) {
					t.Fatalf("%s: shares=%v, want %v", label, shares, st.wantShares)
				}
				var sum int64
				for _, s := range shares {
					sum += s.Amount
				}
				if sum != st.amount {
					t.Fatalf("%s: share sum %d != amount %d", label, sum, st.amount)
				}
			}
		case "hold":
			err := hld.Hold(st.now, st.holdID, st.content, st.from, st.to)
			checkErr(t, label, err, st.wantErr)
		case "release":
			err := hld.Release(st.now, st.holdID)
			checkErr(t, label, err, st.wantErr)
		case "settle":
			payoutAmt, offset, err := pay.Settle(st.now, st.creator)
			checkErr(t, label, err, st.wantErr)
			if st.wantErr == nil && (payoutAmt != st.wantPayout || offset != st.wantOffset) {
				t.Fatalf("%s: payout=%d offset=%d, want payout=%d offset=%d",
					label, payoutAmt, offset, st.wantPayout, st.wantOffset)
			}
			if st.wantErr == nil && payoutAmt != 0 && payoutAmt < sc.min {
				t.Fatalf("%s: payout %d below Min %d", label, payoutAmt, sc.min)
			}
		case "refund":
			err := pay.Refund(st.now, st.eventID)
			checkErr(t, label, err, st.wantErr)
		case "balance":
			held, pending, avail, debt := pay.Balance(st.creator, st.now)
			if held != st.wantHeld || pending != st.wantPending || avail != st.wantAvail || debt != st.wantDebt {
				t.Fatalf("%s: balance=(%d,%d,%d,%d), want (%d,%d,%d,%d)",
					label, held, pending, avail, debt,
					st.wantHeld, st.wantPending, st.wantAvail, st.wantDebt)
			}
		default:
			t.Fatalf("%s: unknown op", label)
		}
	}
}

func stepsOf(sc scenario) []step { return sc.steps }

func checkErr(t *testing.T, label string, err, want error) {
	t.Helper()
	if want == nil {
		if err != nil {
			t.Fatalf("%s: unexpected error %v", label, err)
		}
		return
	}
	if !errors.Is(err, want) {
		t.Fatalf("%s: err=%v, want errors.Is %v", label, err, want)
	}
}

func parts(ps ...interface{}) []revenue.Part {
	out := make([]revenue.Part, 0, len(ps)/2)
	for i := 0; i < len(ps); i += 2 {
		out = append(out, revenue.Part{Creator: ps[i].(string), Bps: ps[i+1].(int64)})
	}
	return out
}

func shares(ps ...interface{}) []revenue.Share {
	out := make([]revenue.Share, 0, len(ps)/2)
	for i := 0; i < len(ps); i += 2 {
		out = append(out, revenue.Share{Creator: ps[i].(string), Amount: ps[i+1].(int64)})
	}
	return out
}

// The worked example from the specification.
var specExample = scenario{
	name: "spec main example",
	wd:   100,
	min:  500,
	steps: []step{
		{op: "setsplit", now: 0, content: "c", parts: parts("甲", int64(7000), "乙", int64(3000))},
		// 余数归第一位: 999 -> 699+299=998, 余 1 给甲
		{op: "earn", now: 0, eventID: "e1", content: "c", amount: 999,
			wantShares: shares("甲", int64(700), "乙", int64(299))},
		{op: "earn", now: 10, eventID: "e2", content: "c", amount: 1001,
			wantShares: shares("甲", int64(701), "乙", int64(300))},
		// t=100: e1 恰成熟, e2 未成熟
		{op: "balance", now: 100, creator: "甲", wantPending: 701, wantAvail: 700},
		{op: "settle", now: 100, creator: "甲", wantPayout: 700},
		{op: "settle", now: 110, creator: "甲", wantPayout: 701},
		{op: "settle", now: 110, creator: "乙", wantPayout: 599},
		// 两人 e1 份额均已付出, 退款全部转为欠款
		{op: "refund", now: 120, eventID: "e1"},
		{op: "balance", now: 120, creator: "甲", wantDebt: 700},
		{op: "balance", now: 120, creator: "乙", wantDebt: 299},
		{op: "earn", now: 130, eventID: "e3", content: "c", amount: 2000,
			wantShares: shares("甲", int64(1400), "乙", int64(600))},
		// A=1400 > debt=700, 净额 700 >= Min
		{op: "settle", now: 230, creator: "甲", wantPayout: 700, wantOffset: 700},
		// A=600 > debt=299, 净额 301 < Min: 抵债照常, 不出款, 余 301 结转
		{op: "settle", now: 230, creator: "乙", wantPayout: 0, wantOffset: 299},
		{op: "balance", now: 230, creator: "乙", wantAvail: 301},
		// 乙: 移除 301, 已抵 299 计入欠款; 甲: 1400 全已付/抵, 全计欠款
		{op: "refund", now: 240, eventID: "e3"},
		{op: "balance", now: 240, creator: "乙", wantDebt: 299},
		{op: "balance", now: 240, creator: "甲", wantDebt: 1400},
	},
}

// The hold example from the specification.
var holdExample = scenario{
	name: "spec hold example",
	wd:   100,
	min:  500,
	steps: []step{
		{op: "setsplit", now: 0, content: "c", parts: parts("甲", int64(7000), "乙", int64(3000))},
		{op: "earn", now: 0, eventID: "e1", content: "c", amount: 999,
			wantShares: shares("甲", int64(700), "乙", int64(299))},
		{op: "earn", now: 10, eventID: "e2", content: "c", amount: 1001,
			wantShares: shares("甲", int64(701), "乙", int64(300))},
		// 半开区间 [0,10): 只覆盖 e1
		{op: "hold", now: 20, holdID: "h1", content: "c", from: 0, to: 10},
		{op: "balance", now: 100, creator: "甲", wantHeld: 700, wantPending: 701},
		{op: "settle", now: 100, creator: "甲", wantPayout: 0, wantOffset: 0},
		{op: "release", now: 105, holdID: "h1"},
		{op: "settle", now: 110, creator: "甲", wantPayout: 1401},
	},
}

// Refunding a held event removes the frozen remaining without debt.
var holdRefund = scenario{
	name: "refund while held",
	wd:   100,
	min:  500,
	steps: []step{
		{op: "setsplit", now: 0, content: "c", parts: parts("甲", int64(7000), "乙", int64(3000))},
		{op: "earn", now: 0, eventID: "e1", content: "c", amount: 999,
			wantShares: shares("甲", int64(700), "乙", int64(299))},
		{op: "hold", now: 20, holdID: "h1", content: "c", from: 0, to: 10},
		{op: "refund", now: 30, eventID: "e1"},
		{op: "balance", now: 30, creator: "甲"}, // 无欠款, 无剩余
		{op: "balance", now: 30, creator: "乙"},
		{op: "release", now: 40, holdID: "h1"},
		{op: "balance", now: 40, creator: "甲"},
	},
}

func TestSpecExamples(t *testing.T) {
	for _, sc := range []scenario{specExample, holdExample, holdRefund} {
		t.Run(sc.name, func(t *testing.T) { runScenario(t, sc) })
	}
}

func TestBoundaryScenarios(t *testing.T) {
	scenarios := []scenario{
		{
			name: "maturity exactly Wd",
			wd:   100,
			min:  1,
			steps: []step{
				{op: "setsplit", now: 0, content: "c", parts: parts("a", int64(10000))},
				{op: "earn", now: 5, eventID: "e1", content: "c", amount: 100,
					wantShares: shares("a", int64(100))},
				{op: "balance", now: 104, creator: "a", wantPending: 100},
				{op: "settle", now: 104, creator: "a", wantPayout: 0},
				{op: "balance", now: 105, creator: "a", wantAvail: 100}, // 恰等成熟
				{op: "settle", now: 105, creator: "a", wantPayout: 100},
			},
		},
		{
			name: "hold half-open interval and late events",
			wd:   0,
			min:  1,
			steps: []step{
				{op: "setsplit", now: 0, content: "c", parts: parts("a", int64(10000))},
				{op: "earn", now: 10, eventID: "e1", content: "c", amount: 100,
					wantShares: shares("a", int64(100))},
				// [10, 20): e1@10 被覆盖; 之后入账且落在区间内的事件也被冻结
				{op: "hold", now: 15, holdID: "h1", content: "c", from: 10, to: 20},
				{op: "balance", now: 15, creator: "a", wantHeld: 100},
				{op: "earn", now: 19, eventID: "e2", content: "c", amount: 50,
					wantShares: shares("a", int64(50))},
				{op: "balance", now: 19, creator: "a", wantHeld: 150},
				// e3@20 不在 [10,20) 内
				{op: "earn", now: 20, eventID: "e3", content: "c", amount: 7,
					wantShares: shares("a", int64(7))},
				{op: "balance", now: 20, creator: "a", wantHeld: 150, wantAvail: 7},
				{op: "settle", now: 20, creator: "a", wantPayout: 7},
			},
		},
		{
			name: "stacked holds need all releases",
			wd:   0,
			min:  1,
			steps: []step{
				{op: "setsplit", now: 0, content: "c", parts: parts("a", int64(10000))},
				{op: "earn", now: 1, eventID: "e1", content: "c", amount: 100,
					wantShares: shares("a", int64(100))},
				{op: "hold", now: 2, holdID: "h1", content: "c", from: 0, to: 10},
				{op: "hold", now: 3, holdID: "h2", content: "c", from: 0, to: 10},
				{op: "release", now: 4, holdID: "h1"},
				{op: "balance", now: 4, creator: "a", wantHeld: 100}, // 仍被 h2 覆盖
				{op: "settle", now: 4, creator: "a", wantPayout: 0},
				{op: "release", now: 5, holdID: "h2"},
				{op: "balance", now: 5, creator: "a", wantAvail: 100},
				{op: "settle", now: 5, creator: "a", wantPayout: 100},
			},
		},
		{
			name: "net exactly Min pays out",
			wd:   0,
			min:  500,
			steps: []step{
				{op: "setsplit", now: 0, content: "c", parts: parts("a", int64(10000))},
				{op: "earn", now: 0, eventID: "e1", content: "c", amount: 800,
					wantShares: shares("a", int64(800))},
				{op: "settle", now: 0, creator: "a", wantPayout: 800},
				{op: "refund", now: 1, eventID: "e1"}, // debt=800
				{op: "earn", now: 2, eventID: "e2", content: "c", amount: 1300,
					wantShares: shares("a", int64(1300))},
				// A=1300, debt=800, 净额恰等 500 -> 出款
				{op: "settle", now: 2, creator: "a", wantPayout: 500, wantOffset: 800},
			},
		},
		{
			name: "below Min still offsets debt",
			wd:   0,
			min:  500,
			steps: []step{
				{op: "setsplit", now: 0, content: "c", parts: parts("a", int64(10000))},
				{op: "earn", now: 0, eventID: "e1", content: "c", amount: 800,
					wantShares: shares("a", int64(800))},
				{op: "settle", now: 0, creator: "a", wantPayout: 800},
				{op: "refund", now: 1, eventID: "e1"}, // debt=800
				{op: "earn", now: 2, eventID: "e2", content: "c", amount: 1000,
					wantShares: shares("a", int64(1000))},
				// 净额 200 < Min: 抵债 800 照常, 不出款, 余 200 结转
				{op: "settle", now: 2, creator: "a", wantPayout: 0, wantOffset: 800},
				{op: "balance", now: 2, creator: "a", wantAvail: 200},
			},
		},
		{
			name: "A not greater than debt",
			wd:   0,
			min:  1,
			steps: []step{
				{op: "setsplit", now: 0, content: "c", parts: parts("a", int64(10000))},
				{op: "earn", now: 0, eventID: "e1", content: "c", amount: 900,
					wantShares: shares("a", int64(900))},
				{op: "settle", now: 0, creator: "a", wantPayout: 900},
				{op: "refund", now: 1, eventID: "e1"}, // debt=900
				{op: "earn", now: 2, eventID: "e2", content: "c", amount: 400,
					wantShares: shares("a", int64(400))},
				// A=400 <= debt=900: 全部清零抵债, 出款 0
				{op: "settle", now: 2, creator: "a", wantPayout: 0, wantOffset: 400},
				{op: "balance", now: 2, creator: "a", wantDebt: 500},
			},
		},
		{
			name: "partially consumed share refunded",
			wd:   0,
			min:  500,
			steps: []step{
				{op: "setsplit", now: 0, content: "c", parts: parts("a", int64(10000))},
				{op: "earn", now: 0, eventID: "e1", content: "c", amount: 700,
					wantShares: shares("a", int64(700))},
				{op: "settle", now: 0, creator: "a", wantPayout: 700},
				{op: "refund", now: 1, eventID: "e1"}, // debt=700
				{op: "earn", now: 2, eventID: "e2", content: "c", amount: 1000,
					wantShares: shares("a", int64(1000))},
				// 抵债 700, e2 份额被消耗到一半(余 300), 净额 300 < Min 不出款
				{op: "settle", now: 2, creator: "a", wantPayout: 0, wantOffset: 700},
				{op: "balance", now: 2, creator: "a", wantAvail: 300},
				// 退款 e2: 移除 300, 已抵 700 计入欠款
				{op: "refund", now: 3, eventID: "e2"},
				{op: "balance", now: 3, creator: "a", wantDebt: 700},
			},
		},
		{
			name: "event idempotent replay and conflict",
			wd:   0,
			min:  1,
			steps: []step{
				{op: "setsplit", now: 0, content: "c", parts: parts("a", int64(6000), "b", int64(4000))},
				{op: "earn", now: 1, eventID: "e1", content: "c", amount: 100,
					wantShares: shares("a", int64(60), "b", int64(40))},
				// 完全相同的重放: 空操作, 返回原份额, 不推进时钟
				{op: "earn", now: 5, eventID: "e1", content: "c", amount: 100,
					wantShares: shares("a", int64(60), "b", int64(40))},
				{op: "balance", now: 5, creator: "a", wantAvail: 60},
				// 时钟未被重放推进: now=2 仍被接受
				{op: "earn", now: 2, eventID: "e2", content: "c", amount: 10,
					wantShares: shares("a", int64(6), "b", int64(4))},
				// 金额不同 -> 冲突; 内容不同 -> 冲突
				{op: "earn", now: 6, eventID: "e1", content: "c", amount: 101,
					wantErr: revenue.ErrEventConflict},
				{op: "setsplit", now: 6, content: "d", parts: parts("a", int64(10000))},
				{op: "earn", now: 7, eventID: "e1", content: "d", amount: 100,
					wantErr: revenue.ErrEventConflict},
			},
		},
	}
	for _, sc := range scenarios {
		t.Run(sc.name, func(t *testing.T) { runScenario(t, sc) })
	}
}

// Rejection order: only the first failing rule is reported, and a rejected
// operation changes no state, including the clock.
func TestRejectionOrder(t *testing.T) {
	scenarios := []scenario{
		{
			name: "setsplit param validation",
			wd:   0, min: 1,
			steps: []step{
				{op: "setsplit", now: 5, content: "c", parts: parts("a", int64(9999), "b", int64(1))},
				// 参数非法优先于时钟回退
				{op: "setsplit", now: 3, content: "c", parts: parts("a", int64(9998), "b", int64(1)),
					wantErr: revenue.ErrInvalidParam}, // 合计 9999
				{op: "setsplit", now: 6, content: "c", parts: parts("a", int64(10001), "b", int64(1)),
					wantErr: revenue.ErrInvalidParam}, // 合计 10002
				{op: "setsplit", now: 6, content: "c", parts: parts("a", int64(5000), "a", int64(5000)),
					wantErr: revenue.ErrInvalidParam}, // 创作者重复
				{op: "setsplit", now: 6, content: "c", parts: parts("a", int64(0), "b", int64(10000)),
					wantErr: revenue.ErrInvalidParam}, // 基点为 0
				{op: "setsplit", now: 6, content: "c", parts: parts("a", int64(10001)),
					wantErr: revenue.ErrInvalidParam}, // 基点超界且合计错
				{op: "setsplit", now: 6, content: "c", parts: []revenue.Part{},
					wantErr: revenue.ErrInvalidParam}, // 空表
				{op: "setsplit", now: 6, content: "c", parts: parts(
					"u1", int64(2000), "u2", int64(1000), "u3", int64(1000), "u4", int64(1000),
					"u5", int64(1000), "u6", int64(1000), "u7", int64(1000), "u8", int64(1000), "u9", int64(1000)),
					wantErr: revenue.ErrInvalidParam}, // 9 个创作者
				// 合法但时钟回退
				{op: "setsplit", now: 3, content: "c", parts: parts("a", int64(10000)),
					wantErr: revenue.ErrClockBack},
				// 被拒操作不推进时钟: now=5 仍被接受
				{op: "setsplit", now: 5, content: "c", parts: parts("a", int64(10000))},
			},
		},
		{
			name: "earn rejection order",
			wd:   0, min: 1,
			steps: []step{
				{op: "setsplit", now: 5, content: "c", parts: parts("a", int64(10000))},
				{op: "earn", now: 6, eventID: "e1", content: "c", amount: 100,
					wantShares: shares("a", int64(100))},
				// 参数非法 > 时钟回退
				{op: "earn", now: 3, eventID: "e2", content: "c", amount: 0,
					wantErr: revenue.ErrInvalidParam},
				{op: "earn", now: 7, eventID: "e2", content: "c", amount: 1_000_000_000_001,
					wantErr: revenue.ErrInvalidParam},
				// 时钟回退 > 内容无分成表
				{op: "earn", now: 3, eventID: "e2", content: "ghost", amount: 1,
					wantErr: revenue.ErrClockBack},
				// 内容无分成表 > 事件冲突
				{op: "earn", now: 7, eventID: "e1", content: "ghost", amount: 999,
					wantErr: revenue.ErrNoSplit},
				// 事件冲突
				{op: "earn", now: 7, eventID: "e1", content: "c", amount: 999,
					wantErr: revenue.ErrEventConflict},
				// 被拒不改状态: 时钟未推进, 份额未变
				{op: "balance", now: 6, creator: "a", wantAvail: 100},
				{op: "earn", now: 6, eventID: "e3", content: "c", amount: 1,
					wantShares: shares("a", int64(1))},
			},
		},
		{
			name: "hold and release rejection order",
			wd:   0, min: 1,
			steps: []step{
				{op: "setsplit", now: 5, content: "c", parts: parts("a", int64(10000))},
				{op: "earn", now: 6, eventID: "e1", content: "c", amount: 100,
					wantShares: shares("a", int64(100))},
				// from >= to: 参数非法, 且优先于时钟回退
				{op: "hold", now: 4, holdID: "h1", content: "c", from: 10, to: 10,
					wantErr: hold.ErrInvalidParam},
				{op: "hold", now: 6, holdID: "h1", content: "c", from: 20, to: 10,
					wantErr: hold.ErrInvalidParam},
				// 时钟回退 > 内容无分成表
				{op: "hold", now: 4, holdID: "h1", content: "ghost", from: 0, to: 1,
					wantErr: hold.ErrClockBack},
				// 内容无分成表 > 冻结已存在
				{op: "hold", now: 7, holdID: "h1", content: "c", from: 0, to: 10},
				{op: "hold", now: 8, holdID: "h1", content: "ghost", from: 0, to: 10,
					wantErr: hold.ErrNoSplit},
				// 冻结已存在
				{op: "hold", now: 8, holdID: "h1", content: "c", from: 0, to: 10,
					wantErr: hold.ErrHoldExists},
				// release: 参数非法 > 时钟回退 > 冻结不存在
				{op: "release", now: 5, holdID: "", wantErr: hold.ErrInvalidParam},
				{op: "release", now: 5, holdID: "h9", wantErr: hold.ErrClockBack},
				{op: "release", now: 9, holdID: "h9", wantErr: hold.ErrHoldNotFound},
				// 被拒不改状态: h1 仍在, 份额仍冻结
				{op: "balance", now: 9, creator: "a", wantHeld: 100},
				{op: "release", now: 9, holdID: "h1"},
				{op: "balance", now: 9, creator: "a", wantAvail: 100},
			},
		},
		{
			name: "refund rejection order",
			wd:   0, min: 1,
			steps: []step{
				{op: "setsplit", now: 5, content: "c", parts: parts("a", int64(10000))},
				{op: "earn", now: 6, eventID: "e1", content: "c", amount: 100,
					wantShares: shares("a", int64(100))},
				// 参数非法 > 时钟回退 > 事件不存在
				{op: "refund", now: 4, eventID: "", wantErr: payout.ErrInvalidParam},
				{op: "refund", now: 4, eventID: "e9", wantErr: payout.ErrClockBack},
				{op: "refund", now: 7, eventID: "e9", wantErr: payout.ErrEventNotFound},
				{op: "refund", now: 7, eventID: "e1"},
				// 已退款
				{op: "refund", now: 8, eventID: "e1", wantErr: payout.ErrAlreadyRefunded},
				// 被拒不改状态
				{op: "balance", now: 8, creator: "a"},
			},
		},
		{
			name: "settle rejection order and unknown creator",
			wd:   0, min: 1,
			steps: []step{
				{op: "setsplit", now: 5, content: "c", parts: parts("a", int64(10000))},
				{op: "settle", now: 4, creator: "", wantErr: payout.ErrInvalidParam},
				{op: "settle", now: 4, creator: "ghost", wantErr: payout.ErrClockBack},
				// 从未出现在任何分成表中
				{op: "settle", now: 6, creator: "ghost", wantErr: payout.ErrCreatorNotFound},
				// 出现在分成表但无收入: 正常结算出款 0
				{op: "settle", now: 6, creator: "a", wantPayout: 0, wantOffset: 0},
				// 被拒的 settle 不推进时钟: now=6 仍被接受
				{op: "earn", now: 6, eventID: "e1", content: "c", amount: 3,
					wantShares: shares("a", int64(3))},
			},
		},
		{
			name: "rejected op with future now does not move clock",
			wd:   0, min: 1,
			steps: []step{
				{op: "setsplit", now: 5, content: "c", parts: parts("a", int64(10000))},
				{op: "earn", now: 1000, eventID: "e1", content: "c", amount: 0,
					wantErr: revenue.ErrInvalidParam}, // 被拒, 时钟不动
				{op: "earn", now: 6, eventID: "e1", content: "c", amount: 42,
					wantShares: shares("a", int64(42))}, // now=6 仍合法
			},
		},
	}
	for _, sc := range scenarios {
		t.Run(sc.name, func(t *testing.T) { runScenario(t, sc) })
	}
}

func TestConstructorValidation(t *testing.T) {
	if _, _, _, err := ontology.New(-1, 1); !errors.Is(err, ontology.ErrInvalidParam) {
		t.Fatalf("wd=-1: %v", err)
	}
	if _, _, _, err := ontology.New(1_000_000_001, 1); !errors.Is(err, ontology.ErrInvalidParam) {
		t.Fatalf("wd too large: %v", err)
	}
	if _, _, _, err := ontology.New(0, 0); !errors.Is(err, ontology.ErrInvalidParam) {
		t.Fatalf("min=0: %v", err)
	}
	if _, _, _, err := ontology.New(0, 1_000_000_000_001); !errors.Is(err, ontology.ErrInvalidParam) {
		t.Fatalf("min too large: %v", err)
	}
	if _, _, _, err := ontology.New(1_000_000_000, 1_000_000_000_000); err != nil {
		t.Fatalf("valid bounds rejected: %v", err)
	}
}

// Concurrent calls must behave as some serial order: run mixed operations
// from many goroutines under -race and verify the books still balance.
func TestConcurrent(t *testing.T) {
	rev, hld, pay, err := ontology.New(10, 100)
	if err != nil {
		t.Fatal(err)
	}
	if err := rev.SetSplit(0, "c", parts("a", int64(5000), "b", int64(3000), "c", int64(2000))); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			base := int64(w * 1000)
			for i := 0; i < 200; i++ {
				now := base + int64(i)
				id := fmt.Sprintf("w%d-e%d", w, i)
				if _, err := rev.Earn(now, id, "c", 1000); err != nil {
					continue // 时钟回退属正常竞争结果
				}
				if i%5 == 0 {
					_ = hld.Hold(now, fmt.Sprintf("w%d-h%d", w, i), "c", now, now+1)
				}
				if i%7 == 0 {
					_ = hld.Release(now, fmt.Sprintf("w%d-h%d", w, i-i%5))
				}
				for _, cr := range []string{"a", "b", "c"} {
					_, _, _ = pay.Settle(now, cr)
					_, _, _, _ = pay.Balance(cr, now)
				}
				if i%3 == 0 {
					_ = pay.Refund(now, id)
				}
			}
		}(w)
	}
	wg.Wait()
	// 不变量: 出款要么为 0 要么 >= Min, 且各类余额非负
	for _, cr := range []string{"a", "b", "c"} {
		held, pending, avail, debt := pay.Balance(cr, 1_000_000)
		if held < 0 || pending < 0 || avail < 0 || debt < 0 {
			t.Fatalf("%s: negative balance (%d,%d,%d,%d)", cr, held, pending, avail, debt)
		}
	}
}

package remittance

import (
	"fmt"
	"math/rand"
	"testing"
)

// difftest 同时驱动 Engine 与朴素模型，逐操作比较结果。
// 所有输入、输出、判定依据均写入 test log（-v 可见）。
type difftest struct {
	t    *testing.T
	rng  *rand.Rand
	eng  *Engine
	nav  *naiveModel
	cfg  Config
	step int
}

func newDifftest(t *testing.T, seed int64) *difftest {
	cfg := Config{QuoteTTLSeconds: 500, ReviewSeconds: 120, ReviewThreshold: 50}
	eng := New(cfg)
	nav := newNaive(cfg)
	return &difftest{t: t, rng: rand.New(rand.NewSource(seed)), eng: eng, nav: nav, cfg: cfg}
}

func (d *difftest) logf(format string, args ...any) {
	d.t.Helper()
	d.t.Logf("[step %d seed-op] %s", d.step, fmt.Sprintf(format, args...))
}

func (d *difftest) sender(i int) string { return fmt.Sprintf("sender%02d", i) }

// setup 初始化若干汇款人（限额刻意很小以制造拒绝）。
func (d *difftest) setup(nSenders int) {
	for i := 0; i < nSenders; i++ {
		id := d.sender(i)
		lim := Limits{
			Single: int64(1 + d.rng.Intn(120)),
			Daily:  int64(50 + d.rng.Intn(300)),
			Annual: int64(200 + d.rng.Intn(2000)),
		}
		d.eng.AddSender(id, lim)
		d.nav.addSender(id, lim)
	}
	for _, p := range []string{"badA", "badB"} {
		d.eng.AddSanctionedPayee(p)
		d.nav.addSanction(p)
	}
}

// 随机源金额/汇率组合：同时覆盖整除、floor/ceil 差一的情形。
func (d *difftest) randomAmountRate() (int64, int64) {
	switch d.rng.Intn(4) {
	case 0:
		return int64(1 + d.rng.Intn(5)), int64(1 + d.rng.Intn(5_000_000))
	case 1:
		return int64(1 + d.rng.Intn(100)), rateScale // 整除：floor==ceil
	default:
		return int64(1 + d.rng.Intn(30)), int64(1 + d.rng.Intn(9_000_000))
	}
}

var codeNames = map[ErrorCode]string{
	0:                          "OK",
	ErrCodeInvalidArgument:     "InvalidArgument",
	ErrCodeClockBackward:       "ClockBackward",
	ErrCodeSanctionedPayee:     "SanctionedPayee",
	ErrCodeIdempotencyConflict: "IdempotencyConflict",
	ErrCodeQuoteNotFound:       "QuoteNotFound",
	ErrCodeQuoteExpired:        "QuoteExpired",
	ErrCodeSingleLimitExceeded: "SingleLimitExceeded",
	ErrCodeDailyLimitExceeded:  "DailyLimitExceeded",
	ErrCodeAnnualLimitExceeded: "AnnualLimitExceeded",
	ErrCodeTransferNotFound:    "TransferNotFound",
	ErrCodeIllegalState:        "IllegalState",
}

func codeName(c ErrorCode) string {
	if name, ok := codeNames[c]; ok {
		return name
	}
	return fmt.Sprintf("UNKNOWN(%d)", c)
}

func (d *difftest) compareErr(op string, eerr error, ncode ErrorCode) {
	d.t.Helper()
	ecode := CodeOf(eerr)
	if ecode != ncode {
		d.t.Fatalf("%s error mismatch: engine=%v(%d) naive=%d at step %d",
			op, eerr, ecode, ncode, d.step)
	}
	if eerr != nil {
		d.logf("%s output REJECT code=%s 判定依据=%v", op, codeName(ncode), eerr)
	}
}

// quoteRef 记录两侧共享的报价编号（编号生成规则一致）。
func (d *difftest) runSubmit(sender string, quoteID int64, payee, key string, now int64) {
	d.step++
	d.logf("Submit input sender=%s quoteID=%d payee=%q key=%q now=%d",
		sender, quoteID, payee, key, now)
	eres, eerr := d.eng.Submit(SubmitRequest{
		Sender: sender, QuoteID: quoteID, Payee: payee, IdemKey: key, Now: now,
	})
	nres, ncode := d.nav.submit(sender, quoteID, payee, key, now)
	d.compareErr("Submit", eerr, ncode)
	if eerr == nil {
		if eres.TransferID != nres.tid || eres.TargetAmount != nres.target ||
			eres.Occupied != nres.occ || eres.Status != nres.status ||
			eres.ReviewDeadline != nres.dl || eres.Replay != nres.replay {
			d.t.Fatalf("Submit result mismatch:\nengine=%+v\nnaive =%+v", eres, nres)
		}
		reason := "目标额<阈值, 提交即出款"
		if eres.Status == StatusPending {
			reason = fmt.Sprintf("目标额%d>=阈值%d, 待审核, deadline=%d",
				eres.TargetAmount, d.cfg.ReviewThreshold, eres.ReviewDeadline)
		}
		if eres.Replay {
			reason = "幂等参数完全一致, 返回首次原结果(不重复占用)"
		}
		d.logf("Submit output OK tid=%d target=floor=%d occupied=ceil=%d status=%s replay=%v | %s",
			eres.TransferID, eres.TargetAmount, eres.Occupied, eres.Status, eres.Replay, reason)
	}
}

func (d *difftest) runReview(kind string, tid, now int64) {
	d.step++
	d.logf("%s input tid=%d now=%d", kind, tid, now)
	var eerr error
	switch kind {
	case "Approve":
		eerr = d.eng.Approve(tid, now)
	case "Reject":
		eerr = d.eng.Reject(tid, now)
	default:
		eerr = d.eng.Withdraw(tid, now)
	}
	ncode := d.nav.review(tid, now, kind == "Approve")
	d.compareErr(kind, eerr, ncode)
	if eerr == nil {
		d.logf("%s output OK tid=%d 判定: 参数/时钟/存在/状态全通过, 已物化更早逾期单", kind, tid)
	}
}

func (d *difftest) runGet(tid, now int64) {
	d.step++
	d.logf("GetTransfer input tid=%d now=%d", tid, now)
	info, eerr := d.eng.GetTransfer(tid, now)
	nstatus, ndecided, nday, ncode := d.nav.get(tid, now)
	d.compareErr("GetTransfer", eerr, ncode)
	if eerr == nil {
		if info.Status != nstatus || info.DecidedAt != ndecided || info.Day != nday {
			d.t.Fatalf("GetTransfer mismatch engine=%+v naive(status=%d decided=%d day=%d)",
				info, nstatus, ndecided, nday)
		}
		d.logf("GetTransfer output OK tid=%d status=%s day=%d decidedAt=%d",
			tid, nstatus, nday, ndecided)
	}
}

func (d *difftest) runUsage(sender string, now int64) {
	d.step++
	d.logf("Usage input sender=%s now=%d", sender, now)
	u, eerr := d.eng.Usage(sender, now)
	nday, ndayUsed, nannual, ncode := d.nav.useQuery(sender, now)
	d.compareErr("Usage", eerr, ncode)
	if eerr == nil {
		if u.Day != nday || u.DayUsed != ndayUsed || u.AnnualUsed != nannual {
			d.t.Fatalf("Usage mismatch engine=%+v naive(day=%d dayUsed=%d annual=%d)",
				u, nday, ndayUsed, nannual)
		}
		d.logf("Usage output OK day=%d dayUsed=%d rollingYearUsed=%d 判定: 扫描全部历史活占用(朴素)对照增量桶",
			u.Day, u.DayUsed, u.AnnualUsed)
	}
}

// TestDifferentialRandom 大量随机操作序列对照。
// 固定种子 => 完全可重放；时间在“偶尔回退、经常前进、偶尔跨日/跨年”间分布。
func TestDifferentialRandom(t *testing.T) {
	const trials = 40
	for seed := int64(1); seed <= trials; seed++ {
		t.Run(fmt.Sprintf("seed=%d", seed), func(t *testing.T) {
			d := newDifftest(t, seed)
			nSenders := 2 + d.rng.Intn(4)
			d.setup(nSenders)

			var now int64
			type liveQuote struct {
				id     int64
				sender string
			}
			var quotes []liveQuote
			// 已知转账编号集合（用于随机审核/查询存在与不存在两种情况）
			maxTid := int64(0)

			keys := []string{"k1", "k2", "k3"}
			payees := []string{"alice", "bob", "carol", "badA"}

			for op := 0; op < 400; op++ {
				// 时间推进：大多数小幅前进，少数跨日/跨年，少数故意回退。
				switch r := d.rng.Intn(20); {
				case r == 0 && now > 0:
					now -= int64(1 + d.rng.Intn(5)) // 故意回退
				case r == 1:
					now += int64(86400 * (1 + d.rng.Intn(400))) // 跨日/跨年
				default:
					now += int64(d.rng.Intn(200))
				}
				if now < 0 {
					now = 0
				}

				sender := d.sender(d.rng.Intn(nSenders))
				switch d.rng.Intn(10) {
				case 0, 1, 2: // 申请报价
					amount, rate := d.randomAmountRate()
					qnow := now
					id, err := d.eng.ApplyQuote(QuoteRequest{
						Sender: sender, SourceCCY: "USD", TargetCCY: "CNY",
						Amount: amount, RatePPM: rate, Now: qnow,
					})
					if err == nil {
						nid, ncode := d.nav.applyQuote(sender, "USD", "CNY", amount, rate, qnow)
						if ncode != 0 || nid != id {
							t.Fatalf("ApplyQuote mismatch at step %d: engine id=%d err=%v naive id=%d code=%d",
								d.step+1, id, err, nid, ncode)
						}
						quotes = append(quotes, liveQuote{id: id, sender: sender})
						d.step++
						d.logf("ApplyQuote output OK quoteID=%d sender=%s amount=%d ratePPM=%d now=%d 有效期=[%d,%d]",
							id, sender, amount, rate, qnow, qnow, qnow+d.cfg.QuoteTTLSeconds)
					} else {
						_, ncode := d.nav.applyQuote(sender, "USD", "CNY", amount, rate, qnow)
						if CodeOf(err) != ncode {
							t.Fatalf("ApplyQuote reject mismatch engine=%v naive=%d", err, ncode)
						}
					}
				case 3, 4, 5, 6: // 提交
					var qid int64
					if len(quotes) > 0 && d.rng.Intn(5) != 0 {
						q := quotes[d.rng.Intn(len(quotes))]
						qid = q.id
						if d.rng.Intn(6) == 0 {
							qid = q.id + 777 // 故意不存在
						}
					} else {
						qid = int64(1 + d.rng.Intn(50))
					}
					payee := payees[d.rng.Intn(len(payees))]
					key := keys[d.rng.Intn(len(keys))]
					d.runSubmit(sender, qid, payee, key, now)
				case 7: // 审核/撤回
					tid := int64(0)
					if maxTid > 0 {
						tid = int64(1 + d.rng.Intn(int(maxTid)+3))
					}
					kinds := []string{"Approve", "Reject", "Withdraw"}
					d.runReview(kinds[d.rng.Intn(3)], tid, now)
				case 8:
					tid := int64(0)
					if maxTid > 0 {
						tid = int64(1 + d.rng.Intn(int(maxTid)+2))
					}
					d.runGet(tid, now)
				default:
					d.runUsage(sender, now)
				}

				// 更新已知最大转账编号（两侧一致）。
				maxTid = d.nav.nextXfer
			}

			// 序列结束后再用一批跨度很大的查询点双重核对占用。
			for _, probe := range []int64{0, 1, 119, 120, 121, 86399, 86400,
				364 * 86400, 365*86400 - 1, 365 * 86400, 400 * 86400, now + 1_000_000} {
				if probe < 0 {
					continue
				}
				for i := 0; i < nSenders; i++ {
					d.runUsage(d.sender(i), probe)
				}
			}
		})
	}
}

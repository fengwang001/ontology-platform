package remittance

// 随机差分测试：同一随机操作序列同时作用于被测系统、
// 独立朴素模型与另一个重放实例，三者结果必须完全一致；
// 每步打印输入、输出与判定依据（go test -v 可见）。

import (
	"errors"
	"fmt"
	"math/rand"
	"testing"
)

func diffConfigs() []Config {
	return []Config{
		{SingleLimit: 1000, DayLimit: 3000, YearLimit: 8000, QuoteTTL: 200, ReviewThreshold: 500, ReviewTimeout: 300},
		{SingleLimit: 100, DayLimit: 250, YearLimit: 600, QuoteTTL: 50, ReviewThreshold: 60, ReviewTimeout: 80},
		{SingleLimit: 5000, DayLimit: 9000, YearLimit: 20000, QuoteTTL: 0, ReviewThreshold: 0, ReviewTimeout: 0},
		{SingleLimit: 10, DayLimit: 30, YearLimit: 90, QuoteTTL: 86400 * 400, ReviewThreshold: 5, ReviewTimeout: 86400 * 500},
	}
}

// checkErr 校验两处错误一致（哨兵层面）。
func checkErr(t *testing.T, what string, got, want error) {
	t.Helper()
	if want == nil {
		if got != nil {
			t.Fatalf("%s: model ok but system err=%v", what, got)
		}
		return
	}
	if got == nil || !errors.Is(got, want) {
		t.Fatalf("%s: want %v, got %v", what, want, got)
	}
}

func TestDifferentialRandom(t *testing.T) {
	cfgs := diffConfigs()
	remitters := []string{"u0", "u1", "u2", "u3"}
	payees := []string{"p0", "p1", "p2", "bad"}

	for seed := int64(0); seed < 400; seed++ {
		cfg := cfgs[seed%int64(len(cfgs))]
		rng := rand.New(rand.NewSource(seed))
		sys, err := NewSystem(cfg)
		if err != nil {
			t.Fatalf("NewSystem: %v", err)
		}
		replay, _ := NewSystem(cfg) // 重放实例：验证相同序列结果完全相同
		model := newNaiveModel(cfg)

		var quoteIDs, remIDs []string
		now := int64(0)

		for i := 0; i < 500; i++ {
			// 时间推进：多为小步，部分跨日/跨年度，少量回退。
			switch r := rng.Intn(100); {
			case r < 55:
				now += int64(rng.Intn(50))
			case r < 70:
				now += 86400 * (1 + int64(rng.Intn(400)))
			case r < 80:
				// 原地不动
			case r < 90:
				now -= int64(rng.Intn(100))
				if now < 0 {
					now = 0
				}
			default:
				now += int64(rng.Intn(1000))
			}

			op := rng.Intn(100)
			switch {
			case op < 22: // 申请报价
				remitter := remitters[rng.Intn(len(remitters))]
				src := int64(rng.Intn(1200) - 5)   // 少量非正数触发参数非法
				rate := int64(rng.Intn(2_000_000)) // 含 0 触发参数非法
				id, err1 := sys.RequestQuote(remitter, src, rate, now)
				id2, err2 := replay.RequestQuote(remitter, src, rate, now)
				mid, merr, reason := model.RequestQuote(remitter, src, rate, now)
				checkErr(t, "RequestQuote", err1, merr)
				checkErr(t, "RequestQuote(replay)", err2, merr)
				if merr == nil && (id != mid || id2 != mid) {
					t.Fatalf("quote id mismatch: sys=%q replay=%q model=%q", id, id2, mid)
				}
				if merr == nil {
					quoteIDs = append(quoteIDs, mid)
				}
				t.Logf("seed=%d i=%d RequestQuote(%q,%d,%d,now=%d) -> id=%q err=%v | %s",
					seed, i, remitter, src, rate, now, mid, merr, reason)

			case op < 55: // 提交汇款
				remitter := remitters[rng.Intn(len(remitters))]
				payee := payees[rng.Intn(len(payees))]
				qid := "Q-999"
				if len(quoteIDs) > 0 && rng.Intn(100) < 90 {
					qid = quoteIDs[rng.Intn(len(quoteIDs))]
				}
				key := fmt.Sprintf("k%d", rng.Intn(10)) // 小键池制造重放与冲突
				res, err1 := sys.Submit(remitter, key, qid, payee, now)
				res2, err2 := replay.Submit(remitter, key, qid, payee, now)
				mres, merr, reason := model.Submit(remitter, key, qid, payee, now)
				checkErr(t, "Submit", err1, merr)
				checkErr(t, "Submit(replay)", err2, merr)
				if merr == nil && (res != mres || res2 != mres) {
					t.Fatalf("submit result mismatch: sys=%+v replay=%+v model=%+v", res, res2, mres)
				}
				if merr == nil {
					remIDs = append(remIDs, mres.RemittanceID)
				}
				t.Logf("seed=%d i=%d Submit(%q,%q,%q,%q,now=%d) -> %+v err=%v | %s",
					seed, i, remitter, key, qid, payee, now, mres, merr, reason)

			case op < 68: // 审核：批准/拒绝/撤回
				rid := "R-999"
				if len(remIDs) > 0 && rng.Intn(100) < 90 {
					rid = remIDs[rng.Intn(len(remIDs))]
				}
				var err1, err2, merr error
				var action, reason string
				switch rng.Intn(3) {
				case 0:
					action = "Approve"
					err1 = sys.Approve(rid, now)
					err2 = replay.Approve(rid, now)
					merr, reason = model.Approve(rid, now)
				case 1:
					action = "Reject"
					err1 = sys.Reject(rid, now)
					err2 = replay.Reject(rid, now)
					merr, reason = model.Reject(rid, now)
				default:
					action = "Withdraw"
					err1 = sys.Withdraw(rid, now)
					err2 = replay.Withdraw(rid, now)
					merr, reason = model.Withdraw(rid, now)
				}
				checkErr(t, action, err1, merr)
				checkErr(t, action+"(replay)", err2, merr)
				t.Logf("seed=%d i=%d %s(%q,now=%d) -> err=%v | %s", seed, i, action, rid, now, merr, reason)

			case op < 82: // 查询占用
				remitter := remitters[rng.Intn(len(remitters))]
				u, err1 := sys.QueryUsage(remitter, now)
				u2, err2 := replay.QueryUsage(remitter, now)
				mu, merr, reason := model.QueryUsage(remitter, now)
				checkErr(t, "QueryUsage", err1, merr)
				checkErr(t, "QueryUsage(replay)", err2, merr)
				if merr == nil && (u != mu || u2 != mu) {
					t.Fatalf("usage mismatch: sys=%+v replay=%+v model=%+v", u, u2, mu)
				}
				if merr == nil && (u.DayUsed > cfg.DayLimit || u.RollingYearUsed > cfg.YearLimit) {
					t.Fatalf("limit invariant violated: %+v cfg=%+v", u, cfg)
				}
				t.Logf("seed=%d i=%d QueryUsage(%q,now=%d) -> %+v err=%v | %s",
					seed, i, remitter, now, mu, merr, reason)

			case op < 92: // 查询汇款状态
				rid := "R-999"
				if len(remIDs) > 0 && rng.Intn(100) < 90 {
					rid = remIDs[rng.Intn(len(remIDs))]
				}
				v, err1 := sys.GetRemittance(rid, now)
				v2, err2 := replay.GetRemittance(rid, now)
				mv, merr, reason := model.GetRemittance(rid, now)
				checkErr(t, "GetRemittance", err1, merr)
				checkErr(t, "GetRemittance(replay)", err2, merr)
				if merr == nil && (v != mv || v2 != mv) {
					t.Fatalf("view mismatch: sys=%+v replay=%+v model=%+v", v, v2, mv)
				}
				t.Logf("seed=%d i=%d GetRemittance(%q,now=%d) -> %+v err=%v | %s",
					seed, i, rid, now, mv, merr, reason)

			default: // 制裁名单管理
				p := payees[rng.Intn(len(payees))]
				if rng.Intn(2) == 0 {
					sys.AddSanctionedPayee(p)
					replay.AddSanctionedPayee(p)
					model.AddSanctionedPayee(p)
					t.Logf("seed=%d i=%d AddSanctionedPayee(%q)", seed, i, p)
				} else {
					sys.RemoveSanctionedPayee(p)
					replay.RemoveSanctionedPayee(p)
					model.RemoveSanctionedPayee(p)
					t.Logf("seed=%d i=%d RemoveSanctionedPayee(%q)", seed, i, p)
				}
			}
		}
	}
}

package subro_test

import (
	"errors"
	"fmt"
	"math/rand"
	"testing"

	"ontology/subro"
	"ontology/subro/ledger"
	"ontology/subro/naive"
)

func errKind(err error) naive.ErrKind {
	switch {
	case err == nil:
		return naive.OK
	case errors.Is(err, subro.ErrInvalidParam):
		return naive.InvalidParam
	case errors.Is(err, subro.ErrClockRollback):
		return naive.ClockRollback
	case errors.Is(err, subro.ErrCaseNotFound):
		return naive.CaseNotFound
	case errors.Is(err, subro.ErrExpired):
		return naive.Expired
	case errors.Is(err, subro.ErrExceedsTotalLoss):
		return naive.ExceedsTotalLoss
	case errors.Is(err, subro.ErrAlreadyWaived):
		return naive.AlreadyWaived
	default:
		return "unknown"
	}
}

func adjStrings(in []ledger.Adjustment) []string {
	out := make([]string, len(in))
	for i, a := range in {
		out[i] = fmt.Sprintf("%s:%s:%d@%d/%s", a.Party, a.Direction, a.Amount, a.Now, a.Cause)
	}
	return out
}

// TestDifferentialRandom 把大量随机操作序列同时施加到正式实现与
// 独立朴素模型上，逐步比对错误类别、应得、已发放与调整记录，
// 并校验不变式：三方应得之和恒等于净回收总额。
func TestDifferentialRandom(t *testing.T) {
	ratios := []int64{0, 1, 2500, 5000, 9999, 10000}

	for seed := int64(0); seed < 20; seed++ {
		rng := rand.New(rand.NewSource(seed))
		svc := subro.NewService()
		ref := naive.New()
		ids := []string{"alpha", "beta", "gamma"}

		now := int64(0)
		for _, id := range ids {
			totalLoss := rng.Int63n(20000)
			paid := rng.Int63n(totalLoss + 1)
			deadline := 30 + rng.Int63n(40)
			ratio := ratios[rng.Intn(len(ratios))]
			if rng.Intn(3) == 0 {
				ratio = rng.Int63n(10001)
			}
			if err := svc.RegisterCase(now, subro.CaseInput{
				CaseID: id, TotalLoss: totalLoss, InsurerPaid: paid, Deadline: deadline, RatioBP: ratio,
			}); err != nil {
				t.Fatalf("seed=%d 登记案件 %s 失败: %v", seed, id, err)
			}
			if k := ref.Register(now, id, totalLoss, paid, deadline, ratio); k != naive.OK {
				t.Fatalf("seed=%d 朴素模型登记案件 %s 失败: %v", seed, id, k)
			}
		}

		pickCase := func() string {
			if rng.Intn(10) == 0 {
				return "ghost"
			}
			return ids[rng.Intn(len(ids))]
		}
		advance := func() {
			if rng.Intn(20) == 0 {
				now -= 1 + rng.Int63n(3) // 偶发时钟回退，双方应一致拒绝
				return
			}
			now += []int64{0, 0, 1, 1, 2, 3, 7}[rng.Intn(7)]
		}

		for step := 0; step < 400; step++ {
			advance()
			kind := rng.Intn(100)
			id := pickCase()

			var gotErr error
			var wantKind naive.ErrKind
			var res subro.OpResult
			var opDesc string

			switch {
			case kind < 45: // 回收
				gross := []int64{0, 1, 100, rng.Int63n(3000)}[rng.Intn(4)]
				var fee int64
				switch rng.Intn(10) {
				case 0, 1:
					fee = gross // 费用恰等于毛额
				case 2:
					fee = gross + 1 + rng.Int63n(100) // 非法：费用大于毛额
				case 3:
					fee = 0
				default:
					fee = rng.Int63n(gross + 1)
				}
				opDesc = fmt.Sprintf("recover(now=%d,case=%s,gross=%d,fee=%d)", now, id, gross, fee)
				res, gotErr = svc.Recover(now, id, gross, fee)
				wantKind = ref.Recover(now, id, gross, fee)
			case kind < 65: // 调整责任比例
				ratio := ratios[rng.Intn(len(ratios))]
				switch rng.Intn(10) {
				case 0:
					ratio = rng.Int63n(10001)
				case 1:
					ratio = 10001 + rng.Int63n(100) // 非法：越界
				}
				opDesc = fmt.Sprintf("adjust_ratio(now=%d,case=%s,ratio=%d)", now, id, ratio)
				res, gotErr = svc.AdjustRatio(now, id, ratio)
				wantKind = ref.AdjustRatio(now, id, ratio)
			case kind < 80: // 补充赔付
				amount := int64(1 + rng.Int63n(2000))
				switch rng.Intn(10) {
				case 0:
					amount = 100000 // 超出总损失额
				case 1:
					amount = 0 // 非法：非正
				}
				opDesc = fmt.Sprintf("supplement(now=%d,case=%s,amount=%d)", now, id, amount)
				res, gotErr = svc.Supplement(now, id, amount)
				wantKind = ref.Supplement(now, id, amount)
			default: // 放弃追偿权
				opDesc = fmt.Sprintf("waive(now=%d,case=%s)", now, id)
				res, gotErr = svc.Waive(now, id)
				wantKind = ref.Waive(now, id)
			}

			if gotKind := errKind(gotErr); gotKind != wantKind {
				t.Fatalf("seed=%d step=%d op=%s: 错误类别不一致 正式=%s 朴素=%s",
					seed, step, opDesc, gotKind, wantKind)
			}

			snap, ok := svc.Snapshot(id)
			if ok {
				t.Logf("seed=%d step=%d op=%s -> err=%v ent=%v adj=%v", seed, step, opDesc, errKind(gotErr), res.Entitlements, adjStrings(res.Adjustments))
				t.Logf("  判定依据: netTotal=%d cap=%d distributable=%d uncompensated=%d paid=%d waived=%v",
					snap.NetTotal, snap.Cap, snap.Distributable, snap.Uncompensated, snap.InsurerPaid, snap.Waived)
			}

			if gotErr != nil || !ok {
				continue // 被拒绝或案件不存在：双方已一致，无状态可比
			}

			rc := ref.Cases[id]
			wantEnt := rc.Entitled()
			gotEnt := res.Entitlements
			if gotEnt.Insured != wantEnt[naive.PInsured] ||
				gotEnt.Insurer != wantEnt[naive.PInsurer] ||
				gotEnt.ThirdParty != wantEnt[naive.PThird] {
				t.Fatalf("seed=%d step=%d op=%s: 应得不一致 正式=%+v 朴素=%v",
					seed, step, opDesc, gotEnt, wantEnt)
			}
			if res.Disbursed.Insured != rc.Disbursed[naive.PInsured] ||
				res.Disbursed.Insurer != rc.Disbursed[naive.PInsurer] ||
				res.Disbursed.ThirdParty != rc.Disbursed[naive.PThird] {
				t.Fatalf("seed=%d step=%d op=%s: 已发放不一致 正式=%+v 朴素=%v",
					seed, step, opDesc, res.Disbursed, rc.Disbursed)
			}
			// 不变式：三方应得之和加超额退还额等于净回收总额
			// （超额退还额即第三方应得，已含在求和内）。
			if sum := gotEnt.Insured + gotEnt.Insurer + gotEnt.ThirdParty; sum != snap.NetTotal {
				t.Fatalf("seed=%d step=%d op=%s: 不变式破坏 应得之和=%d 净回收总额=%d",
					seed, step, opDesc, sum, snap.NetTotal)
			}
			if res.Disbursed != gotEnt {
				t.Fatalf("seed=%d step=%d op=%s: 结清后已发放应等于应得", seed, step, opDesc)
			}
			// 比对本次操作产生的调整记录（按方/向/额/时间/原因）。
			if len(res.Adjustments) > 3 {
				t.Fatalf("seed=%d step=%d op=%s: 单次操作调整记录超过 3 条", seed, step, opDesc)
			}
			naiveAdjs := rc.Adjs[len(rc.Adjs)-len(res.Adjustments):]
			for i, a := range res.Adjustments {
				na := naiveAdjs[i]
				if a.Party.String() != na.Party || a.Direction.String() != na.Dir ||
					a.Amount != na.Amount || a.Now != na.Now || a.Cause != na.Cause {
					t.Fatalf("seed=%d step=%d op=%s: 调整记录[%d]不一致 正式=%+v 朴素=%+v",
						seed, step, opDesc, i, a, na)
				}
			}
			// 聚合状态一致性。
			if snap.NetTotal != rc.NetTotal() || snap.InsurerPaid != rc.Paid ||
				snap.Waived != rc.Waived || snap.RatioBP != rc.RatioBP ||
				snap.RecoveryCount != len(rc.Recs) {
				t.Fatalf("seed=%d step=%d op=%s: 聚合状态不一致 正式=%+v 朴素=%+v",
					seed, step, opDesc, snap, rc)
			}
			// 调整记录总数一致性。
			if len(snap.Adjustments) != len(rc.Adjs) {
				t.Fatalf("seed=%d step=%d op=%s: 调整记录总数不一致 正式=%d 朴素=%d",
					seed, step, opDesc, len(snap.Adjustments), len(rc.Adjs))
			}
		}
		t.Logf("seed=%d 完成 400 步随机对照, 时钟 now=%d", seed, now)
	}
}

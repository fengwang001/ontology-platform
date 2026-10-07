package ncd

import (
	"fmt"
	"math/rand"
	"reflect"
	"testing"
)

func codeOf(err error) Code {
	if err == nil {
		return 0
	}
	if ce, ok := err.(*Error); ok {
		return ce.Code
	}
	return -1
}

func engineYearLevels(t *testing.T, e *Engine, id string) []int {
	t.Helper()
	years, err := e.Years(id)
	if err != nil {
		return nil
	}
	out := make([]int, len(years))
	for i, y := range years {
		out[i] = y.Level
	}
	return out
}

// 随机出险、续保、保护、转移与迟报序列：引擎与朴素模型逐步对照，
// 日志打印输入、输出与判定依据。
func TestDifferentialRandom(t *testing.T) {
	seeds := []int64{1, 7, 42, 99, 2024, 31337, 88888, 123456}
	for _, seed := range seeds {
		t.Run(fmt.Sprintf("seed=%d", seed), func(t *testing.T) {
			rng := rand.New(rand.NewSource(seed))
			cfg := Config{
				MaxLevel:         3 + rng.Intn(5),
				RenewalGraceDays: 5 + rng.Intn(20),
				LiableThreshold:  20 + rng.Intn(61),
			}
			cfg.ProtectStartLevel = rng.Intn(cfg.MaxLevel + 1)
			cfg.Premiums = make([]int64, cfg.MaxLevel+1)
			for i := range cfg.Premiums {
				cfg.Premiums[i] = int64(2000 - 150*i)
			}
			t.Logf("配置: %+v", cfg)
			eng := mustEngine(t, cfg)
			nav := newNaive(cfg)
			ids := []string{"甲", "乙", "丙"}
			now := 0
			compare := func(step int, op string) {
				t.Helper()
				for _, id := range ids {
					el := engineYearLevels(t, eng, id)
					nl := nav.yearLevels(id)
					if !reflect.DeepEqual(el, nl) {
						t.Fatalf("step %d %s: 被保人 %s 等级轨迹不一致 引擎=%v 朴素=%v", step, op, id, el, nl)
					}
					es, _ := eng.Surcharges(id)
					ns := nav.allSurcharges(id)
					if len(es) != 0 || len(ns) != 0 {
						if !reflect.DeepEqual(es, ns) {
							t.Fatalf("step %d %s: 被保人 %s 追补不一致 引擎=%v 朴素=%v", step, op, id, es, ns)
						}
					}
				}
			}
			for step := 0; step < 400; step++ {
				now += rng.Intn(50)
				id := ids[rng.Intn(len(ids))]
				var errE, errN error
				var op, basis string
				switch rng.Intn(7) {
				case 0: // 新保/中断后重保
					pid := fmt.Sprintf("P%d", step)
					errE = eng.Insure(id, pid, now)
					errN = nav.Insure(id, pid, now)
					op = fmt.Sprintf("Insure(%s,%s,%d)", id, pid, now)
				case 1, 2: // 续保：半数概率瞄准窗口
					day := now
					if years, err := eng.Years(id); err == nil && rng.Intn(2) == 0 {
						expiry := years[len(years)-1].Start + policyYearDays
						lo, hi := expiry-earlyRenewalDays, expiry+cfg.RenewalGraceDays
						if hi >= now {
							day = max(lo+rng.Intn(hi-lo+1), now)
							basis = fmt.Sprintf("窗口[%d,%d]", lo, hi)
						}
					}
					var lvE, lvN int
					lvE, errE = eng.Renew(id, day)
					lvN, errN = nav.Renew(id, day)
					op = fmt.Sprintf("Renew(%s,%d)->等级(%d,%d) %s", id, day, lvE, lvN, basis)
					if errE == nil && errN == nil && lvE != lvN {
						t.Fatalf("step %d %s: 续保等级不一致", step, op)
					}
				case 3, 4: // 出险（含迟报）：事故日取已承保年度或随机
					accidentDay := rng.Intn(now + 400)
					if years, err := eng.Years(id); err == nil && rng.Intn(2) == 0 {
						y := years[rng.Intn(len(years))]
						accidentDay = y.Start + rng.Intn(policyYearDays)
						if accidentDay > now {
							accidentDay = now
						}
						basis = fmt.Sprintf("归入年度起点%d", y.Start)
					}
					ratio := rng.Intn(120) - 5              // 偶发越界，覆盖参数非法
					acc := fmt.Sprintf("A%d", rng.Intn(24)) // 小编号池，制造重复
					errE = eng.ReportClaim(id, acc, accidentDay, ratio, now)
					errN = nav.ReportClaim(id, acc, accidentDay, ratio, now)
					op = fmt.Sprintf("ReportClaim(%s,%s,事故日%d,比例%d,%d) 门槛%d %s",
						id, acc, accidentDay, ratio, now, cfg.LiableThreshold, basis)
				case 5: // 撤销
					acc := fmt.Sprintf("A%d", rng.Intn(24))
					errE = eng.WithdrawClaim(id, acc, now)
					errN = nav.WithdrawClaim(id, acc, now)
					op = fmt.Sprintf("WithdrawClaim(%s,%s,%d)", id, acc, now)
				case 6: // 保护 / 转移
					if rng.Intn(2) == 0 {
						errE = eng.BuyProtection(id, now)
						errN = nav.BuyProtection(id, now)
						op = fmt.Sprintf("BuyProtection(%s,%d) 起始级%d", id, now, cfg.ProtectStartLevel)
					} else {
						pid := fmt.Sprintf("T%d", step)
						errE = eng.Transfer(id, pid, now)
						errN = nav.Transfer(id, pid, now)
						op = fmt.Sprintf("Transfer(%s,%s,%d)", id, pid, now)
					}
				}
				if codeOf(errE) != codeOf(errN) {
					t.Fatalf("step %d %s: 判定不一致 引擎=%v 朴素=%v", step, op, errE, errN)
				}
				t.Logf("step %03d now=%d %s => %s", step, now, op, codeOf(errE))
				compare(step, op)
			}
			for _, id := range ids {
				lv, _ := eng.Level(id)
				es, _ := eng.Surcharges(id)
				t.Logf("终态 被保人%s: 等级=%d 轨迹=%v 追补=%v", id, lv, engineYearLevels(t, eng, id), es)
			}
		})
	}
}

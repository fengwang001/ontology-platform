package settlement_test

import (
	"fmt"
	"math/rand"
	"testing"

	"ontology/settlement"
	"ontology/settlement/naive"
)

// op 一条随机生成的操作，重放时同时喂给引擎与朴素模型。
type op struct {
	kind       string // "tx" 或 "settle"
	now        int64
	merchantID string
	txID       string
	day        int64
	amount     int64
}

func (o op) String() string {
	if o.kind == "tx" {
		return fmt.Sprintf("PostTransaction(now=%d, m=%q, id=%q, day=%d, amount=%d)",
			o.now, o.merchantID, o.txID, o.day, o.amount)
	}
	return fmt.Sprintf("Settle(now=%d, m=%q, d=%d)", o.now, o.merchantID, o.day)
}

func assertSameError(t *testing.T, errE, errM error, ctx string) {
	t.Helper()
	if (errE == nil) != (errM == nil) {
		t.Fatalf("%s: engine err=%v, naive err=%v", ctx, errE, errM)
	}
	if errE != nil && codeOf(t, errE) != naive.CodeOf(errM) {
		t.Fatalf("%s: engine code=%v, naive code=%v", ctx, codeOf(t, errE), naive.CodeOf(errM))
	}
}

// TestDifferentialAgainstNaiveModel 用大量随机操作序列对照优化实现与独立朴素模型：
// 每个操作后比较错误码与出款记录，结束后比较完整状态快照与不变式。
// 日志打印输入、输出与判定依据（go test -v 可见）。
func TestDifferentialAgainstNaiveModel(t *testing.T) {
	const seeds = 40
	for seed := int64(0); seed < seeds; seed++ {
		t.Run(fmt.Sprintf("seed=%d", seed), func(t *testing.T) {
			rng := rand.New(rand.NewSource(seed))

			// 随机营业日日历：1..40，每天 4/5 概率为营业日，保证非空。
			var days []int64
			for d := int64(1); d <= 40; d++ {
				if rng.Intn(5) != 0 {
					days = append(days, d)
				}
			}
			if len(days) == 0 {
				days = append(days, 1)
			}
			cal := settlement.NewCalendar(days)
			t.Logf("日历营业日: %v", days)

			eng := settlement.NewEngine(cal)
			model := naive.New(cal)

			// 随机 1..3 个商户，配置覆盖 N/bps/H 的各种组合。
			allIDs := []string{"m0", "m1", "m2"}
			merchants := allIDs[:1+rng.Intn(3)]
			now := int64(0)
			for _, id := range merchants {
				cfg := settlement.Config{
					DelayDays:   1 + rng.Intn(3),
					ReserveBps:  []int{0, 500, 1000, 5000, 10000}[rng.Intn(5)],
					HorizonDays: 1 + rng.Intn(4),
				}
				now += int64(rng.Intn(2))
				errE := eng.AddMerchant(now, id, cfg)
				errM := model.AddMerchant(now, id, cfg)
				assertSameError(t, errE, errM, "AddMerchant")
				t.Logf("op AddMerchant(now=%d, id=%q, cfg=%+v) -> engine=%v naive=%v 判定: 错误码一致",
					now, id, cfg, errE, errM)
			}

			// 随机操作序列：流水与结算交替，now 偶发回退，
			// 发生日围绕 now 波动以覆盖日期非法与已封账，编号取自小池以覆盖重复。
			const ops = 200
			for i := 0; i < ops; i++ {
				if rng.Intn(20) == 0 {
					now -= int64(rng.Intn(3))
				} else {
					now += int64(rng.Intn(3))
				}
				id := merchants[rng.Intn(len(merchants))]
				if rng.Intn(2) == 0 {
					o := op{kind: "tx", now: now, merchantID: id}
					o.day = now - int64(rng.Intn(10)) + int64(rng.Intn(3))
					if o.day < 1 {
						o.day = 1
					}
					o.amount = int64(rng.Intn(40001) - 20000)
					o.txID = fmt.Sprintf("tx-%d", rng.Intn(ops/2))
					errE := eng.PostTransaction(o.now, o.merchantID, o.txID, o.day, o.amount)
					errM := model.PostTransaction(o.now, o.merchantID, o.txID, o.day, o.amount)
					assertSameError(t, errE, errM, o.String())
					t.Logf("op %d: %s -> engine=%v naive=%v 判定: 错误码一致", i, o, errE, errM)
				} else {
					o := op{kind: "settle", now: now, merchantID: id, day: int64(1 + rng.Intn(45))}
					recE, errE := eng.Settle(o.now, o.merchantID, o.day)
					recM, errM := model.Settle(o.now, o.merchantID, o.day)
					assertSameError(t, errE, errM, o.String())
					if errE == nil {
						if len(recE) != len(recM) {
							t.Fatalf("%s: 记录数 engine=%d naive=%d", o, len(recE), len(recM))
						}
						for j := range recE {
							if recE[j] != recM[j] {
								t.Fatalf("%s: 第 %d 条记录不一致\nengine: %+v\nnaive:  %+v",
									o, j, recE[j], recM[j])
							}
						}
						t.Logf("op %d: %s -> 双双接受, %d 条出款记录逐字段相等", i, o, len(recE))
					} else {
						t.Logf("op %d: %s -> 双双拒绝, 错误码一致", i, o)
					}
				}
			}

			// 终态：逐商户比较完整快照并校验资金不变式。
			for _, id := range merchants {
				se, err := eng.Snapshot(id)
				mustOK(t, err)
				sm, err := model.Snapshot(id)
				mustOK(t, err)
				if se.LastSettledDay != sm.LastSettledDay ||
					se.CumulativePayout != sm.CumulativePayout ||
					se.ReserveBalance != sm.ReserveBalance ||
					se.Carry != sm.Carry ||
					se.SettledTxSum != sm.SettledTxSum ||
					se.PendingTxCount != sm.PendingTxCount ||
					len(se.Batches) != len(sm.Batches) {
					t.Fatalf("merchant %q snapshot differs:\nengine: %+v\nnaive:  %+v", id, se, sm)
				}
				for j := range se.Batches {
					if se.Batches[j] != sm.Batches[j] {
						t.Fatalf("merchant %q batch %d differs: %+v vs %+v", id, j, se.Batches[j], sm.Batches[j])
					}
				}
				checkInvariant(t, eng, id)
				t.Logf("merchant %q 终态一致: payout=%d reserve=%d carry=%d settled=%d pending=%d batches=%d 判定: 快照逐字段相等且不变式成立",
					id, se.CumulativePayout, se.ReserveBalance, se.Carry, se.SettledTxSum, se.PendingTxCount, len(se.Batches))
			}
		})
	}
}

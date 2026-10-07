package inventory_test

import (
	"fmt"
	"math/rand"
	"reflect"
	"testing"

	"ontology/inventory"
	"ontology/inventory/naive"
)

// outcome 记录一次操作的输出，用于双实现对照与重放比对。
type outcome struct {
	kind     string // "ok" / "reject" / "value"
	reason   inventory.Reason
	line     int
	value    int64
	commit   *inventory.CommitResult
	details  []inventory.ReservationDetail
	hasError bool
}

// step 是一个可序列化的随机操作。
type step struct {
	desc string
	// applyReal/applyNaive 分别作用于两个实现，返回可比较的输出。
	applyReal  func(s *inventory.System) outcome
	applyNaive func(s *naive.System) outcome
}

// genSequence 生成确定性的随机操作序列（同一 seed 序列相同）。
// 仓库 W1/W2/W3 的优先序号故意取 2/1/3，使优先序与名称序不同。
func genSequence(rng *rand.Rand, n int) []step {
	warehouses := []string{"W1", "W2", "W3"}
	products := []string{"P1", "P2", "P3"}
	var orderPool []string
	for i := 0; i < 8; i++ {
		orderPool = append(orderPool, fmt.Sprintf("O%d", i))
	}
	var inboundIDs []string
	inboundSeq := 0
	var now int64
	steps := make([]step, 0, n)
	for i := 0; i < n; i++ {
		// 时间只前进不回退；少数操作故意携带过去的时刻以触发时钟回退。
		now += rng.Int63n(4)
		at := now
		if rng.Intn(10) == 0 && at >= 2 {
			at--
		}
		wh := warehouses[rng.Intn(len(warehouses))]
		p := products[rng.Intn(len(products))]

		switch rng.Intn(9) {
		case 0: // 增加现货
			qty := 1 + rng.Int63n(10)
			steps = append(steps, step{
				desc:       fmt.Sprintf("AddOnHand(%s,%s,%d,@%d)", wh, p, qty, at),
				applyReal:  func(s *inventory.System) outcome { return errOutcome(s.AddOnHand(wh, p, qty, at)) },
				applyNaive: func(s *naive.System) outcome { return errOutcome(s.AddOnHand(wh, p, qty, at)) },
			})
		case 1: // 登记计划入库
			id := fmt.Sprintf("I%d", inboundSeq)
			inboundSeq++
			inboundIDs = append(inboundIDs, id)
			arrival := at + rng.Int63n(16)
			qty := 1 + rng.Int63n(10)
			steps = append(steps, step{
				desc: fmt.Sprintf("AddPlannedInbound(%s,%s,%s,arrival=%d,qty=%d,@%d)", wh, p, id, arrival, qty, at),
				applyReal: func(s *inventory.System) outcome {
					return errOutcome(s.AddPlannedInbound(wh, p, id, arrival, qty, at))
				},
				applyNaive: func(s *naive.System) outcome {
					return errOutcome(s.AddPlannedInbound(wh, p, id, arrival, qty, at))
				},
			})
		case 2: // 确认到货（可能不存在或已到货）
			id := "I-X"
			if len(inboundIDs) > 0 && rng.Intn(4) > 0 {
				id = inboundIDs[rng.Intn(len(inboundIDs))]
			}
			steps = append(steps, step{
				desc:       fmt.Sprintf("ConfirmArrival(%s,%s,%s,@%d)", wh, p, id, at),
				applyReal:  func(s *inventory.System) outcome { return errOutcome(s.ConfirmArrival(wh, p, id, at)) },
				applyNaive: func(s *naive.System) outcome { return errOutcome(s.ConfirmArrival(wh, p, id, at)) },
			})
		case 3, 4: // 订单承诺
			orderID := orderPool[rng.Intn(len(orderPool))]
			nLines := 1 + rng.Intn(3)
			lines := make([]inventory.OrderLine, nLines)
			for j := range lines {
				lines[j] = inventory.OrderLine{Product: products[rng.Intn(len(products))], Qty: 1 + rng.Int63n(8)}
			}
			allowSplit := rng.Intn(2) == 0
			reserveFor := rng.Int63n(13)
			maxWH := 1 + rng.Intn(3)
			steps = append(steps, step{
				desc: fmt.Sprintf("CommitOrder(%s,%v,@%d,split=%v,reserve=%d,maxWH=%d)",
					orderID, lines, at, allowSplit, reserveFor, maxWH),
				applyReal: func(s *inventory.System) outcome {
					res, err := s.CommitOrder(orderID, lines, at, allowSplit, reserveFor, maxWH)
					return commitOutcome(res, err)
				},
				applyNaive: func(s *naive.System) outcome {
					res, err := s.CommitOrder(orderID, lines, at, allowSplit, reserveFor, maxWH)
					return commitOutcome(res, err)
				},
			})
		case 5: // 确认出库
			orderID := orderPool[rng.Intn(len(orderPool))]
			steps = append(steps, step{
				desc:       fmt.Sprintf("ConfirmOutbound(%s,@%d)", orderID, at),
				applyReal:  func(s *inventory.System) outcome { return errOutcome(s.ConfirmOutbound(orderID, at)) },
				applyNaive: func(s *naive.System) outcome { return errOutcome(s.ConfirmOutbound(orderID, at)) },
			})
		case 6: // 释放预留
			orderID := orderPool[rng.Intn(len(orderPool))]
			steps = append(steps, step{
				desc:       fmt.Sprintf("ReleaseReservation(%s,@%d)", orderID, at),
				applyReal:  func(s *inventory.System) outcome { return errOutcome(s.ReleaseReservation(orderID, at)) },
				applyNaive: func(s *naive.System) outcome { return errOutcome(s.ReleaseReservation(orderID, at)) },
			})
		case 7: // ATP 查询
			q := at + rng.Int63n(6)
			steps = append(steps, step{
				desc: fmt.Sprintf("ATP(%s,%s,@%d)", wh, p, q),
				applyReal: func(s *inventory.System) outcome {
					v, err := s.ATP(wh, p, q)
					return valueOutcome(v, err)
				},
				applyNaive: func(s *naive.System) outcome {
					v, err := s.ATP(wh, p, q)
					return valueOutcome(v, err)
				},
			})
		case 8: // 预留明细查询
			orderID := orderPool[rng.Intn(len(orderPool))]
			steps = append(steps, step{
				desc: fmt.Sprintf("ReservationDetails(%s)", orderID),
				applyReal: func(s *inventory.System) outcome {
					d, err := s.ReservationDetails(orderID)
					return detailsOutcome(d, err)
				},
				applyNaive: func(s *naive.System) outcome {
					d, err := s.ReservationDetails(orderID)
					return detailsOutcome(d, err)
				},
			})
		}
	}
	return steps
}

func errOutcome(err *inventory.Error) outcome {
	if err == nil {
		return outcome{kind: "ok", reason: -1, line: -2}
	}
	return outcome{kind: "reject", reason: err.Reason, line: err.Line, hasError: true}
}

func commitOutcome(res *inventory.CommitResult, err *inventory.Error) outcome {
	if err == nil {
		return outcome{kind: "ok", reason: -1, line: -2, commit: res}
	}
	return outcome{kind: "reject", reason: err.Reason, line: err.Line, hasError: true}
}

func valueOutcome(v int64, err *inventory.Error) outcome {
	if err == nil {
		return outcome{kind: "value", reason: -1, line: -2, value: v}
	}
	return outcome{kind: "reject", reason: err.Reason, line: err.Line, hasError: true}
}

func detailsOutcome(d []inventory.ReservationDetail, err *inventory.Error) outcome {
	if err == nil {
		return outcome{kind: "value", reason: -1, line: -2, details: d}
	}
	return outcome{kind: "reject", reason: err.Reason, line: err.Line, hasError: true}
}

func newRealSystem(t *testing.T) *inventory.System {
	t.Helper()
	s := inventory.NewSystem()
	// 优先序号与名称序故意不同。
	for i, prio := range []int{2, 1, 3} {
		if err := s.AddWarehouse(fmt.Sprintf("W%d", i+1), prio); err != nil {
			t.Fatalf("AddWarehouse: %v", err)
		}
	}
	return s
}

func newNaiveSystem(t *testing.T) *naive.System {
	t.Helper()
	s := naive.New()
	for i, prio := range []int{2, 1, 3} {
		if err := s.AddWarehouse(fmt.Sprintf("W%d", i+1), prio); err != nil {
			t.Fatalf("AddWarehouse: %v", err)
		}
	}
	return s
}

// equalOutcome 比较两个实现的输出是否一致（分配方案逐条相同）。
func equalOutcome(a, b outcome) bool {
	if a.kind != b.kind || a.reason != b.reason || a.line != b.line || a.value != b.value {
		return false
	}
	if (a.commit == nil) != (b.commit == nil) {
		return false
	}
	if a.commit != nil {
		if a.commit.OrderID != b.commit.OrderID || a.commit.ExpireAt != b.commit.ExpireAt ||
			!reflect.DeepEqual(a.commit.Allocations, b.commit.Allocations) {
			return false
		}
	}
	return reflect.DeepEqual(a.details, b.details)
}

func describe(o outcome) string {
	if o.hasError {
		return fmt.Sprintf("拒绝(%s,行=%d)", o.reason, o.line)
	}
	if o.commit != nil {
		return fmt.Sprintf("承诺成功(到期=%d,分配=%v)", o.commit.ExpireAt, o.commit.Allocations)
	}
	if o.details != nil {
		return fmt.Sprintf("明细%v", o.details)
	}
	if o.kind == "value" {
		return fmt.Sprintf("值=%d", o.value)
	}
	return "成功"
}

// TestDifferential 与独立编写的朴素模型对照大量随机操作序列。
// 日志打印每步的输入、两个实现的输出与判定依据。
func TestDifferential(t *testing.T) {
	const seeds = 40
	const opsPerSeed = 300
	for seed := int64(0); seed < seeds; seed++ {
		t.Run(fmt.Sprintf("seed=%d", seed), func(t *testing.T) {
			rng := rand.New(rand.NewSource(seed))
			steps := genSequence(rng, opsPerSeed)
			real := newRealSystem(t)
			model := newNaiveSystem(t)
			for i, st := range steps {
				gotReal := st.applyReal(real)
				gotNaive := st.applyNaive(model)
				t.Logf("step %d 输入: %s | 正式实现: %s | 朴素模型: %s",
					i, st.desc, describe(gotReal), describe(gotNaive))
				if !equalOutcome(gotReal, gotNaive) {
					t.Fatalf("step %d 输出不一致\n输入: %s\n正式实现: %s\n朴素模型: %s",
						i, st.desc, describe(gotReal), describe(gotNaive))
				}
			}
			if err := real.Validate(); err != nil {
				t.Fatalf("不变量校验失败: %v", err)
			}
			t.Logf("seed=%d 判定依据: %d 步操作输出完全一致；预留考察次数 正式实现=%d 朴素模型=%d",
				seed, len(steps), real.Stats().ReservationsExamined, model.Examined)
		})
	}
}

// TestReplayDeterminism 相同操作序列重放得到完全相同的发货仓分配。
func TestReplayDeterminism(t *testing.T) {
	const seeds = 10
	for seed := int64(1000); seed < 1000+seeds; seed++ {
		rng := rand.New(rand.NewSource(seed))
		steps := genSequence(rng, 200)
		var runs [][]outcome
		for replay := 0; replay < 2; replay++ {
			s := newRealSystem(t)
			var outs []outcome
			for _, st := range steps {
				outs = append(outs, st.applyReal(s))
			}
			runs = append(runs, outs)
		}
		for i := range runs[0] {
			if !equalOutcome(runs[0][i], runs[1][i]) {
				t.Fatalf("seed=%d step=%d 重放结果不一致: %s vs %s",
					seed, i, describe(runs[0][i]), describe(runs[1][i]))
			}
		}
	}
}

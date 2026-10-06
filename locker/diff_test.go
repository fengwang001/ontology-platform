package locker

import (
	"bytes"
	"fmt"
	"math/rand/v2"
	"testing"
)

const (
	opDeposit = iota
	opPickup
	opPay
	opRecycle
	opUnlock
)

func diffConfig() Config {
	return Config{
		FreeStorage:   5,
		BillingPeriod: 3,
		FeePerPeriod:  2,
		FeeCap:        7,
		MaxStorage:    30,
		CodeCooldown:  7,
		CodeCount:     6,
	}
}

func diffCells() []Cell {
	return []Cell{
		{ID: 11, Size: SizeSmall},
		{ID: 12, Size: SizeSmall},
		{ID: 21, Size: SizeMedium},
		{ID: 31, Size: SizeLarge},
	}
}

type liveParcel struct {
	tracking string
	tail     string
}

// TestNaiveModelDifferential：生产实现与独立朴素模型在大量随机序列上逐条对照，
// 同时校验格口互斥、取件码唯一与冷却互斥等全局不变量。
func TestNaiveModelDifferential(tb *testing.T) {
	const sequences = 400
	const opsPerSequence = 300

	for seed := uint64(1); seed <= sequences; seed++ {
		rng := rand.New(rand.NewPCG(seed, seed^0x9e3779b97f4a7c15))
		var logs bytes.Buffer
		cfg := diffConfig()
		real, err := NewCabinet(diffCells(), cfg, NewTextLogger(&logs))
		if err != nil {
			tb.Fatal(err)
		}
		model := NewNaiveModel(diffCells(), cfg)

		var live []liveParcel
		nextTracking := 0
		var clock int64

		randomTail := func() string {
			return fmt.Sprintf("%04d", rng.IntN(10000))
		}

		for step := 0; step < opsPerSequence; step++ {
			kind := rng.IntN(100)
			delta := []int64{0, 0, 1, 2, 3, 5, 8, 13, 25, 40}[rng.IntN(10)]
			at := clock + delta
			if rng.IntN(12) == 0 {
				at = clock - 1 - int64(rng.IntN(3))
			}

			var opDesc string
			switch {
			case kind < 28: // 存件
				tr := fmt.Sprintf("TR%d", nextTracking)
				nextTracking++
				size := Size(rng.IntN(3))
				phone := "137" + randomTail()
				if rng.IntN(15) == 0 {
					phone = "12" // 非法手机号
				}
				opDesc = describeOp(opDeposit, at, tr, size, phone, 0, 0)
				rr, re := real.Deposit(at, TrackingNo(tr), size, Phone(phone))
				mr, me := model.Deposit(at, tr, size, Phone(phone))
				if !sameErr(re, me) {
					tb.Fatalf("seed=%d step=%d %s\nreal err=%v\nmodel err=%v\n%s",
						seed, step, opDesc, re, me, logs.String())
				}
				if re == nil {
					if int(rr.Code) != mr.code || int(rr.Cell) != mr.cell {
						tb.Fatalf("seed=%d step=%d %s real=(%d,%d) model=(%d,%d)",
							seed, step, opDesc, rr.Code, rr.Cell, mr.code, mr.cell)
					}
					tail, _ := Phone(phone).lastFour()
					live = append(live, liveParcel{tracking: tr, tail: tail})
				}
			case kind < 53: // 取件
				code := 0
				tail := ""
				if len(live) > 0 && rng.IntN(6) != 0 {
					lp := live[rng.IntN(len(live))]
					if p := model.parcels[lp.tracking]; p != nil {
						code = p.code
					}
					if rng.IntN(10) < 7 {
						tail = lp.tail
					} else {
						tail = randomTail()
						if tail == lp.tail {
							tail = "0000"
						}
					}
				} else {
					code = cfg.CodeCount + 1 + rng.IntN(3)
					tail = randomTail()
				}
				phone := "137" + tail
				opDesc = describeOp(opPickup, at, "", 0, phone, code, 0)
				rt, re := real.Pickup(at, Code(code), Phone(phone))
				mt, me := model.Pickup(at, code, Phone(phone))
				if !sameErr(re, me) || rt != TrackingNo(mt) {
					tb.Fatalf("seed=%d step=%d %s\nreal=(%q,%v)\nmodel=(%q,%v)\n%s",
						seed, step, opDesc, rt, re, mt, me, logs.String())
				}
			case kind < 68: // 缴费
				tr := pickTracking(rng, live)
				amount := int64(1 + rng.IntN(6))
				if rng.IntN(20) == 0 {
					amount = 0
				}
				opDesc = describeOp(opPay, at, tr, 0, "", 0, int(amount))
				re := real.Pay(at, TrackingNo(tr), amount)
				me := model.Pay(at, tr, amount)
				if !sameErr(re, me) {
					tb.Fatalf("seed=%d step=%d %s real=%v model=%v\n%s",
						seed, step, opDesc, re, me, logs.String())
				}
			case kind < 84: // 回收
				tr := pickTracking(rng, live)
				opDesc = describeOp(opRecycle, at, tr, 0, "", 0, 0)
				re := real.Recycle(at, TrackingNo(tr))
				me := model.Recycle(at, tr)
				if !sameErr(re, me) {
					tb.Fatalf("seed=%d step=%d %s real=%v model=%v\n%s",
						seed, step, opDesc, re, me, logs.String())
				}
			default: // 解锁
				tr := pickTracking(rng, live)
				opDesc = describeOp(opUnlock, at, tr, 0, "", 0, 0)
				re := real.Unlock(at, TrackingNo(tr))
				me := model.Unlock(at, tr)
				if !sameErr(re, me) {
					tb.Fatalf("seed=%d step=%d %s real=%v model=%v\n%s",
						seed, step, opDesc, re, me, logs.String())
				}
			}

			if at >= clock {
				clock = at
			}
			live = pruneLive(live, model)
			assertStateParity(tb, real, model, at, int64(seed), int64(step), opDesc, &logs)
		}
		if testing.Verbose() && seed%50 == 0 {
			tb.Logf("seed=%d ok (%d ops, %d log lines)",
				seed, opsPerSequence, bytes.Count(logs.Bytes(), []byte("\n")))
		}
	}
}

func pickTracking(rng *rand.Rand, live []liveParcel) string {
	if len(live) == 0 {
		return "TR_NONE"
	}
	if rng.IntN(8) == 0 {
		return "TR_GHOST"
	}
	return live[rng.IntN(len(live))].tracking
}

func pruneLive(live []liveParcel, m *NaiveModel) []liveParcel {
	out := live[:0]
	for _, lp := range live {
		if m.parcels[lp.tracking] != nil {
			out = append(out, lp)
		}
	}
	return out
}

func assertStateParity(tb *testing.T, real *Cabinet, model *NaiveModel,
	at int64, seed, step int64, opDesc string, logs *bytes.Buffer) {
	tb.Helper()
	if real.Clock() != model.clock {
		tb.Fatalf("seed=%d step=%d clock real=%d model=%d after %s",
			seed, step, real.Clock(), model.clock, opDesc)
	}
	if len(real.active) != len(model.parcels) {
		tb.Fatalf("seed=%d step=%d active count real=%d model=%d after %s",
			seed, step, len(real.active), len(model.parcels), opDesc)
	}
	cellOwners := map[CellID]string{}
	codeOwners := map[Code]string{}
	for tr, mp := range model.parcels {
		rp := real.active[TrackingNo(tr)]
		if rp == nil {
			tb.Fatalf("seed=%d step=%d parcel %s missing in real after %s\n%s",
				seed, step, tr, opDesc, logs.String())
		}
		if rp.cell != CellID(mp.cell) || rp.code != Code(mp.code) ||
			rp.depositedAt != mp.deposited || rp.paid != mp.paid ||
			rp.mismatches != mp.fails || rp.locked != mp.locked ||
			rp.phoneTail != mp.tail || rp.size != mp.size {
			tb.Fatalf("seed=%d step=%d parcel %s diverges:\nreal=%+v\nmodel=%+v\nafter %s\n%s",
				seed, step, tr, rp, mp, opDesc, logs.String())
		}
		modelDue := model.fee(mp, at) - mp.paid
		if modelDue < 0 {
			modelDue = 0
		}
		if rp.due(real.cfg, at) != modelDue {
			tb.Fatalf("seed=%d step=%d parcel %s due real=%d model=%d",
				seed, step, tr, rp.due(real.cfg, at), modelDue)
		}
		if owner, clash := cellOwners[rp.cell]; clash {
			tb.Fatalf("invariant: cell %d shared by %s and %s", rp.cell, owner, tr)
		}
		cellOwners[rp.cell] = tr
		if owner, clash := codeOwners[rp.code]; clash {
			tb.Fatalf("invariant: code %d duplicated for %s and %s", rp.code, owner, tr)
		}
		codeOwners[rp.code] = tr
		if _, cooling := model.cooling[int(rp.code)]; cooling {
			tb.Fatalf("invariant: active code %d is also cooling", rp.code)
		}
	}
}

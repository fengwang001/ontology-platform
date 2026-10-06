package consignment

import (
	"errors"
	"fmt"
	"math/rand"
	"testing"
)

// errClass 把错误映射为可比较的类别名。
func errClass(err error) string {
	switch {
	case err == nil:
		return "ok"
	case errors.Is(err, ErrOverlappingAgreement):
		return "overlap"
	case errors.Is(err, ErrInvalidParam):
		return "invalid"
	case errors.Is(err, ErrClockRewind):
		return "rewind"
	case errors.Is(err, ErrNotFound):
		return "notfound"
	case errors.Is(err, ErrNoValidPrice):
		return "noprice"
	case errors.Is(err, ErrInsufficientStock):
		return "insufficient"
	case errors.Is(err, ErrOverCap):
		return "overcap"
	case errors.Is(err, ErrOverAmount):
		return "overamount"
	case errors.Is(err, ErrPeriodNotEnded):
		return "notended"
	}
	return fmt.Sprintf("unknown(%v)", err)
}

// TestRandomAgainstModel 用大量随机操作序列对照优化实现与朴素模型，
// 日志打印每步的输入、输出与判定依据。
func TestRandomAgainstModel(t *testing.T) {
	for _, seed := range []int64{1, 7, 42, 2024, 91827} {
		t.Run(fmt.Sprintf("seed=%d", seed), func(t *testing.T) {
			runRandom(t, seed, 3000)
		})
	}
}

func runRandom(t *testing.T, seed int64, steps int) {
	t.Helper()
	rng := rand.New(rand.NewSource(seed))
	sys := New()
	mdl := newModel()

	var batchIDs []uint64 // 双方一致接受的批次号
	var lineIDs []uint64  // 双方一致接受的结算行号
	clock := int64(0)     // 双方应保持一致的当前时钟

	// nextTime 多数时候推进时钟，偶尔回退以考察拒绝路径。
	nextTime := func() int64 {
		if rng.Intn(10) == 0 && clock > 0 {
			return clock - int64(rng.Intn(int(clock)+1)) // 可能回退
		}
		return clock + int64(rng.Intn(6))
	}

	check := func(step int, op string, sErr, mErr error) {
		t.Helper()
		sc, mc := errClass(sErr), errClass(mErr)
		t.Logf("step=%d op=%s -> sys=%s model=%s", step, op, sc, mc)
		if sc != mc {
			t.Fatalf("step=%d op=%s 判定不一致: sys=%s(%v) model=%s(%v)",
				step, op, sc, sErr, mc, mErr)
		}
		if sErr == nil {
			// 双方接受同一操作后时钟必须一致。
			if sys.Now() != mdl.clock {
				t.Fatalf("step=%d op=%s 时钟不一致: sys=%d model=%d",
					step, op, sys.Now(), mdl.clock)
			}
			clock = sys.Now()
		} else {
			// 被拒绝的操作不得改变时钟。
			if sys.Now() != clock || mdl.clock != clock {
				t.Fatalf("step=%d op=%s 被拒绝后时钟改变: sys=%d model=%d want=%d",
					step, op, sys.Now(), mdl.clock, clock)
			}
		}
	}

	for step := 0; step < steps; step++ {
		sup := uint64(1 + rng.Intn(3))
		item := uint64(1 + rng.Intn(3))
		switch rng.Intn(10) {
		case 0: // SetCap
			cap := uint64(rng.Intn(60))
			tm := nextTime()
			op := fmt.Sprintf("SetCap(sup=%d,item=%d,cap=%d,t=%d)", sup, item, cap, tm)
			check(step, op, sys.SetCap(sup, item, cap, tm), mdl.setCap(sup, item, cap, tm))
		case 1: // AddPrice
			from := nextTime()
			to := from + int64(rng.Intn(40))
			price := int64(rng.Intn(20))
			op := fmt.Sprintf("AddPrice(sup=%d,item=%d,[%d,%d),price=%d,t=%d)",
				sup, item, from, to, price, from)
			check(step, op,
				sys.AddPrice(sup, item, from, to, price, from),
				mdl.addPrice(sup, item, from, to, price, from))
		case 2, 3: // Arrive
			qty := uint64(rng.Intn(12))
			period := int64(rng.Intn(30))
			tm := nextTime()
			op := fmt.Sprintf("Arrive(sup=%d,item=%d,qty=%d,period=%d,t=%d)",
				sup, item, qty, period, tm)
			sID, sErr := sys.Arrive(sup, item, qty, period, tm)
			mID, mErr := mdl.arrive(sup, item, qty, period, tm)
			check(step, op, sErr, mErr)
			if sErr == nil {
				if sID != mID {
					t.Fatalf("step=%d %s 批次号不一致: sys=%d model=%d", step, op, sID, mID)
				}
				batchIDs = append(batchIDs, sID)
			}
		case 4: // Return
			var id uint64
			if len(batchIDs) > 0 && rng.Intn(10) > 0 {
				id = batchIDs[rng.Intn(len(batchIDs))]
			} else {
				id = uint64(rng.Intn(int(len(batchIDs)) + 3)) // 可能不存在
			}
			qty := uint64(rng.Intn(8))
			tm := nextTime()
			op := fmt.Sprintf("Return(batch=%d,qty=%d,t=%d)", id, qty, tm)
			check(step, op, sys.Return(id, qty, tm), mdl.ret(id, qty, tm))
		case 5, 6, 7: // Draw
			qty := uint64(rng.Intn(15))
			tm := nextTime()
			op := fmt.Sprintf("Draw(item=%d,qty=%d,t=%d)", item, qty, tm)
			sLines, sErr := sys.Draw(item, qty, tm)
			mLines, mErr := mdl.draw(item, qty, tm)
			check(step, op, sErr, mErr)
			if sErr == nil {
				if len(sLines) != len(mLines) {
					t.Fatalf("step=%d %s 结算行数不一致: sys=%d model=%d",
						step, op, len(sLines), len(mLines))
				}
				for i := range sLines {
					if sLines[i] != mLines[i] {
						t.Fatalf("step=%d %s 第%d行不一致: sys=%+v model=%+v",
							step, op, i, sLines[i], mLines[i])
					}
					lineIDs = append(lineIDs, sLines[i].ID)
				}
				t.Logf("step=%d %s -> lines=%+v", step, op, sLines)
			}
		case 8: // Reverse
			var id uint64
			if len(lineIDs) > 0 && rng.Intn(10) > 0 {
				id = lineIDs[rng.Intn(len(lineIDs))]
			} else {
				id = uint64(rng.Intn(int(len(lineIDs)) + 3))
			}
			qty := uint64(rng.Intn(8))
			tm := nextTime()
			op := fmt.Sprintf("Reverse(line=%d,qty=%d,t=%d)", id, qty, tm)
			check(step, op, sys.Reverse(id, qty, tm), mdl.reverse(id, qty, tm))
		case 9: // 查询类：对账单 + 全量在库与冲销核对
			from := int64(rng.Intn(int(clock) + 5))
			to := from + int64(rng.Intn(30))
			if rng.Intn(2) == 0 {
				to = clock + int64(rng.Intn(5)) // 可能未结束
			}
			op := fmt.Sprintf("Statement(sup=%d,[%d,%d))", sup, from, to)
			sSt, sErr := sys.Statement(sup, from, to)
			mSt, mErr := mdl.statement(sup, from, to)
			check(step, op, sErr, mErr)
			if sErr == nil && sSt != mSt {
				t.Fatalf("step=%d %s 对账单不一致: sys=%+v model=%+v", step, op, sSt, mSt)
			}
		}

		// 每 50 步全量核对只读查询：在库量、到期部分、已冲销数量、时钟。
		if step%50 == 49 {
			for sup := uint64(1); sup <= 3; sup++ {
				for item := uint64(1); item <= 3; item++ {
					sTot, sExp := sys.OnHand(sup, item)
					mTot, mExp := mdl.onHandExpired(sup, item)
					if sTot != mTot || sExp != mExp {
						t.Fatalf("step=%d 在库不一致 sup=%d item=%d: sys=(%d,%d) model=(%d,%d)",
							step, sup, item, sTot, sExp, mTot, mExp)
					}
				}
			}
			for _, id := range lineIDs {
				sQ, sErr := sys.ReversedQty(id)
				mQ, mErr := mdl.reversedQty(id)
				if sErr != nil || mErr != nil || sQ != mQ {
					t.Fatalf("step=%d 已冲销数量不一致 line=%d: sys=%d(%v) model=%d(%v)",
						step, id, sQ, sErr, mQ, mErr)
				}
			}
			if sys.Now() != mdl.clock {
				t.Fatalf("step=%d 时钟漂移: sys=%d model=%d", step, sys.Now(), mdl.clock)
			}
		}
	}
	t.Logf("seed=%d 完成 %d 步：批次 %d 个，结算行 %d 条，终态时钟 %d",
		seed, steps, len(batchIDs), len(lineIDs), clock)
}

// TestDeterministicReplay 验证相同操作序列重放得到完全相同的批次分配与金额。
func TestDeterministicReplay(t *testing.T) {
	const seed = 555
	render := func() string {
		rng := rand.New(rand.NewSource(seed))
		s := New()
		out := ""
		clock := int64(0)
		for i := 0; i < 500; i++ {
			sup := uint64(1 + rng.Intn(3))
			item := uint64(1 + rng.Intn(3))
			tm := clock + int64(rng.Intn(4))
			switch rng.Intn(6) {
			case 0:
				cap := uint64(20 + rng.Intn(50))
				err := s.SetCap(sup, item, cap, tm)
				out += fmt.Sprintf("SetCap:%v;", errClass(err))
			case 1:
				to := tm + 1 + int64(rng.Intn(30))
				err := s.AddPrice(sup, item, tm, to, int64(1+rng.Intn(9)), tm)
				out += fmt.Sprintf("AddPrice:%v;", errClass(err))
			case 2:
				id, err := s.Arrive(sup, item, uint64(1+rng.Intn(10)), int64(1+rng.Intn(30)), tm)
				out += fmt.Sprintf("Arrive:%d:%v;", id, errClass(err))
			case 3:
				lines, err := s.Draw(item, uint64(1+rng.Intn(10)), tm)
				out += fmt.Sprintf("Draw:%v:%v;", errClass(err), lines)
			case 4:
				err := s.Return(uint64(rng.Intn(20)), uint64(1+rng.Intn(5)), tm)
				out += fmt.Sprintf("Return:%v;", errClass(err))
			case 5:
				err := s.Reverse(uint64(rng.Intn(20)), uint64(1+rng.Intn(5)), tm)
				out += fmt.Sprintf("Reverse:%v;", errClass(err))
			}
			if tm >= clock {
				clock = tm
			}
		}
		return out
	}
	if a, b := render(), render(); a != b {
		t.Fatal("相同操作序列重放结果不一致")
	}
}

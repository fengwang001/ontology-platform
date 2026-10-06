package settlement_test

import (
	"fmt"
	"math/rand"
	"testing"

	"ontology/settlement"
	"ontology/settlement/naive"
)

// TestRandomDifferential 用大量随机操作序列对照独立朴素模型。
func TestRandomDifferential(t *testing.T) {
	const iterations = 2000
	for seed := int64(1); seed <= iterations; seed++ {
		runOneDiff(t, seed)
	}
}

func runOneDiff(t *testing.T, seed int64) {
	t.Helper()
	rng := rand.New(rand.NewSource(seed))

	numDays := 1 + rng.Intn(7)
	days := make([]int64, numDays)
	d := int64(1 + rng.Intn(3))
	for i := range days {
		days[i] = d
		d += int64(1 + rng.Intn(3))
	}
	maxFail := 1 + rng.Intn(4)
	bps := int64(rng.Intn(600))

	sys, err := settlement.NewSystem(settlement.Config{BusinessDays: days, MaxFailDays: maxFail, PenaltyBPS: bps})
	if err != nil {
		t.Fatalf("seed %d: new engine: %v", seed, err)
	}
	nv, err := naive.New(days, maxFail, bps)
	if err != nil {
		t.Fatalf("seed %d: new naive: %v", seed, err)
	}

	numAccts := 1 + rng.Intn(4)
	var accts []string
	numSec := 1 + rng.Intn(3)
	for i := 0; i < numAccts; i++ {
		name := fmt.Sprintf("A%d", i)
		accts = append(accts, name)
		sec := map[int]int64{}
		for k := 1; k <= numSec; k++ {
			sec[k] = int64(rng.Intn(20))
		}
		sec64 := map[int64]int64{}
		for k, v := range sec {
			sec64[int64(k)] = v
		}
		cash := int64(rng.Intn(300))
		if err := sys.AddAccount(name, sec64, cash); err != nil {
			t.Fatalf("seed %d: add account engine: %v", seed, err)
		}
		if err := nv.AddAccount(name, sec64, cash); err != nil {
			t.Fatalf("seed %d: add account naive: %v", seed, err)
		}
	}

	nextID := int64(1)
	dayCursor := 0
	totalOps := 5 + rng.Intn(40)
	for op := 0; op < totalOps; op++ {
		// 约 60% 登记指令，40% 推进批处理。
		if dayCursor < numDays && (op == 0 || rng.Intn(10) < 6) {
			buyer := accts[rng.Intn(len(accts))]
			seller := accts[rng.Intn(len(accts))]
			o := settlement.Order{
				ID:           nextID,
				Security:     int64(1 + rng.Intn(numSec)),
				Buyer:        buyer,
				Seller:       seller,
				Qty:          int64(1 + rng.Intn(8)),
				Price:        int64(1 + rng.Intn(20)),
				SettleDay:    days[rng.Intn(numDays)],
				AllowPartial: rng.Intn(2) == 0,
			}
			// 偶尔制造非法/缺失账户/重复。
			mode := rng.Intn(10)
			var no naive.Order
			if mode == 0 {
				o.Qty = 0
			} else if mode == 1 {
				o.Seller = "GHOST"
			}
			no = naive.Order{ID: o.ID, Security: o.Security, Buyer: o.Buyer, Seller: o.Seller,
				Qty: o.Qty, Price: o.Price, SettleDay: o.SettleDay, AllowPartial: o.AllowPartial}

			// 偶尔重复已登记编号。
			if mode == 2 && nextID > 1 {
				o.ID = 1 + rng.Int63n(nextID-1)
				no.ID = o.ID
			}

			errE := sys.RegisterOrder(o)
			errN := nv.Register(no)
			nextID++
			logf(t, "seed=%d op=%d REGISTER %+v -> engine=%v naive=%v", seed, op, o, errE, errN)
			if sameErr(errE, errN) == false {
				t.Fatalf("seed %d: register error mismatch engine=%v naive=%v order=%+v", seed, errE, errN, o)
			}
		} else if dayCursor < numDays {
			day := days[dayCursor]
			ref := map[int64]int64{}
			for k := 1; k <= numSec; k++ {
				ref[int64(k)] = int64(1 + rng.Intn(30))
			}
			errE := sys.RunBatch(day, ref)
			errN := nv.Batch(day, ref)
			logf(t, "seed=%d op=%d BATCH day=%d ref=%v -> engine=%v naive=%v", seed, op, day, ref, errE, errN)
			if (errE == nil) != (errN == nil) {
				t.Fatalf("seed %d: batch error mismatch engine=%v naive=%v day=%d", seed, errE, errN, day)
			}
			if errE == nil {
				dayCursor++
			} else {
				// 出错情形通常是参数非法（ref 合法时不会发生）；这里直接结束该序列避免死循环。
				dayCursor = numDays
			}
		}
		assertEqual(t, seed, sys, nv, accts)
	}
	// 推进剩余营业日。
	for ; dayCursor < numDays; dayCursor++ {
		day := days[dayCursor]
		ref := map[int64]int64{}
		for k := 1; k <= numSec; k++ {
			ref[int64(k)] = int64(1 + rng.Intn(30))
		}
		errE := sys.RunBatch(day, ref)
		errN := nv.Batch(day, ref)
		logf(t, "seed=%d tail BATCH day=%d -> %v/%v", seed, day, errE, errN)
		if errE != nil || errN != nil {
			t.Fatalf("seed %d tail batch %d: %v %v", seed, day, errE, errN)
		}
		assertEqual(t, seed, sys, nv, accts)
	}
}

var errName = map[error]string{
	settlement.ErrInvalidParam:   "invalid",
	settlement.ErrNonBusinessDay: "nonbiz",
	settlement.ErrOrdering:       "ordering",
	settlement.ErrDuplicateID:    "dup",
	settlement.ErrAccountMissing: "noacct",
	settlement.ErrDatePassed:     "passed",
	settlement.ErrOrderNotFound:  "notfound",
}

func sameErr(a, b error) bool {
	return errClass(a) == errClass(b)
}

func errClass(e error) string {
	if e == nil {
		return ""
	}
	for k, v := range errName {
		if k.Error() == e.Error() {
			return v
		}
	}
	return "other"
}

func assertEqual(t *testing.T, seed int64, sys *settlement.System, nv *naive.Model, accts []string) {
	t.Helper()
	for _, name := range accts {
		av, err := sys.QueryAccount(name)
		if err != nil {
			t.Fatalf("seed %d: query %s: %v", seed, name, err)
		}
		mv := nv.Accts[name]
		if av.Cash != mv.Cash || av.FeePayable != mv.FeePay || av.FeeReceivable != mv.FeeRecv ||
			av.CompPayable != mv.CompPay || av.CompReceivable != mv.CompRecv {
			t.Fatalf("seed %d account %s mismatch engine=%+v naive=%+v", seed, name, av, mv)
		}
		for sec, q := range mv.Sec {
			if av.Securities[sec] != q {
				t.Fatalf("seed %d account %s sec %d engine=%d naive=%d", seed, name, sec, av.Securities[sec], q)
			}
		}
		for sec, q := range av.Securities {
			if mv.Sec[sec] != q {
				t.Fatalf("seed %d account %s extra sec %d engine=%d", seed, name, sec, q)
			}
		}
	}
	for id, mo := range nv.Orders {
		ov, err := sys.QueryOrder(id)
		if err != nil {
			t.Fatalf("seed %d order %d missing in engine: %v", seed, id, err)
		}
		if ov.DeliveredQty != mo.Delivered || ov.FailedDays != mo.FailDays || int(ov.Status) != mo.Status {
			t.Fatalf("seed %d order %d mismatch engine=%+v naive=%+v", seed, id, ov, mo)
		}
	}
}

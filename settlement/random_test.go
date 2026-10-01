package settlement

import (
	"errors"
	"fmt"
	"math/rand"
	"reflect"
	"testing"
)

func engineErrKind(err error) naiveErr {
	switch {
	case err == nil:
		return naiveOK
	case errors.Is(err, ErrInvalidParam):
		return naiveInvalid
	case errors.Is(err, ErrExpired):
		return naiveExpired
	case errors.Is(err, ErrSelfTrade):
		return naiveSelfTrade
	case errors.Is(err, ErrNoLongPosition):
		return naiveNoLong
	}
	panic(fmt.Sprintf("unmapped error %v", err))
}

var errName = map[naiveErr]string{
	naiveOK:        "ok",
	naiveInvalid:   "invalid-param",
	naiveExpired:   "expired",
	naiveSelfTrade: "self-trade",
	naiveNoLong:    "no-long-position",
}

// TestRandomDifferential replays 2000 random register/settle sequences
// against both the engine and the naive big.Int oracle, compares every
// outcome, and checks the settlement invariants on each successful Settle.
func TestRandomDifferential(t *testing.T) {
	const seed = 20261002
	rng := rand.New(rand.NewSource(seed))
	t.Logf("seed=%d sequences=2000", seed)

	accts := []string{"A", "B", "C", "D", "E", "F", "G", "H", "Z", "a", "0", "acct-1"}
	pick := func() string { return accts[rng.Intn(len(accts))] }

	settled := 0
	for seq := 0; seq < 2000; seq++ {
		kind := Call
		if rng.Intn(2) == 1 {
			kind = Put
		}
		// Bias K and S small so exercise happens often.
		k := uint64(1 + rng.Intn(200))
		if rng.Intn(4) == 0 {
			k = uint64(1 + rng.Intn(1_000_000))
		}
		mult := uint64(1 + rng.Intn(1000))
		th := uint64(1 + rng.Intn(50))
		if rng.Intn(4) == 0 {
			th = uint64(1 + rng.Intn(1_000_000))
		}

		eng, err := NewEngine(kind, k, mult, th)
		if err != nil {
			t.Fatal(err)
		}
		nav := newNaive(kind, k, mult, th)

		t.Logf("seq=%d kind=%d K=%d Mu=%d T=%d", seq, kind, k, mult, th)

		// Driver-side bookkeeping for invariant checks.
		batchN := make(map[uint64]uint64)
		margins := make(map[string]uint64)

		nOps := 5 + rng.Intn(45)
		var lastRes *Settlement
		for i := 0; i < nOps; i++ {
			var got naiveErr
			var desc string
			switch rng.Intn(10) {
			case 0, 1, 2, 3: // trade
				b, s := pick(), pick()
				n := uint64(1 + rng.Intn(1000))
				switch rng.Intn(12) {
				case 0:
					b = "" // invalid: empty buyer
				case 1:
					s = "" // invalid: empty seller
				case 2:
					s = b // self trade
				case 3:
					n = 0 // invalid qty
				case 4:
					n = 1_000_001 // invalid qty
				case 5:
					n = 1_000_000 // max qty
				}
				gotSeq, err := eng.Trade(b, s, n)
				got = engineErrKind(err)
				want := nav.trade(b, s, n)
				desc = fmt.Sprintf("Trade(%q,%q,%d)", b, s, n)
				if got != want {
					t.Fatalf("seq=%d op=%d %s: engine=%s naive=%s", seq, i, desc, errName[got], errName[want])
				}
				if got == naiveOK {
					batchN[gotSeq] = n
					if gotSeq != nav.seq {
						t.Fatalf("seq=%d op=%d %s: engine seq=%d naive seq=%d", seq, i, desc, gotSeq, nav.seq)
					}
				}
			case 4, 5: // margin
				a := pick()
				g := uint64(1 + rng.Intn(1_000_000))
				switch rng.Intn(10) {
				case 0:
					a = ""
				case 1:
					g = 0
				case 2:
					g = 1_000_000_000_000 // max single
				}
				got = engineErrKind(eng.Margin(a, g))
				want := nav.margin(a, g)
				desc = fmt.Sprintf("Margin(%q,%d)", a, g)
				if got != want {
					t.Fatalf("seq=%d op=%d %s: engine=%s naive=%s", seq, i, desc, errName[got], errName[want])
				}
				if got == naiveOK {
					margins[a] += g
				}
			case 6, 7: // abstain
				a := pick()
				if rng.Intn(10) == 0 {
					a = ""
				}
				got = engineErrKind(eng.Abstain(a))
				want := nav.abstain(a)
				desc = fmt.Sprintf("Abstain(%q)", a)
				if got != want {
					t.Fatalf("seq=%d op=%d %s: engine=%s naive=%s", seq, i, desc, errName[got], errName[want])
				}
			default: // settle
				s := uint64(1 + rng.Intn(300))
				switch rng.Intn(12) {
				case 0:
					s = 0
				case 1:
					s = 1_000_001
				case 2:
					s = 1_000_000
				}
				res, err := eng.Settle(s)
				got = engineErrKind(err)
				nres, want := nav.settle(s)
				desc = fmt.Sprintf("Settle(%d)", s)
				if got != want {
					t.Fatalf("seq=%d op=%d %s: engine=%s naive=%s", seq, i, desc, errName[got], errName[want])
				}
				if got == naiveOK {
					lastRes = res
					settled++
					checkSettlement(t, seq, desc, res, nres, mult, batchN, margins)
				}
			}
			t.Logf("  op=%d %-28s -> %s", i, desc, errName[got])
		}

		// Ensure every sequence ends with a settle attempt.
		if lastRes == nil {
			s := uint64(1 + rng.Intn(300))
			res, err := eng.Settle(s)
			got := engineErrKind(err)
			nres, want := nav.settle(s)
			if got != want {
				t.Fatalf("seq=%d final Settle(%d): engine=%s naive=%s", seq, s, errName[got], errName[want])
			}
			t.Logf("  final Settle(%d) -> %s", s, errName[got])
			if got == naiveOK {
				lastRes = res
				settled++
				checkSettlement(t, seq, "final", res, nres, mult, batchN, margins)
			}
		}

		if lastRes != nil {
			t.Logf("  settled: v=%d Q=%d exercises=%d assignments=%d defaults=%d nets=%d judgment=match+invariants-ok",
				lastRes.Value, lastRes.Total, len(lastRes.Exercises),
				len(lastRes.Assignments), len(lastRes.Defaults), len(lastRes.NetCash))
		} else {
			t.Logf("  no successful settle; judgment=match")
		}
	}
	t.Logf("done: 2000 sequences, %d successful settlements, all matched oracle and invariants", settled)
}

// checkSettlement compares the engine result with the naive oracle and
// verifies the settlement invariants from independently tracked inputs.
func checkSettlement(t *testing.T, seq int, desc string, res *Settlement, nres *naiveResult,
	mult uint64, batchN map[uint64]uint64, margins map[string]uint64) {
	t.Helper()

	if res.Value != nres.v || res.Total != nres.q {
		t.Fatalf("seq=%d %s: v/Q engine=(%d,%d) naive=(%d,%d)", seq, desc, res.Value, res.Total, nres.v, nres.q)
	}
	if !reflect.DeepEqual(res.Exercises, nres.exercises) {
		t.Fatalf("seq=%d %s: exercises engine=%+v naive=%+v", seq, desc, res.Exercises, nres.exercises)
	}
	if !reflect.DeepEqual(res.Assignments, nres.assignments) {
		t.Fatalf("seq=%d %s: assignments engine=%+v naive=%+v", seq, desc, res.Assignments, nres.assignments)
	}
	if !reflect.DeepEqual(res.Defaults, nres.defaults) {
		t.Fatalf("seq=%d %s: defaults engine=%+v naive=%+v", seq, desc, res.Defaults, nres.defaults)
	}
	if !reflect.DeepEqual(res.NetCash, nres.net) {
		t.Fatalf("seq=%d %s: net engine=%+v naive=%+v", seq, desc, res.NetCash, nres.net)
	}

	// Invariant: net cash sums to zero.
	var sum int64
	netOf := make(map[string]int64)
	for _, n := range res.NetCash {
		sum += n.Net
		netOf[n.Account] = n.Net
	}
	if sum != 0 {
		t.Fatalf("seq=%d %s: net sum=%d != 0", seq, desc, sum)
	}

	// Invariant: assigned total == Q, each batch within its size, and
	// assignments form a full prefix with at most one partial batch.
	var assigned uint64
	seenPartial := false
	for _, a := range res.Assignments {
		assigned += a.Quantity
		n, ok := batchN[a.Seq]
		if !ok {
			t.Fatalf("seq=%d %s: assignment to unknown seq %d", seq, desc, a.Seq)
		}
		if a.Quantity > n {
			t.Fatalf("seq=%d %s: batch %d assigned %d > size %d", seq, desc, a.Seq, a.Quantity, n)
		}
		if seenPartial && a.Quantity != 0 {
			t.Fatalf("seq=%d %s: nonzero assignment after partial batch: %+v", seq, desc, a)
		}
		if a.Quantity < n {
			seenPartial = true
		}
	}
	if assigned != res.Total {
		t.Fatalf("seq=%d %s: assigned=%d != Q=%d", seq, desc, assigned, res.Total)
	}

	// Invariants on money: recompute owe/pay/recv/loss from inputs.
	owe := make(map[string]uint64)
	for _, a := range res.Assignments {
		owe[a.Account] += res.Value * a.Quantity * mult
	}
	recv := make(map[string]uint64)
	for _, ex := range res.Exercises {
		recv[ex.Account] = res.Value * ex.Quantity * mult
	}
	var delta uint64
	for _, d := range res.Defaults {
		delta += d.Amount
	}
	var lossSum uint64
	for acct, net := range netOf {
		pay := owe[acct]
		if m := margins[acct]; m < pay {
			pay = m
		}
		if pay > margins[acct] {
			t.Fatalf("seq=%d %s: pay %d > margin %d for %s", seq, desc, pay, margins[acct], acct)
		}
		// net = recv - loss - pay  =>  loss = recv - pay - net
		loss := int64(recv[acct]) - int64(pay) - net
		if loss < 0 {
			t.Fatalf("seq=%d %s: negative loss %d for %s", seq, desc, loss, acct)
		}
		if uint64(loss) > recv[acct] {
			t.Fatalf("seq=%d %s: loss %d > recv %d for %s", seq, desc, loss, recv[acct], acct)
		}
		lossSum += uint64(loss)
	}
	if lossSum != delta {
		t.Fatalf("seq=%d %s: loss sum %d != delta %d", seq, desc, lossSum, delta)
	}
}

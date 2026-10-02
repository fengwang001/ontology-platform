package deposit

import (
	"fmt"
	"math/big"
	"math/rand"
	"reflect"
	"testing"
)

// naive is a reference model that stores every accepted operation and
// recomputes the whole history day by day from scratch. It exists only to
// differential-test the incremental Account implementation.
type naive struct {
	start, m, prev int64
	ops            []nOp

	balance, rem  int64
	records       []settleRecord
	dayBal        []int64 // dayBal[k-start] = day-end balance of day k
	totalProducts *big.Int
}

type nOp struct {
	kind int // 0 deposit, 1 withdraw, 2 setrate, 3 settle, 4 correct
	d, x int64
}

func newNaive(start int64) *naive {
	n := &naive{start: start, m: start, prev: start}
	n.recompute()
	return n
}

// recompute replays all accepted operations day by day.
func (n *naive) recompute() {
	flows := map[int64]int64{}
	rates := map[int64]int64{}
	settles := map[int64]bool{}
	for _, op := range n.ops {
		switch op.kind {
		case 0:
			flows[op.d] += op.x
		case 1:
			flows[op.d] -= op.x
		case 2:
			rates[op.d] = op.x // last one of the day wins
		case 3:
			settles[op.d] = true
		case 4:
			flows[op.d] += op.x
		}
	}
	n.records = nil
	n.dayBal = n.dayBal[:0]
	n.totalProducts = new(big.Int)

	bal := int64(0)
	rate := int64(0)
	R := int64(0)
	prev := n.start
	P := new(big.Int)
	for k := n.start; k <= n.m; k++ {
		bal += flows[k]
		if r, ok := rates[k]; ok {
			rate = r
		}
		if settles[k] {
			N := new(big.Int).Add(P, big.NewInt(R))
			q, rr := new(big.Int).QuoRem(N, bigD, new(big.Int))
			i := q.Int64()
			n.records = append(n.records, settleRecord{start: prev, end: k, interest: i, remainder: rr.Int64()})
			n.totalProducts.Add(n.totalProducts, P)
			bal += i
			R = rr.Int64()
			prev = k
			P = new(big.Int)
		}
		n.dayBal = append(n.dayBal, bal)
		t := big.NewInt(bal)
		P.Add(P, t.Mul(t, big.NewInt(rate)))
	}
	n.balance = bal
	n.rem = R
	n.prev = prev
}

func (n *naive) deposit(d, x int64) error {
	if d < 0 || d > maxOpDay || x < 1 || x > maxAmount {
		return ErrInvalidParam
	}
	if d < n.m {
		return ErrOutOfOrder
	}
	if n.balance+x > maxBalance {
		return ErrBalanceOverflow
	}
	n.ops = append(n.ops, nOp{0, d, x})
	n.m = d
	n.recompute()
	return nil
}

func (n *naive) withdraw(d, x int64) error {
	if d < 0 || d > maxOpDay || x < 1 || x > maxAmount {
		return ErrInvalidParam
	}
	if d < n.m {
		return ErrOutOfOrder
	}
	if x > n.balance {
		return ErrInsufficientFunds
	}
	n.ops = append(n.ops, nOp{1, d, x})
	n.m = d
	n.recompute()
	return nil
}

func (n *naive) setRate(d, r int64) error {
	if d < 0 || d > maxOpDay || r < 0 || r > maxRate {
		return ErrInvalidParam
	}
	if d < n.m {
		return ErrOutOfOrder
	}
	n.ops = append(n.ops, nOp{2, d, r})
	n.m = d
	n.recompute()
	return nil
}

func (n *naive) settle(d int64) (int64, int64, int64, error) {
	if d < 0 || d > maxOpDay || d-n.prev > maxSettleDays {
		return 0, 0, 0, ErrInvalidParam
	}
	if d < n.m {
		return 0, 0, 0, ErrOutOfOrder
	}
	if d <= n.prev {
		return 0, 0, 0, ErrEmptyRange
	}
	n.ops = append(n.ops, nOp{3, d, 0})
	n.m = d
	n.recompute()
	rec := n.records[len(n.records)-1]
	return rec.interest, n.balance, n.rem, nil
}

func (n *naive) correct(d, delta int64) (int64, int64, []Change, error) {
	if d < 0 || d > maxOpDay || d < n.start || delta == 0 || delta > maxDelta || delta < -maxDelta {
		return 0, 0, nil, ErrInvalidParam
	}
	if d >= n.prev {
		return 0, 0, nil, ErrNotSettled
	}
	trial := &naive{start: n.start, m: n.m}
	trial.ops = append(append([]nOp(nil), n.ops...), nOp{4, d, delta})
	trial.recompute()
	negative, overflow := false, false
	for k := d; k <= n.m; k++ {
		b := trial.dayBal[k-n.start]
		if b < 0 {
			negative = true
		}
		if b > maxCorrectedBal {
			overflow = true
		}
	}
	if negative {
		return 0, 0, nil, ErrNegativeBalance
	}
	if overflow {
		return 0, 0, nil, ErrCorrectedOverflow
	}
	changes := []Change{}
	for i, rec := range trial.records {
		old := n.records[i]
		if rec.interest != old.interest || rec.remainder != old.remainder {
			changes = append(changes, Change{
				SettleDay:    rec.end,
				OldInterest:  old.interest,
				NewInterest:  rec.interest,
				OldRemainder: old.remainder,
				NewRemainder: rec.remainder,
			})
		}
	}
	n.ops = trial.ops
	n.recompute()
	return n.balance, n.rem, changes, nil
}

// checkInvariants verifies the cross-cutting guarantees on the account:
// 0 <= R < D, balance == deposits - withdrawals + corrections + credited
// interest, and sum(i)*D + R == total settled products.
func checkInvariants(t *testing.T, a *Account, nv *naive, seq, idx int, op string) {
	t.Helper()
	if a.rem < 0 || a.rem >= D {
		t.Fatalf("seq %d op %d (%s): R = %d out of [0, %d)", seq, idx, op, a.rem, D)
	}
	var flows, interests int64
	for _, v := range a.flows {
		flows += v
	}
	for _, rec := range a.records {
		interests += rec.interest
	}
	if a.balance != flows+interests {
		t.Fatalf("seq %d op %d (%s): balance %d != flows %d + interests %d",
			seq, idx, op, a.balance, flows, interests)
	}
	prodSum := new(big.Int)
	prodSum.Mul(big.NewInt(interests), bigD)
	prodSum.Add(prodSum, big.NewInt(a.rem))
	if prodSum.Cmp(nv.totalProducts) != 0 {
		t.Fatalf("seq %d op %d (%s): sum(i)*D+R = %s, naive total products = %s",
			seq, idx, op, prodSum, nv.totalProducts)
	}
	if a.balance != nv.balance || a.rem != nv.rem {
		t.Fatalf("seq %d op %d (%s): account (bal=%d, R=%d) != naive (bal=%d, R=%d)",
			seq, idx, op, a.balance, a.rem, nv.balance, nv.rem)
	}
	if !reflect.DeepEqual(a.records, nv.records) {
		t.Fatalf("seq %d op %d (%s): records differ:\n got %+v\nwant %+v",
			seq, idx, op, a.records, nv.records)
	}
}

// TestRandomAgainstNaive replays 2000 random operation sequences against
// both the incremental account and the from-scratch naive simulation,
// comparing every result and logging inputs, outputs and the verdict basis.
func TestRandomAgainstNaive(t *testing.T) {
	const sequences = 2000
	for s := 0; s < sequences; s++ {
		rng := rand.New(rand.NewSource(int64(s) + 1))
		start := int64(rng.Intn(4))
		a, err := New(start)
		if err != nil {
			t.Fatalf("seq %d: New(%d): %v", s, start, err)
		}
		nv := newNaive(start)
		ops := 20 + rng.Intn(31)
		t.Logf("seq %d: start=%d, %d ops", s, start, ops)

		genDate := func() int64 {
			if rng.Intn(10) == 0 {
				return nv.m - int64(rng.Intn(4)) // may go backwards / negative
			}
			return nv.m + int64(rng.Intn(6))
		}

		for i := 0; i < ops; i++ {
			kind := rng.Intn(100)
			switch {
			case kind < 25: // deposit
				d, x := genDate(), int64(rng.Intn(1000000)+1)
				if rng.Intn(20) == 0 {
					x = int64(rng.Intn(2000000002)) - 1 // sometimes invalid / overflow
				}
				gErr := a.Deposit(d, x)
				nErr := nv.deposit(d, x)
				t.Logf("seq %d op %d: Deposit(%d, %d) -> account=%v naive=%v", s, i, d, x, gErr, nErr)
				if gErr != nErr {
					t.Fatalf("seq %d op %d: Deposit(%d, %d): account=%v naive=%v", s, i, d, x, gErr, nErr)
				}
			case kind < 40: // withdraw
				d, x := genDate(), int64(rng.Intn(1000000)+1)
				if rng.Intn(20) == 0 {
					x = int64(rng.Intn(2000000002)) - 1
				}
				gErr := a.Withdraw(d, x)
				nErr := nv.withdraw(d, x)
				t.Logf("seq %d op %d: Withdraw(%d, %d) -> account=%v naive=%v", s, i, d, x, gErr, nErr)
				if gErr != nErr {
					t.Fatalf("seq %d op %d: Withdraw(%d, %d): account=%v naive=%v", s, i, d, x, gErr, nErr)
				}
			case kind < 55: // setrate
				d, r := genDate(), int64(rng.Intn(10001))
				if rng.Intn(20) == 0 {
					r = int64(rng.Intn(20002)) - 5000 // sometimes invalid
				}
				gErr := a.SetRate(d, r)
				nErr := nv.setRate(d, r)
				t.Logf("seq %d op %d: SetRate(%d, %d) -> account=%v naive=%v", s, i, d, r, gErr, nErr)
				if gErr != nErr {
					t.Fatalf("seq %d op %d: SetRate(%d, %d): account=%v naive=%v", s, i, d, r, gErr, nErr)
				}
			case kind < 75: // settle
				d := genDate()
				if rng.Intn(15) == 0 {
					d = nv.prev + 3660 + int64(rng.Intn(3)) // coverage limit edge
				}
				gi, gb, gr, gErr := a.Settle(d)
				ni, nb, nr, nErr := nv.settle(d)
				t.Logf("seq %d op %d: Settle(%d) -> account=(%d,%d,%d,%v) naive=(%d,%d,%d,%v)",
					s, i, d, gi, gb, gr, gErr, ni, nb, nr, nErr)
				if gErr != nErr || gi != ni || gb != nb || gr != nr {
					t.Fatalf("seq %d op %d: Settle(%d): account=(%d,%d,%d,%v) naive=(%d,%d,%d,%v)",
						s, i, d, gi, gb, gr, gErr, ni, nb, nr, nErr)
				}
			default: // correct
				span := nv.prev - start + 2
				d := start + int64(rng.Intn(int(span))) // hits settled & unsettled days
				delta := int64(rng.Intn(2000001)) - 1000000
				if rng.Intn(20) == 0 {
					delta = int64(rng.Intn(4000000001)) - 2000000000 // sometimes invalid
				}
				gb, gr, gc, gErr := a.Correct(d, delta)
				nb, nr, nc, nErr := nv.correct(d, delta)
				t.Logf("seq %d op %d: Correct(%d, %d) -> account=(%d,%d,%v,%v) naive=(%d,%d,%v,%v)",
					s, i, d, delta, gb, gr, gc, gErr, nb, nr, nc, nErr)
				if gErr != nErr || gb != nb || gr != nr || !reflect.DeepEqual(gc, nc) {
					t.Fatalf("seq %d op %d: Correct(%d, %d): account=(%d,%d,%+v,%v) naive=(%d,%d,%+v,%v)",
						s, i, d, delta, gb, gr, gc, gErr, nb, nr, nc, nErr)
				}
			}
			checkInvariants(t, a, nv, s, i, fmt.Sprintf("kind=%d", kind))
		}
		t.Logf("seq %d: verdict OK — account matches naive day-by-day replay; "+
			"R in [0,D); balance == flows + credited interest; sum(i)*D+R == total products; "+
			"final balance=%d R=%d records=%d", s, a.balance, a.rem, len(a.records))
	}
}

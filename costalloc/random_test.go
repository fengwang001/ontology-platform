package costalloc

import (
	"errors"
	"fmt"
	"math/big"
	"math/rand"
	"reflect"
	"sort"
	"testing"
)

// naiveLedger is an independent, literal transcription of the specification
// using big integers, used as the reference model for randomized testing.
type naiveLedger struct {
	k, p, n int
	u       [][]int64
	h       []int64
	periods int
}

var (
	errNaiveInvalid    = errors.New("naive: invalid parameter")
	errNaiveNoReceiver = errors.New("naive: no receiver")
)

func newNaive(k, p int) *naiveLedger {
	n := k + p
	u := make([][]int64, n)
	for i := range u {
		u[i] = make([]int64, n)
	}
	return &naiveLedger{k: k, p: p, n: n, u: u, h: make([]int64, n)}
}

func (nl *naiveLedger) setUsage(s, r int, u int64) error {
	if s < 0 || s >= nl.k || r < 0 || r >= nl.n || r == s || u < 0 || u > 1_000_000 {
		return errNaiveInvalid
	}
	nl.u[s][r] = u
	return nil
}

func (nl *naiveLedger) close(ds, dp []int64) ([]Step, []int64, error) {
	if len(ds) != nl.k || len(dp) != nl.p {
		return nil, nil, errNaiveInvalid
	}
	for _, v := range ds {
		if v < 0 || v > 10_000_000_000_000 {
			return nil, nil, errNaiveInvalid
		}
	}
	for _, v := range dp {
		if v < 0 || v > 10_000_000_000_000 {
			return nil, nil, errNaiveInvalid
		}
	}

	h := append([]int64(nil), nl.h...)
	received := make([]int64, nl.n)
	pushed := make([]bool, nl.k)
	var steps []Step

	for step := 0; step < nl.k; step++ {
		best := -1
		var bestA, bestB int64
		for s := 0; s < nl.k; s++ {
			if pushed[s] {
				continue
			}
			var a, b int64
			for r := 0; r < nl.n; r++ {
				if r == s || (r < nl.k && pushed[r]) {
					continue
				}
				b += nl.u[s][r]
				if r < nl.k {
					a += nl.u[s][r]
				}
			}
			greater := false
			switch {
			case best == -1:
				greater = true
			case b == 0:
				greater = false
			case bestB == 0:
				greater = true
			default:
				lhs := new(big.Int).Mul(big.NewInt(a), big.NewInt(bestB))
				rhs := new(big.Int).Mul(big.NewInt(bestA), big.NewInt(b))
				greater = lhs.Cmp(rhs) > 0
			}
			if greater {
				best, bestA, bestB = s, a, b
			}
		}

		s := best
		total := ds[s] + received[s]
		st := Step{Dept: s, Total: total}
		if total > 0 {
			var recvs []int
			bSum := int64(0)
			for r := 0; r < nl.n; r++ {
				if r == s || (r < nl.k && pushed[r]) {
					continue
				}
				if nl.u[s][r] > 0 {
					recvs = append(recvs, r)
					bSum += nl.u[s][r]
				}
			}
			if len(recvs) == 0 {
				return nil, nil, errNaiveNoReceiver
			}
			shares := make([]Share, len(recvs))
			sum := int64(0)
			for i, r := range recvs {
				prod := new(big.Int).Mul(big.NewInt(total), big.NewInt(nl.u[s][r]))
				amount := new(big.Int).Quo(prod, big.NewInt(bSum)).Int64()
				shares[i] = Share{Dept: r, Amount: amount}
				sum += amount
			}
			remainder := total - sum
			order := append([]int(nil), recvs...)
			sort.Slice(order, func(x, y int) bool {
				if h[order[x]] != h[order[y]] {
					return h[order[x]] < h[order[y]]
				}
				return order[x] < order[y]
			})
			for i := int64(0); i < remainder; i++ {
				for j := range shares {
					if shares[j].Dept == order[i] {
						shares[j].Amount++
					}
				}
			}
			for _, sh := range shares {
				h[sh.Dept] += sh.Amount
				received[sh.Dept] += sh.Amount
			}
			st.Shares = shares
		}
		pushed[s] = true
		steps = append(steps, st)
	}

	full := make([]int64, nl.p)
	for i := 0; i < nl.p; i++ {
		full[i] = dp[i] + received[nl.k+i]
	}
	nl.h = h
	nl.periods++
	return steps, full, nil
}

type errKind int

const (
	kindOK errKind = iota
	kindInvalid
	kindNoReceiver
)

func ledgerErrKind(err error) errKind {
	switch {
	case err == nil:
		return kindOK
	case errors.Is(err, ErrInvalidParam):
		return kindInvalid
	case errors.Is(err, ErrNoReceiver):
		return kindNoReceiver
	default:
		return -1
	}
}

func naiveErrKind(err error) errKind {
	switch {
	case err == nil:
		return kindOK
	case errors.Is(err, errNaiveInvalid):
		return kindInvalid
	case errors.Is(err, errNaiveNoReceiver):
		return kindNoReceiver
	default:
		return -1
	}
}

// TestRandomAgainstNaive replays 2000 random operation sequences against the
// naive big-integer model, logging inputs, outputs and the verdict basis.
func TestRandomAgainstNaive(t *testing.T) {
	rng := rand.New(rand.NewSource(20261003))
	const sequences = 2000
	for seq := 0; seq < sequences; seq++ {
		k := 1 + rng.Intn(4)
		p := 1 + rng.Intn(4)
		n := k + p
		l, err := New(k, p)
		if err != nil {
			t.Fatalf("seq %d: New(%d, %d): %v", seq, k, p, err)
		}
		nl := newNaive(k, p)
		ops := 3 + rng.Intn(18)
		t.Logf("seq %d: K=%d P=%d ops=%d", seq, k, p, ops)

		for op := 0; op < ops; op++ {
			switch kind := rng.Intn(100); {
			case kind < 40: // valid SetUsage
				s := rng.Intn(k)
				r := rng.Intn(n - 1)
				if r >= s {
					r++
				}
				var u int64
				switch rng.Intn(10) {
				case 0, 1, 2, 3, 4, 5:
					u = int64(rng.Intn(9))
				case 6, 7, 8:
					u = int64(rng.Intn(1001))
				default:
					u = int64(rng.Intn(1_000_001))
				}
				gotErr := l.SetUsage(s, r, u)
				wantErr := nl.setUsage(s, r, u)
				t.Logf("  op %d: SetUsage(%d,%d,%d) -> ledger=%v naive=%v 判定: 均为 nil", op, s, r, u, gotErr, wantErr)
				if gotErr != nil || wantErr != nil {
					t.Fatalf("seq %d op %d: SetUsage(%d,%d,%d) ledger=%v naive=%v", seq, op, s, r, u, gotErr, wantErr)
				}

			case kind < 50: // invalid SetUsage
				s, r, u := rng.Intn(k), 0, int64(rng.Intn(10))
				switch rng.Intn(4) {
				case 0:
					s = k + rng.Intn(3)
				case 1:
					r = n + rng.Intn(3)
				case 2:
					r = s
				default:
					u = 1_000_001 + int64(rng.Intn(100))
				}
				gotErr := l.SetUsage(s, r, u)
				wantErr := nl.setUsage(s, r, u)
				gk, wk := ledgerErrKind(gotErr), naiveErrKind(wantErr)
				t.Logf("  op %d: SetUsage(%d,%d,%d) -> ledger=%v naive=%v 判定: 错误类别 %v", op, s, r, u, gotErr, wantErr, gk == wk)
				if gk != wk {
					t.Fatalf("seq %d op %d: SetUsage(%d,%d,%d) kind %v != %v", seq, op, s, r, u, gk, wk)
				}

			case kind < 90: // Close, mostly valid
				ds := make([]int64, k)
				dp := make([]int64, p)
				for i := range ds {
					if rng.Intn(10) < 6 {
						ds[i] = int64(rng.Intn(501))
					} else {
						ds[i] = rng.Int63n(10_000_000_000_001)
					}
				}
				for i := range dp {
					dp[i] = int64(rng.Intn(501))
				}
				gotRes, gotErr := l.Close(ds, dp)
				wantSteps, wantFull, wantErr := nl.close(ds, dp)
				gk, wk := ledgerErrKind(gotErr), naiveErrKind(wantErr)
				if gk != wk {
					t.Fatalf("seq %d op %d: Close(%v,%v) kind %v != %v (ledger err %v, naive err %v)",
						seq, op, ds, dp, gk, wk, gotErr, wantErr)
				}
				if gk == kindOK {
					if !reflect.DeepEqual(gotRes.Steps, wantSteps) || !reflect.DeepEqual(gotRes.FullCosts, wantFull) {
						t.Fatalf("seq %d op %d: Close(%v,%v)\nledger steps=%+v full=%v\nnaive  steps=%+v full=%v",
							seq, op, ds, dp, gotRes.Steps, gotRes.FullCosts, wantSteps, wantFull)
					}
					t.Logf("  op %d: Close(%v,%v) -> steps=%+v full=%v 判定: 与朴素模拟逐步一致", op, ds, dp, gotRes.Steps, gotRes.FullCosts)
				} else {
					t.Logf("  op %d: Close(%v,%v) -> ledger=%v naive=%v 判定: 拒绝类别一致(%d)", op, ds, dp, gotErr, wantErr, gk)
				}

			default: // invalid Close
				ds := make([]int64, k)
				dp := make([]int64, p)
				for i := range ds {
					ds[i] = int64(rng.Intn(100))
				}
				for i := range dp {
					dp[i] = int64(rng.Intn(100))
				}
				switch rng.Intn(4) {
				case 0:
					ds = append(ds, 0)
				case 1:
					dp = append(dp, 0)
				case 2:
					ds[0] = 10_000_000_000_001
				default:
					dp[0] = -1
				}
				_, gotErr := l.Close(ds, dp)
				_, _, wantErr := nl.close(ds, dp)
				gk, wk := ledgerErrKind(gotErr), naiveErrKind(wantErr)
				t.Logf("  op %d: Close(%v,%v) -> ledger=%v naive=%v 判定: 错误类别一致=%v", op, ds, dp, gotErr, wantErr, gk == wk)
				if gk != wk {
					t.Fatalf("seq %d op %d: invalid Close kind %v != %v", seq, op, gk, wk)
				}
			}

			if got, want := l.Snapshot(), nl.h; !reflect.DeepEqual(got, want) {
				t.Fatalf("seq %d op %d: H = %v, naive H = %v", seq, op, got, want)
			}
			if l.Periods() != nl.periods {
				t.Fatalf("seq %d op %d: periods %d != %d", seq, op, l.Periods(), nl.periods)
			}
		}
		t.Logf("seq %d: 结束 H=%v periods=%d 判定: H 与期数均与朴素模拟一致", seq, nl.h, nl.periods)
	}
}

// Example demonstrates the specification's worked example.
func Example() {
	l, _ := New(2, 2)
	_ = l.SetUsage(0, 1, 1)
	_ = l.SetUsage(0, 2, 1)
	_ = l.SetUsage(0, 3, 2)
	_ = l.SetUsage(1, 0, 1)
	_ = l.SetUsage(1, 2, 3)
	res, _ := l.Close([]int64{101, 60}, []int64{10, 20})
	for _, st := range res.Steps {
		fmt.Printf("push dept %d total %d shares %v\n", st.Dept, st.Total, st.Shares)
	}
	fmt.Println("full costs:", res.FullCosts)
	// Output:
	// push dept 0 total 101 shares [{1 26} {2 25} {3 50}]
	// push dept 1 total 86 shares [{2 86}]
	// full costs: [121 70]
}

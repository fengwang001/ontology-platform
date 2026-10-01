package freeze

import (
	"fmt"
	"math/rand"
	"strings"
	"testing"
)

const randomSeeds = 2000

// TestRandomDifferential replays 2000 random operation sequences against both
// the manager and the independent naive model, comparing acceptance, rejection
// reason and every query snapshot. Logs print inputs, outputs and the reason.
func TestRandomDifferential(t *testing.T) {
	for seed := int64(1); seed <= randomSeeds; seed++ {
		rng := rand.New(rand.NewSource(seed))
		mgr := NewManager()
		nm := newNaive()
		var clock int64
		acctNames := []string{"a", "b", "c"}

		ops := 40 + rng.Intn(40)
		var logb strings.Builder
		fmt.Fprintf(&logb, "seed=%d ops=%d\n", seed, ops)

		for step := 0; step < ops; step++ {
			op := randOp{acct: acctNames[rng.Intn(len(acctNames))]}
			// Timestamp: usually non-decreasing with repeats; occasionally regress.
			switch rng.Intn(12) {
			case 0:
				op.t = clock
			case 1:
				op.t = clock - int64(1+rng.Intn(3))
			default:
				op.t = clock + int64(rng.Intn(4))
			}
			op.op = []string{
				"deposit", "debit", "freeze",
				"unfreeze", "seize", "seizeq", "query",
			}[rng.Intn(7)]

			switch op.op {
			case "deposit":
				op.x = 1 + rng.Int63n(120)
			case "debit", "seizeq":
				op.x = 1 + rng.Int63n(200)
			case "freeze":
				op.id = fmt.Sprintf("o%d", rng.Intn(7)) // small pool: dup/reuse likely
				op.x = 1 + rng.Int63n(200)
				switch rng.Intn(6) {
				case 0:
					op.exp = 0
				case 1:
					op.exp = op.t + 1
				case 2:
					op.exp = op.t // invalid: expiry must be strictly after t
				default:
					op.exp = 1 + rng.Int63n(15)
				}
			case "unfreeze", "seize":
				op.id = fmt.Sprintf("o%d", rng.Intn(7))
				op.x = 1 + rng.Int63n(150)
			}

			ok, reason, snap := op.apply(mgr)
			nok, nreason, nsnap := nm.call(op.op, op.acct, op.t, op.id, op.x, op.exp)

			verdict := "ACCEPT"
			if !ok {
				verdict = "REJECT reason=" + reason
			}
			basis := decideBasis(op.op, ok)
			fmt.Fprintf(&logb, "  step %2d %-52s -> %-45s %s\n", step, op.String(), verdict, basis)

			if ok != nok || reason != nreason {
				t.Fatalf("seed=%d step=%d %s: manager ok=%v reason=%q; naive ok=%v reason=%q\n%s",
					seed, step, op.String(), ok, reason, nok, nreason, logb.String())
			}
			if ok && op.op == "query" && !snapsEqual(snap, nsnap) {
				t.Fatalf("seed=%d step=%d %s: snapshot mismatch\nmanager=%+v\nnaive=%+v\n%s",
					seed, step, op.String(), snap, nsnap, logb.String())
			}
			if ok && op.t >= 0 {
				clock = op.t
			}
		}

		// After the sequence, every known account at the final clock must match.
		for _, name := range acctNames {
			if _, err := mgr.Query(b(name), clock); err != nil {
				continue
			}
			mgSnap, err := mgr.Query(b(name), clock)
			if err != nil {
				continue
			}
			acc := nm.accounts[name]
			if acc == nil {
				t.Fatalf("seed=%d: manager has account %s but naive does not\n%s", seed, name, logb.String())
			}
			nm.sweep(acc, clock)
			ns := &naiveSnap{
				balance:   acc.balance,
				available: acc.balance - effTotal(nm.eff(acc)),
				orders:    nm.eff(acc),
			}
			if !snapsEqual(mgSnap, ns) {
				t.Fatalf("seed=%d final snapshot of %s mismatch\nmanager=%+v\nnaive=%+v\n%s",
					seed, name, mgSnap, ns, logb.String())
			}
		}

		// The per-seed log is attached verbosely; visible with `go test -v`.
		t.Logf("\n%s", logb.String())
	}
}

// decideBasis records why an accepted/rejected call was decided, making the
// differential log explain each judgement.
func decideBasis(op string, ok bool) string {
	if ok {
		return "basis: effective state validated, expiry sweep + mutation committed atomically"
	}
	return "basis: judged on effective state, rejected call committed nothing"
}

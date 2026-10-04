package scaler

import (
	"errors"
	"fmt"
	"math/rand"
	"reflect"
	"testing"
)

// naiveSim is a deliberately plain, step-by-step reference model written
// directly from the specification. It trades clarity for cleverness so the
// randomized cross-check stays meaningful.
type naiveSim struct {
	cfg     Config
	cap     int64
	batches []Batch
	b0      int64
	lastOut int64
	hasOut  bool
	lastIn  int64
	hasIn   bool
	maxNow  int64
	lastNow int64
	lastMet int64
	hasLast bool
}

func newNaiveSim(cfg Config) *naiveSim {
	return &naiveSim{cfg: cfg, cap: cfg.C0}
}

func naivePickPct(tiers []Tier, v int64) int64 {
	chosen := tiers[0].Pct
	for _, tier := range tiers {
		if tier.Lo <= v {
			chosen = tier.Pct
		}
	}
	return chosen
}

// eval runs one evaluation and returns the result, the rejection error and
// a human-readable decision basis for logging.
func (n *naiveSim) eval(now, metric int64) (Result, error, string) {
	cfg := &n.cfg

	if now < 0 || now > 1_000_000_000_000_000 || metric < 0 || metric > 1_000_000_000 {
		return Result{}, ErrInvalidArgument, "reject: invalid argument"
	}
	if now < n.maxNow {
		return Result{}, ErrClockRegression, fmt.Sprintf("reject: clock regression now=%d < maxNow=%d", now, n.maxNow)
	}

	// Step 1: merge ready batches.
	merged := int64(0)
	keep := make([]Batch, 0, len(n.batches))
	for _, b := range n.batches {
		if b.ReadyAt <= now {
			n.cap += b.Count
			merged += b.Count
		} else {
			keep = append(keep, b)
		}
	}
	n.batches = keep

	inFlight := func() int64 {
		total := int64(0)
		for _, b := range n.batches {
			total += b.Count
		}
		return total
	}
	eff := func() int64 { return n.cap + inFlight() }
	result := func(kind Kind, amount int64) Result {
		return Result{Kind: kind, Amount: amount, Cap: n.cap, InFlight: inFlight()}
	}

	// Idempotency: identical repeat at the same now is a no-op.
	if n.hasLast && n.lastNow == now && n.lastMet == metric {
		n.maxNow = now
		return result(KindNone, 0), nil, "repeat of last (now,metric): no action"
	}
	n.hasLast, n.lastNow, n.lastMet = true, now, metric

	basis := fmt.Sprintf("merged=%d", merged)

	if metric >= cfg.H {
		v := metric - cfg.H
		pct := naivePickPct(cfg.OutTiers, v)
		cooling := n.hasOut && now < n.lastOut+cfg.Cout
		base := eff()
		if cooling {
			base = n.b0
		}
		delta := cfg.Ms
		if c := (base*pct + 99) / 100; c > delta {
			delta = c
		}
		target := base + delta
		if target > cfg.Mx {
			target = cfg.Mx
		}
		basis += fmt.Sprintf(" out: v=%d pct=%d cooling=%v base=%d delta=%d target=%d eff=%d",
			v, pct, cooling, base, delta, target, eff())
		oldEff := eff()
		if target > oldEff {
			n.batches = append(n.batches, Batch{ReadyAt: now + cfg.W, Count: target - oldEff})
			if !cooling {
				n.b0 = oldEff
				n.lastOut = now
				n.hasOut = true
			}
			n.maxNow = now
			return result(KindOut, target-oldEff), nil, basis + " -> append batch"
		}
		n.maxNow = now
		return result(KindNone, 0), nil, basis + " -> target<=eff, no action"
	}

	if metric < cfg.Lw {
		u := cfg.Lw - metric
		pct := naivePickPct(cfg.InTiers, u)
		basis += fmt.Sprintf(" in: u=%d pct=%d", u, pct)
		if len(n.batches) > 0 {
			n.maxNow = now
			return result(KindNone, 0), nil, basis + " -> blocked by in-flight"
		}
		if n.hasIn && now < n.lastIn+cfg.Cin {
			n.maxNow = now
			return result(KindNone, 0), nil, basis + " -> blocked by Cin"
		}
		delta := n.cap * pct / 100
		if delta < 1 {
			delta = 1
		}
		target := n.cap - delta
		if target < cfg.Mn {
			target = cfg.Mn
		}
		basis += fmt.Sprintf(" delta=%d target=%d cap=%d", delta, target, n.cap)
		if target < n.cap {
			amount := n.cap - target
			n.cap = target
			n.lastIn, n.hasIn = now, true
			n.maxNow = now
			return result(KindIn, amount), nil, basis + " -> scale-in"
		}
		n.maxNow = now
		return result(KindNone, 0), nil, basis + " -> target==cap, no action"
	}

	n.maxNow = now
	return result(KindNone, 0), nil, basis + " dead band"
}

// randConfig builds a random valid config.
func randConfig(r *rand.Rand) Config {
	mn := int64(1 + r.Intn(50))
	mx := mn + int64(r.Intn(200))
	c0 := mn + int64(r.Intn(int(mx-mn+1)))
	h := int64(1 + r.Intn(1000))
	lw := int64(r.Intn(int(h)))
	randTiers := func() []Tier {
		n := 1 + r.Intn(4)
		tiers := make([]Tier, 0, n)
		lo := int64(0)
		for i := 0; i < n; i++ {
			tiers = append(tiers, Tier{Lo: lo, Pct: int64(1 + r.Intn(1000))})
			lo += int64(1 + r.Intn(20))
		}
		return tiers
	}
	return Config{
		Mn: mn, Mx: mx, C0: c0,
		H: h, Lw: lw,
		OutTiers: randTiers(),
		InTiers:  randTiers(),
		Ms:       int64(1 + r.Intn(10)),
		W:        int64(1 + r.Intn(10)),
		Cout:     int64(r.Intn(16)),
		Cin:      int64(r.Intn(16)),
	}
}

// step is one randomized Evaluate input.
type step struct {
	now, metric int64
}

// randSteps builds a random evaluation sequence, including occasional
// invalid arguments and clock regressions.
func randSteps(r *rand.Rand, cfg Config, n int) []step {
	steps := make([]step, 0, n)
	now := int64(0)
	for i := 0; i < n; i++ {
		now += int64(r.Intn(16))
		metric := int64(r.Intn(int(cfg.H) + 500))
		switch r.Intn(100) {
		case 0, 1, 2: // invalid argument
			if r.Intn(2) == 0 {
				now = -int64(1 + r.Intn(5))
			} else {
				metric = 1_000_000_000 + int64(1+r.Intn(5))
			}
		case 3, 4, 5: // clock regression (only meaningful once now>0)
			if now > 0 {
				now = int64(r.Intn(int(now)))
			}
		case 6: // exact repeat of the previous evaluation
			if len(steps) > 0 {
				now, metric = steps[len(steps)-1].now, steps[len(steps)-1].metric
			}
		}
		steps = append(steps, step{now: now, metric: metric})
		if now < 0 {
			now = 0
		}
	}
	return steps
}

// TestRandomAgainstNaive replays 2000 random evaluation sequences against
// the naive reference model, logging inputs, outputs and the decision
// basis for every step.
func TestRandomAgainstNaive(t *testing.T) {
	const sequences = 2000
	r := rand.New(rand.NewSource(20261004))

	for seq := 0; seq < sequences; seq++ {
		cfg := randConfig(r)
		steps := randSteps(r, cfg, 5+r.Intn(36))

		s, err := New(cfg)
		if err != nil {
			t.Fatalf("seq %d: New(%+v): %v", seq, cfg, err)
		}
		sim := newNaiveSim(cfg)

		for i, st := range steps {
			got, gotErr := s.Evaluate(st.now, st.metric)
			want, wantErr, basis := sim.eval(st.now, st.metric)

			t.Logf("seq=%d step=%d in=(now=%d metric=%d) out=%+v err=%v basis=%s",
				seq, i, st.now, st.metric, got, gotErr, basis)

			if !errors.Is(gotErr, wantErr) {
				t.Fatalf("seq %d step %d Evaluate(%d,%d): err=%v, want %v (basis: %s)",
					seq, i, st.now, st.metric, gotErr, wantErr, basis)
			}
			if wantErr == nil && got != want {
				t.Fatalf("seq %d step %d Evaluate(%d,%d): got %+v, want %+v (basis: %s)",
					seq, i, st.now, st.metric, got, want, basis)
			}
			if gotErr != nil {
				continue // rejected: state untouched, invariants vacuous
			}
			// Invariants after every accepted evaluation.
			if got.Cap < cfg.Mn || got.Cap > cfg.Mx {
				t.Fatalf("seq %d step %d: cap=%d outside [%d,%d]", seq, i, got.Cap, cfg.Mn, cfg.Mx)
			}
			if got.Cap+got.InFlight > cfg.Mx {
				t.Fatalf("seq %d step %d: eff=%d exceeds Mx=%d", seq, i, got.Cap+got.InFlight, cfg.Mx)
			}
			_, batches, _, _, _, _, _, maxNow := s.Snapshot()
			for _, b := range batches {
				if b.ReadyAt <= maxNow {
					t.Fatalf("seq %d step %d: batch %+v ready at <= maxNow=%d", seq, i, b, maxNow)
				}
			}
		}

		// Full final-state comparison, including the batch list.
		_, batches, _, _, _, _, _, maxNow := s.Snapshot()
		if len(batches) == 0 && len(sim.batches) == 0 {
			batches, sim.batches = nil, nil
		}
		if !reflect.DeepEqual(batches, sim.batches) {
			t.Fatalf("seq %d: final batches=%v, naive=%v", seq, batches, sim.batches)
		}
		if maxNow != sim.maxNow {
			t.Fatalf("seq %d: maxNow=%d, naive=%d", seq, maxNow, sim.maxNow)
		}
	}
}

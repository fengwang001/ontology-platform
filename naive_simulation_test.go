package ontology

import (
	"errors"
	"fmt"
	"math/big"
	"math/rand"
	"testing"
)

type naiveConfig struct {
	rA, rB, nmin, minStep, nmax, tf, tau int64
	boundary                             []int64
}

type naiveState struct {
	stopped        bool
	effectiveLooks int
	lastCountedN   int64
	lastNA         int64
	lastCA         int64
	lastNB         int64
	lastCB         int64
	hasAccepted    bool
	lastConclusion Conclusion
}

type naiveOutput struct {
	accepted       bool
	conclusion     Conclusion
	effectiveLook  bool
	lookIndex      int
	boundary       int64
	boundaryUsed   bool
	d              int64
	left, right    string
	stopped        bool
	effectiveCount int
	lastCountedN   int64
}

func naiveLook(state *naiveState, cfg naiveConfig, nA, cA, nB, cB int64) (naiveOutput, error) {
	if nA < 0 || cA < 0 || cA > nA || nA > 1_000_000 ||
		nB < 0 || cB < 0 || cB > nB || nB > 1_000_000 {
		return naiveOutput{}, ErrInvalidArguments
	}
	if state.stopped {
		return naiveOutput{}, ErrStopped
	}
	if state.hasAccepted && (nA < state.lastNA || cA < state.lastCA || nB < state.lastNB || cB < state.lastCB) {
		return naiveOutput{}, ErrDataRegression
	}

	n := nA + nB
	c := cA + cB
	d := cB*nA - cA*nB

	out := naiveOutput{
		accepted:       true,
		d:              d,
		stopped:        state.stopped,
		effectiveCount: state.effectiveLooks,
		lastCountedN:   state.lastCountedN,
	}
	state.lastNA = nA
	state.lastCA = cA
	state.lastNB = nB
	state.lastCB = cB
	state.hasAccepted = true

	ratioA := nA * cfg.rB
	ratioB := nB * cfg.rA
	ratioDeviation := absInt64(ratioA-ratioB) * 100
	ratioLimit := cfg.tau * (ratioA + ratioB)

	if n >= 2*cfg.nmin && ratioDeviation > ratioLimit {
		out.conclusion = RatioInvalid
		out.stopped = true
		state.stopped = true
		state.lastConclusion = out.conclusion
		return finalizeNaive(state, out), nil
	}

	if nA < cfg.nmin || nB < cfg.nmin {
		out.conclusion = ContinueSampleTooSmall
		state.lastConclusion = out.conclusion
		return finalizeNaive(state, out), nil
	}

	if n-state.lastCountedN < cfg.minStep {
		out.conclusion = ContinueObservation
		state.lastConclusion = out.conclusion
		return finalizeNaive(state, out), nil
	}

	state.effectiveLooks++
	state.lastCountedN = n
	out.effectiveLook = true
	out.effectiveCount = state.effectiveLooks
	out.lastCountedN = n
	out.lookIndex = state.effectiveLooks

	boundaryIndex := state.effectiveLooks
	if boundaryIndex > len(cfg.boundary) {
		boundaryIndex = len(cfg.boundary)
	}
	out.boundary = cfg.boundary[boundaryIndex-1]
	out.boundaryUsed = true

	if c == 0 || c == n {
		out.left = "0"
		out.right = "0"
		if n >= cfg.nmax {
			out.conclusion = Futile
			out.stopped = true
			state.stopped = true
		} else if 2*n >= cfg.nmax && cfg.tf > 0 {
			out.conclusion = Futile
			out.stopped = true
			state.stopped = true
		} else {
			out.conclusion = ContinueRunning
		}
		state.lastConclusion = out.conclusion
		return finalizeNaive(state, out), nil
	}

	left := new(big.Int).SetInt64(d)
	left.Mul(left, left)
	left.Mul(left, big.NewInt(n))
	left.Mul(left, big.NewInt(100))

	right := big.NewInt(out.boundary)
	right.Mul(right, big.NewInt(nA))
	right.Mul(right, big.NewInt(nB))
	right.Mul(right, big.NewInt(c))
	right.Mul(right, big.NewInt(n-c))

	futilityRight := big.NewInt(cfg.tf)
	futilityRight.Mul(futilityRight, big.NewInt(nA))
	futilityRight.Mul(futilityRight, big.NewInt(nB))
	futilityRight.Mul(futilityRight, big.NewInt(c))
	futilityRight.Mul(futilityRight, big.NewInt(n-c))

	out.left = left.String()
	out.right = right.String()

	if left.Cmp(right) >= 0 {
		if d > 0 {
			out.conclusion = Winner
		} else {
			out.conclusion = Worse
		}
		out.stopped = true
		state.stopped = true
	} else if n >= cfg.nmax {
		out.conclusion = Futile
		out.stopped = true
		state.stopped = true
	} else if 2*n >= cfg.nmax && left.Cmp(futilityRight) < 0 {
		out.conclusion = Futile
		out.stopped = true
		state.stopped = true
	} else {
		out.conclusion = ContinueRunning
	}

	state.lastConclusion = out.conclusion
	return finalizeNaive(state, out), nil
}

func finalizeNaive(state *naiveState, out naiveOutput) naiveOutput {
	out.effectiveCount = state.effectiveLooks
	out.lastCountedN = state.lastCountedN
	return out
}

func TestSpecExample(t *testing.T) {
	stopper := mustStopper(t, 1, 1, 100, 1000, 100000, []int64{2000, 1000, 400}, 50, 5)

	first := mustLook(t, stopper, 1000, 100, 1000, 140)
	if first.Conclusion != ContinueRunning || first.LookIndex != 1 || first.Boundary != 2000 {
		t.Fatalf("first example look = %+v", first)
	}

	second := mustLook(t, stopper, 2000, 200, 2000, 300)
	if second.Conclusion != Winner || second.LookIndex != 2 || second.Boundary != 1000 || second.D != 200000 {
		t.Fatalf("second example look = %+v", second)
	}
}

func TestRandomSequencesAgainstNaiveSimulation(t *testing.T) {
	rng := rand.New(rand.NewSource(20261003))

	for sequence := 0; sequence < 2000; sequence++ {
		cfg := randomNaiveConfig(rng)
		stopper, err := NewSequentialABStopper(cfg.rA, cfg.rB, cfg.nmin, cfg.minStep, cfg.nmax, cfg.boundary, cfg.tf, cfg.tau)
		if err != nil {
			t.Fatalf("sequence %d random config rejected: %+v %v", sequence, cfg, err)
		}
		naive := &naiveState{}

		var nA, cA, nB, cB int64
		steps := 1 + rng.Intn(18)
		for step := 0; step < steps; step++ {
			nextA, nextCA, nextB, nextCB := nA, cA, nB, cB
			if rng.Intn(12) == 0 && (nA > 0 || nB > 0) {
				if nA > 0 {
					nextA--
				} else {
					nextB--
				}
				if nextCA > nextA {
					nextCA = nextA
				}
				if nextCB > nextB {
					nextCB = nextB
				}
			} else {
				if nA < 1_000_000 {
					nextA = nA + 1 + rng.Int63n(cfg.nmin+299)
					if nextA > 1_000_000 {
						nextA = 1_000_000
					}
				}
				if nB < 1_000_000 {
					nextB = nB + 1 + rng.Int63n(cfg.nmin+299)
					if nextB > 1_000_000 {
						nextB = 1_000_000
					}
				}
				nextCA = cA
				if nextA > cA {
					nextCA += rng.Int63n(nextA - cA + 1)
				}
				nextCB = cB
				if nextB > cB {
					nextCB += rng.Int63n(nextB - cB + 1)
				}
			}

			var gotErr error
			var got LookResult
			got, gotErr = stopper.Look(nextA, nextCA, nextB, nextCB)
			want, wantErr := naiveLook(naive, cfg, nextA, nextCA, nextB, nextCB)

			t.Logf("seq=%d step=%d input=(nA=%d,cA=%d,nB=%d,cB=%d) cfg=%+v got=(conclusion=%s accepted=%t effective=%t index=%d boundary=%d d=%d L=%s R=%s err=%v) want=(conclusion=%s accepted=%t effective=%t index=%d boundary=%d d=%d L=%s R=%s err=%v) reason=%q",
				sequence, step, nextA, nextCA, nextB, nextCB, cfg,
				got.Conclusion, got.Accepted, got.EffectiveLook, got.LookIndex, got.Boundary, got.D, got.StatisticLeft, got.StatisticRight, gotErr,
				want.conclusion.String(), want.accepted, want.effectiveLook, want.lookIndex, want.boundary, want.d, want.left, want.right, wantErr, got.Reason)

			if !errors.Is(gotErr, wantErr) {
				t.Fatalf("error mismatch: got %v want %v", gotErr, wantErr)
			}
			if gotErr != nil {
				status := stopper.Status()
				if (status.State == Stopped) != naive.stopped ||
					status.EffectiveLooks != naive.effectiveLooks ||
					status.LastCountedN != naive.lastCountedN ||
					status.LastConclusion != naive.lastConclusion {
					t.Fatalf("rejected call changed state: got %+v want stopped=%t looks=%d n=%d conclusion=%s",
						status, naive.stopped, naive.effectiveLooks, naive.lastCountedN, naive.lastConclusion)
				}
				if errors.Is(gotErr, ErrStopped) {
					break
				}
				continue
			}
			if got.Conclusion != want.conclusion ||
				got.Accepted != want.accepted ||
				got.EffectiveLook != want.effectiveLook ||
				got.LookIndex != want.lookIndex ||
				got.Boundary != want.boundary ||
				got.D != want.d ||
				got.StatisticLeft != want.left ||
				got.StatisticRight != want.right {
				t.Fatalf("result mismatch: got %+v want %+v", got, want)
			}

			status := stopper.Status()
			if (status.State == Stopped) != want.stopped ||
				status.EffectiveLooks != want.effectiveCount ||
				status.LastCountedN != want.lastCountedN ||
				status.LastConclusion != want.conclusion {
				t.Fatalf("status mismatch: got %+v want %+v", status, want)
			}

			nA, cA, nB, cB = nextA, nextCA, nextB, nextCB
			if want.stopped {
				break
			}
		}

		t.Logf("sequence %d final status=%+v config=%s", sequence, stopper.Status(), formatNaiveConfig(cfg))
	}
}

func randomNaiveConfig(rng *rand.Rand) naiveConfig {
	boundaryCount := 1 + rng.Intn(8)
	boundaries := make([]int64, boundaryCount)
	for i := range boundaries {
		boundaries[i] = 1 + rng.Int63n(1_000_000)
	}

	return naiveConfig{
		rA:       1 + rng.Int63n(100),
		rB:       1 + rng.Int63n(100),
		nmin:     1 + rng.Int63n(300),
		minStep:  1 + rng.Int63n(500),
		nmax:     2 + rng.Int63n(10_000),
		boundary: boundaries,
		tf:       rng.Int63n(1_000_001),
		tau:      rng.Int63n(101),
	}
}

func formatNaiveConfig(cfg naiveConfig) string {
	return fmt.Sprintf("r=%d:%d nmin=%d minStep=%d nmax=%d T=%v Tf=%d tau=%d",
		cfg.rA, cfg.rB, cfg.nmin, cfg.minStep, cfg.nmax, cfg.boundary, cfg.tf, cfg.tau)
}

package debloat

import (
	"fmt"
	"math/rand"
	"testing"
)

type fuzzOpKind int

const (
	opSample fuzzOpKind = iota
	opSetChannels
	opPause
	opResume
)

type fuzzOp struct {
	kind     fuzzOpKind
	bytes    uint64
	dt       uint64
	channels int
}

func genValidConfig(r *rand.Rand) Config {
	g := uint64(1 + r.Intn(64))
	units := 1 + r.Intn(256) // Bmin in units of G, up to 16384
	bminUnits := units
	b0Units := bminUnits + r.Intn(257)
	bmaxUnits := b0Units + r.Intn(257)
	bmin := g * uint64(bminUnits)
	b0 := g * uint64(b0Units)
	bmax := g * uint64(bmaxUnits)
	c0 := 1 + r.Intn(20)
	// Pool must satisfy Beff(C0) >= B0, i.e. Pool >= C0*B0; add headroom and
	// sometimes cap it so channel growth can force-shrink.
	minPool := uint64(c0) * b0
	pool := minPool + uint64(r.Int63n(int64(bmax)*int64(c0)*3+1))
	return Config{
		Bmin: bmin, Bmax: bmax, G: g, B0: b0,
		T:    uint64(1 + r.Intn(5000)),
		W:    1 + r.Intn(8),
		ThU:  uint64(r.Intn(101)),
		ThD:  uint64(r.Intn(101)),
		Kc:   1 + r.Intn(4),
		C0:   c0,
		Pool: pool,
		H:    r.Intn(6),
	}
}

func genOp(r *rand.Rand) fuzzOp {
	switch r.Intn(10) {
	case 0:
		return fuzzOp{kind: opSetChannels, channels: 1 + r.Intn(40)}
	case 1:
		return fuzzOp{kind: opPause}
	case 2:
		return fuzzOp{kind: opResume}
	case 3:
		// Occasionally out-of-range arguments.
		if r.Intn(2) == 0 {
			return fuzzOp{kind: opSample, bytes: (uint64(1) << 30) + uint64(r.Intn(10)), dt: uint64(1 + r.Intn(1000))}
		}
		return fuzzOp{kind: opSample, bytes: uint64(r.Intn(1 << 20)), dt: uint64(1_000_001 + r.Intn(5))}
	default:
		return fuzzOp{
			kind:  opSample,
			bytes: uint64(r.Intn(120000)),
			dt:    uint64(1 + r.Intn(2000)),
		}
	}
}

func reasonOf(r Result) string {
	switch r.Action {
	case ActionHold:
		return "cand==cur or threshold not met; streak reset"
	case ActionPending:
		return "grow threshold met but streak < need"
	case ActionApplied:
		return "confirmed grow or immediate shrink"
	case ActionSkipped:
		return "pollution flag consumed"
	default:
		return "rejected"
	}
}

func TestRandomizedAgainstNaive(t *testing.T) {
	const sequences = 2000
	for seq := 0; seq < sequences; seq++ {
		r := rand.New(rand.NewSource(int64(seq * 7919)))
		cfg := genValidConfig(r)
		real, err := New(cfg)
		if err != nil {
			t.Fatalf("seq %d: valid config rejected: %+v %v", seq, cfg, err)
		}
		naive := newNaive(cfg)

		n := 30 + r.Intn(40)
		for step := 0; step < n; step++ {
			op := genOp(r)
			switch op.kind {
			case opSample:
				got, gerr := real.Sample(op.bytes, op.dt)
				want := naive.sample(op.bytes, op.dt)
				if t.Failed() {
					return
				}
				if want.ok {
					if gerr != nil {
						t.Fatalf("seq %d step %d unexpected err %v", seq, step, gerr)
					}
					t.Logf("seq=%d step=%d Sample(bytes=%d,dt=%d) -> %s cur=%d R=%d cand=%d streak=%d damp=%d gap=%d | %s",
						seq, step, op.bytes, op.dt, got.Action, got.Cur, got.R, got.Cand, got.Streak,
						real.State().Damp, real.State().Gap, reasonOf(got))
					if got.Action != want.action || got.Cur != want.cur || got.R != want.r ||
						got.Cand != want.cand || got.Streak != want.streak {
						t.Fatalf("seq %d step %d sample mismatch:\n got=%+v\nwant=%+v\ncfg=%+v",
							seq, step, got, want, cfg)
					}
				} else {
					if gerr == nil {
						t.Fatalf("seq %d step %d expected rejection, got %+v", seq, step, got)
					}
					t.Logf("seq=%d step=%d Sample(bytes=%d,dt=%d) -> rejected(%v)",
						seq, step, op.bytes, op.dt, gerr)
				}
			case opSetChannels:
				gerr := real.SetChannels(op.channels)
				_, nerr := naive.setChannels(op.channels)
				t.Logf("seq=%d step=%d SetChannels(%d) -> err=%v", seq, step, op.channels, gerr)
				if (gerr == nil) != (nerr == nil) {
					t.Fatalf("seq %d step %d setchannels mismatch: real=%v naive=%v",
						seq, step, gerr, nerr)
				}
				if (gerr != nil) &&
					((isIllArg(gerr) != isIllArg(nerr)) ||
						(isCapacity(gerr) != isCapacity(nerr))) {
					t.Fatalf("seq %d step %d rejection reason differs: %v vs %v",
						seq, step, gerr, nerr)
				}
			case opPause:
				real.Pause()
				naive.pause()
				t.Logf("seq=%d step=%d Pause()", seq, step)
			case opResume:
				real.Resume()
				naive.resume()
				t.Logf("seq=%d step=%d Resume()", seq, step)
			}

			gs, ns := real.State(), naive.snapshot()
			if gs != ns {
				t.Fatalf("seq %d step %d state mismatch:\n real=%+v\nnaive=%+v\ncfg=%+v\nop=%+v",
					seq, step, gs, ns, cfg, op)
			}
			checkInvariants(t, real, fmt.Sprintf("seq %d step %d", seq, step))
		}
	}
}

func isIllArg(err error) bool   { return err == ErrIllegalArgument }
func isCapacity(err error) bool { return err == ErrInsufficientCapacity }

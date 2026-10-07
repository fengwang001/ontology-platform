package demand_test

import (
	"errors"
	"fmt"
	"math/rand"
	"testing"

	"ontology/demand"
)

// normalizeErr maps an error to its category for comparison.
func normalizeErr(err error) string {
	switch {
	case err == nil:
		return "nil"
	case errors.Is(err, demand.ErrInvalidParam):
		return "param"
	case errors.Is(err, demand.ErrDataIllegal):
		return "data"
	case errors.Is(err, demand.ErrTimeRegression):
		return "time"
	case errors.Is(err, demand.ErrLoadNotFound):
		return "notfound"
	case errors.Is(err, demand.ErrStateNotAllowed):
		return "state"
	default:
		return "unknown:" + err.Error()
	}
}

func actionsString(res demand.ReportResult) string {
	s := "["
	for i, a := range res.Actions {
		if i > 0 {
			s += " "
		}
		s += fmt.Sprintf("%s:%d", a.Kind, a.LoadID)
	}
	return s + "]"
}

func naiveActionsString(res naiveResult) string {
	s := "["
	for i, a := range res.actions {
		if i > 0 {
			s += " "
		}
		kind := "cut"
		if a.restore {
			kind = "restore"
		}
		s += fmt.Sprintf("%s:%d", kind, a.id)
	}
	return s + "]"
}

// TestRandomAgainstNaive drives the optimized controller and the
// independent naive model through identical randomized operation
// sequences and requires identical outcomes on every step. Every step is
// logged with its input, both outputs, and the naive model's decision
// basis (visible with `go test -v`).
func TestRandomAgainstNaive(t *testing.T) {
	for seed := int64(0); seed < 20; seed++ {
		t.Run(fmt.Sprintf("seed-%d", seed), func(t *testing.T) {
			rng := rand.New(rand.NewSource(seed))
			slip := []int64{5, 10, 20}[rng.Intn(3)]
			cfg := demand.Config{
				ContractDemandKW:   int64(50 + rng.Intn(150)),
				WindowSeconds:      slip * int64(2+rng.Intn(5)),
				SlipSeconds:        slip,
				MaxPhysicalPowerKW: 300,
			}
			ctl, err := demand.NewController(cfg)
			if err != nil {
				t.Fatalf("NewController: %v", err)
			}
			nv := newNaive(cfg)
			t.Logf("config: %+v", cfg)

			now := int64(0)
			nextID := 1
			step := 0
			checkPeaks := func() {
				t.Helper()
				cp, cok := ctl.PeakDemand()
				nd, nend, nok := nv.peak()
				if cok != nok || (cok && (cp.DemandKW != nd || cp.WindowEnd != nend)) {
					t.Fatalf("peak mismatch: controller %+v(%v) naive {%g %d}(%v)",
						cp, cok, nd, nend, nok)
				}
			}

			for step = 0; step < 1200; step++ {
				op := rng.Intn(100)
				switch {
				case op < 50: // valid report
					now += 1 + rng.Int63n(8)
					power := int64(rng.Intn(301))
					energy := float64(power) * float64(1) // placeholder, fixed below
					dt := now - max(nv.now(), 0)
					if len(nv.reports) == 0 {
						dt = 1
					}
					energy = float64(power) * float64(dt)
					gotRes, gotErr := ctl.Report(now, energy)
					wantRes, wantErr := nv.report(now, energy)
					if normalizeErr(gotErr) != normalizeErr(wantErr) {
						t.Fatalf("step %d report(%d,%g): err %v vs %v", step, now, energy, gotErr, wantErr)
					}
					got, want := actionsString(gotRes), naiveActionsString(wantRes)
					t.Logf("step %d report(t=%d,e=%g) -> ctl %s sv=%v | naive %s sv=%v | %s",
						step, now, energy, got, gotRes.StillViolating, want, wantRes.stillViolating, nv.decision)
					if got != want || gotRes.StillViolating != wantRes.stillViolating {
						t.Fatalf("step %d report(%d,%g): controller %s sv=%v, naive %s sv=%v",
							step, now, energy, got, gotRes.StillViolating, want, wantRes.stillViolating)
					}
				case op < 60 && nextID <= 8: // add load
					spec := demand.LoadSpec{
						ID:            nextID,
						RatedPowerKW:  int64(5 + rng.Intn(26)),
						Priority:      rng.Intn(5),
						MinOnSeconds:  rng.Int63n(11),
						MinOffSeconds: rng.Int63n(11),
					}
					nextID++
					gotErr := ctl.AddLoad(spec)
					wantErr := nv.addLoad(spec)
					t.Logf("step %d addLoad(%+v) -> %v / %v", step, spec, gotErr, wantErr)
					if normalizeErr(gotErr) != normalizeErr(wantErr) {
						t.Fatalf("step %d addLoad(%+v): %v vs %v", step, spec, gotErr, wantErr)
					}
				case op < 68: // lock
					id := 1 + rng.Intn(max(nextID-1, 1))
					gotErr := ctl.LockLoad(id)
					wantErr := nv.lock(id)
					t.Logf("step %d lock(%d) -> %v / %v", step, id, gotErr, wantErr)
					if normalizeErr(gotErr) != normalizeErr(wantErr) {
						t.Fatalf("step %d lock(%d): %v vs %v", step, id, gotErr, wantErr)
					}
				case op < 76: // unlock
					id := 1 + rng.Intn(max(nextID-1, 1))
					gotErr := ctl.UnlockLoad(id)
					wantErr := nv.unlock(id)
					t.Logf("step %d unlock(%d) -> %v / %v", step, id, gotErr, wantErr)
					if normalizeErr(gotErr) != normalizeErr(wantErr) {
						t.Fatalf("step %d unlock(%d): %v vs %v", step, id, gotErr, wantErr)
					}
				case op < 80: // remove (always rejected for existing loads)
					id := 1 + rng.Intn(max(nextID-1, 1))
					gotErr := ctl.RemoveLoad(id)
					wantErr := nv.removeLoad(id)
					t.Logf("step %d remove(%d) -> %v / %v", step, id, gotErr, wantErr)
					if normalizeErr(gotErr) != normalizeErr(wantErr) {
						t.Fatalf("step %d remove(%d): %v vs %v", step, id, gotErr, wantErr)
					}
				default: // invalid report, must be rejected identically
					kind := rng.Intn(4)
					if len(nv.reports) == 0 {
						kind = 0 // before any accepted report only negative energy is invalid
					}
					var ts int64
					var energy float64
					switch kind {
					case 0: // negative energy
						ts, energy = now+1, -5
					case 1: // regressed, zero energy
						ts, energy = now, 0
					case 2: // regressed, positive energy -> illegal data
						ts, energy = now, 10
					default: // over physical limit
						ts, energy = now+2, 301*2
					}
					_, gotErr := ctl.Report(ts, energy)
					_, wantErr := nv.report(ts, energy)
					t.Logf("step %d badReport(t=%d,e=%g) -> %v / %v", step, ts, energy, gotErr, wantErr)
					if normalizeErr(gotErr) != normalizeErr(wantErr) {
						t.Fatalf("step %d badReport(%d,%g): %v vs %v", step, ts, energy, gotErr, wantErr)
					}
					if gotErr == nil {
						t.Fatalf("step %d badReport(%d,%g) unexpectedly accepted", step, ts, energy)
					}
				}
				if step%50 == 0 {
					checkPeaks()
				}
			}
			checkPeaks()
			t.Logf("seed %d done: %d steps, %d loads, final time %d", seed, step, nextID-1, now)
		})
	}
}

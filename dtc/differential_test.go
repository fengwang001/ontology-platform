package dtc

import (
	"errors"
	"fmt"
	"math/rand"
	"testing"
)

// errCode extracts the dtc error code, or 0 for nil.
func errCode(err error) ErrCode {
	if err == nil {
		return 0
	}
	var derr *Error
	if errors.As(err, &derr) {
		return derr.Code
	}
	return -1
}

// TestDifferentialRandom replays long random event sequences against
// the incremental Manager and the replay-based naive model, comparing
// accept/reject outcomes and full query snapshots after every event.
// Every event, its outcome and the deciding state are logged (go test
// -v) so failures can be traced to a concrete divergence.
func TestDifferentialRandom(t *testing.T) {
	for seed := int64(1); seed <= 40; seed++ {
		seed := seed
		t.Run(fmt.Sprintf("seed=%d", seed), func(t *testing.T) {
			rng := rand.New(rand.NewSource(seed))
			cfg := randomConfig(rng)
			ids := []string{"A", "B", "C"}
			sevs := map[string]int{}

			m, err := NewManager(cfg)
			if err != nil {
				t.Fatalf("config %+v rejected: %v", cfg, err)
			}
			for _, id := range ids {
				sev := 1 + rng.Intn(3)
				sevs[id] = sev
				if err := m.Register(id, sev); err != nil {
					t.Fatal(err)
				}
			}
			nv := newNaive(cfg, sevs)
			t.Logf("config=%+v severities=%v", cfg, sevs)

			var ts, odo int64 = 1000, 50000
			ignitionOn := false
			for i := 0; i < 400; i++ {
				ev, advanced := genEvent(rng, ids, ignitionOn, ts, odo)
				if advanced {
					ts, odo = ev.Time, ev.Odometer
				}

				errM := m.Handle(ev)
				errN := nv.handle(ev)
				codeM, codeN := errCode(errM), errCode(errN)
				t.Logf("event %03d: kind=%s dtc=%q passed=%v t=%d odo=%d speed=%d cool=%d -> manager=%v naive=%v",
					i, ev.Kind, ev.DTC, ev.Passed, ev.Time, ev.Odometer,
					ev.Speed, ev.Coolant, codeM, codeN)
				if codeM != codeN {
					t.Fatalf("error mismatch: manager=%v (%v) naive=%v (%v)",
						codeM, errM, codeN, errN)
				}
				if errM == nil {
					switch ev.Kind {
					case EvIgnitionOn:
						ignitionOn = true
					case EvIgnitionOff:
						ignitionOn = false
					}
				}

				for _, id := range ids {
					got, err := m.Query(id)
					if err != nil {
						t.Fatalf("Query(%q): %v", id, err)
					}
					want := nv.query(id)
					if got != want {
						t.Fatalf("snapshot mismatch for %q after event %d:\nmanager=%+v\nnaive  =%+v",
							id, i, got, want)
					}
					t.Logf("  %s: pending=%v confirmed=%v healed=%v judge=%v occ=%d consec=%d warm=%d ff=%v dist=%d",
						id, got.Pending, got.Confirmed, got.Healed, got.Judgment,
						got.Occurrences, got.ConsecFailCycles, got.FaultFreeWarmups,
						got.HasFreezeFrame, got.DistanceSinceClear)
				}
				if got, want := m.FreezeFrame(), nv.freezeFrame(); got != want {
					t.Fatalf("freeze frame mismatch after event %d:\nmanager=%+v\nnaive  =%+v",
						i, got, want)
				}
			}
		})
	}
}

// randomConfig draws a valid configuration with small numbers so that
// lifecycle transitions actually happen within a few hundred events.
func randomConfig(rng *rand.Rand) Config {
	heal := 1 + rng.Intn(3)
	return Config{
		DebounceRiseStep:  1 + rng.Intn(3),
		DebounceFailLimit: 1 + rng.Intn(4),
		DebounceFallStep:  1 + rng.Intn(3),
		DebouncePassLimit: -rng.Intn(3), // in [-2, 0]
		ConfirmCycles:     1 + rng.Intn(3),
		HealWarmupCycles:  heal,
		AutoClearWarmups:  heal + rng.Intn(4),
		WarmupRise:        5 + rng.Intn(15),
		WarmupFinalTemp:   60 + rng.Intn(30),
	}
}

// genEvent produces the next event. It returns advanced=true when the
// event carries fresh monotone time/odometer (regression events reuse
// smaller values and do not move the watermark).
func genEvent(rng *rand.Rand, ids []string, ignitionOn bool, ts, odo int64) (Event, bool) {
	// ~8% of events are deliberately broken.
	if rng.Intn(100) < 8 {
		return genInvalidEvent(rng, ids, ignitionOn, ts, odo), false
	}

	ts += int64(rng.Intn(3))
	odo += int64(rng.Intn(20))
	ev := Event{Time: ts, Odometer: odo}

	if !ignitionOn {
		switch r := rng.Intn(100); {
		case r < 80:
			ev.Kind = EvIgnitionOn
		case r < 90:
			ev.Kind = EvClear
		default:
			ev.Kind = EvIgnitionOff // illegal: already off
		}
		return ev, true
	}

	switch r := rng.Intn(100); {
	case r < 45:
		ev.Kind = EvMonitorResult
		ev.DTC = ids[rng.Intn(len(ids))]
		ev.Passed = rng.Intn(100) < 45
	case r < 55:
		ev.Kind = EvMonitorResult
		ev.DTC = "ZZZ" // unregistered
		ev.Passed = rng.Intn(2) == 0
	case r < 80:
		ev.Kind = EvEnvSample
		ev.Speed = rng.Intn(180)
		// Coolant profiles that sometimes qualify as warm-up.
		ev.Coolant = 15 + rng.Intn(80)
	case r < 90:
		ev.Kind = EvIgnitionOff
	default:
		ev.Kind = EvClear // illegal state: ignition on
	}
	return ev, true
}

// genInvalidEvent produces events violating parameter, regression or
// ordering rules. The returned event must not move the watermark.
func genInvalidEvent(rng *rand.Rand, ids []string, ignitionOn bool, ts, odo int64) Event {
	switch rng.Intn(5) {
	case 0: // time regression
		back := int64(1 + rng.Intn(5))
		if ts-back < 0 {
			back = ts + 1
		}
		return Event{Kind: EvMonitorResult, DTC: ids[rng.Intn(len(ids))],
			Time: ts - back, Odometer: odo}
	case 1: // odometer regression
		back := int64(1 + rng.Intn(50))
		if odo-back < 0 {
			back = odo + 1
		}
		return Event{Kind: EvEnvSample, Speed: 10, Coolant: 30,
			Time: ts, Odometer: odo - back}
	case 2: // negative parameters
		return Event{Kind: EvMonitorResult, DTC: ids[rng.Intn(len(ids))],
			Time: -1, Odometer: odo}
	case 3: // unknown event kind
		return Event{Kind: EventKind(99), Time: ts, Odometer: odo}
	default: // double ignition on / off
		if ignitionOn {
			return Event{Kind: EvIgnitionOn, Time: ts, Odometer: odo}
		}
		return Event{Kind: EvIgnitionOff, Time: ts, Odometer: odo}
	}
}

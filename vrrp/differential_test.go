package vrrp

import (
	"errors"
	"math/rand"
	"slices"
	"testing"
)

func errKindOf(err error) ErrKind {
	if err == nil {
		return 0
	}
	var verr *Error
	if errors.As(err, &verr) {
		return verr.Kind
	}
	return -1
}

// randomConfig builds a randomized but valid config. Owner configs are
// produced with a small probability.
func randomConfig(rng *rand.Rand, ids []string) Config {
	prio := 1 + rng.Intn(254)
	if rng.Intn(20) == 0 {
		prio = OwnerPriority
	}
	return Config{
		ID:               ids[rng.Intn(len(ids))],
		Priority:         prio,
		Preempt:          rng.Intn(2) == 0,
		AdvertIntervalMs: 1 + rng.Intn(50),
	}
}

// randomEvent generates the next event. now is the generator clock which
// only moves forward on advance events; rollback and invalid events are
// mixed in to exercise rejection paths.
func randomEvent(rng *rand.Rand, ids []string, now uint64) (Event, uint64) {
	kind := rng.Intn(100)
	switch {
	case kind < 8:
		return Start(now), now
	case kind < 14:
		return Stop(now), now
	case kind < 55:
		adv := Advert{
			SenderID:   ids[rng.Intn(len(ids))],
			IntervalMs: 1 + rng.Intn(50),
		}
		switch r := rng.Intn(100); {
		case r < 15:
			adv.Priority = YieldPriority
		case r < 25:
			adv.Priority = OwnerPriority
		case r < 30: // invalid priority
			adv.Priority = -1 + rng.Intn(2)*257 // -1 or 256
		default:
			adv.Priority = 1 + rng.Intn(254)
		}
		if rng.Intn(20) == 0 { // invalid interval
			adv.IntervalMs = rng.Intn(2) * (MaxAdvertIntervalMs + 1)
		}
		if rng.Intn(30) == 0 { // invalid empty sender
			adv.SenderID = ""
		}
		return ReceiveAdvert(adv, now), now
	case kind < 80:
		step := rng.Uint64() % 160
		return AdvanceTime(now + step), now + step
	case kind < 88:
		return TakeAdvert(now), now
	case kind < 95:
		p := 1 + rng.Intn(255) // includes 255: always rejected at runtime
		if rng.Intn(15) == 0 {
			p = rng.Intn(2) * 256 // 0 or 256: invalid
		}
		return SetPriority(p, now), now
	default:
		return SetPreempt(rng.Intn(2) == 0, now), now
	}
}

// TestDifferentialRandom runs well over a thousand random event sequences
// through both the real Device and the independent naive model, comparing
// outputs, error kinds and full state after every step, and logging each
// step's input, output and decision rationale.
func TestDifferentialRandom(t *testing.T) {
	const (
		sequences = 1500
		maxSteps  = 40
	)
	ids := []string{"alpha", "beta", "gamma"}
	rng := rand.New(rand.NewSource(20261007))
	for seq := 0; seq < sequences; seq++ {
		cfg := randomConfig(rng, ids)
		dev, err := NewDevice(cfg)
		if err != nil {
			t.Fatalf("seq %d: NewDevice: %v", seq, err)
		}
		model := newNaiveModel(cfg)
		now := uint64(0)
		steps := 1 + rng.Intn(maxSteps)
		for step := 0; step < steps; step++ {
			ev, next := randomEvent(rng, ids, now)
			// Occasionally roll the clock back to exercise rejection.
			if now > 20 && rng.Intn(25) == 0 {
				ev.Now = now - 1 - rng.Uint64()%20
			} else {
				now = next
			}

			devRes, devErr := dev.Handle(ev)
			modRes, modErr := model.handle(ev)

			t.Logf("seq=%d step=%d in=%s | dev: out=%v err=%v why=%q | model: out=%v err=%v why=%q",
				seq, step, ev, devRes.Adverts, devErr, devRes.Reason,
				modRes.Adverts, modErr, modRes.Reason)

			if dk, mk := errKindOf(devErr), errKindOf(modErr); dk != mk {
				t.Fatalf("seq %d step %d in=%s: error kind dev=%v model=%v",
					seq, step, ev, dk, mk)
			}
			if !slices.Equal(devRes.Adverts, modRes.Adverts) {
				t.Fatalf("seq %d step %d in=%s: adverts dev=%v model=%v",
					seq, step, ev, devRes.Adverts, modRes.Adverts)
			}
			if ds, ms := dev.Snapshot(), model.snapshot(); ds != ms {
				t.Fatalf("seq %d step %d in=%s: state dev=%+v model=%+v",
					seq, step, ev, ds, ms)
			}
		}
	}
}

// TestDifferentialDeterministic replays a fixed set of tricky sequences
// through both implementations.
func TestDifferentialDeterministic(t *testing.T) {
	sequences := [][]Event{
		{Start(0), AdvanceTime(300), TakeAdvert(400), Stop(500), Start(600), AdvanceTime(900)},
		{
			Start(0),
			ReceiveAdvert(Advert{SenderID: "x", Priority: 0, IntervalMs: 100}, 10),
			AdvanceTime(12), AdvanceTime(13),
		},
		{
			Start(0),
			ReceiveAdvert(Advert{SenderID: "x", Priority: 0, IntervalMs: 100}, 10),
			ReceiveAdvert(Advert{SenderID: "x", Priority: 200, IntervalMs: 100}, 15),
			AdvanceTime(13), AdvanceTime(315),
		},
	}
	for i, seq := range sequences {
		cfg := testConfig()
		dev := mustDevice(t, cfg)
		model := newNaiveModel(cfg)
		for j, ev := range seq {
			devRes, devErr := dev.Handle(ev)
			modRes, modErr := model.handle(ev)
			if errKindOf(devErr) != errKindOf(modErr) ||
				!slices.Equal(devRes.Adverts, modRes.Adverts) ||
				dev.Snapshot() != model.snapshot() {
				t.Fatalf("seq %d step %d in=%s: dev=(%v,%v) model=(%v,%v)",
					i, j, ev, devRes, devErr, modRes, modErr)
			}
		}
	}
}

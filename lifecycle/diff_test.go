package lifecycle

import (
	"math/rand"
	"testing"
	"time"
)

// diffWorld is a self-renewing object: S --tick(d)--> S. Randomized guard
// flags and a random "next" pointer let the same declarations exercise
// chains, guards and cascades in both models.
func buildDiffWorld(t *testing.T, eng *Engine, sc *Scanner, n int, rng *rand.Rand) (ids []InstanceID, durations map[InstanceID]time.Duration) {
	t.Helper()
	durations = make(map[InstanceID]time.Duration)
	for i := 0; i < n; i++ {
		id := InstanceID("n" + itoa(i))
		d := time.Duration(1+rng.Intn(5)) * time.Minute
		durations[id] = d
		ids = append(ids, id)
	}
	// One transition per node duration is not representable on a shared type,
	// so give each node its own type sharing the same guard contract.
	for i, id := range ids {
		d := durations[id]
		next := ""
		if rng.Intn(2) == 0 && len(ids) > 1 {
			j := rng.Intn(len(ids))
			for j == i {
				j = rng.Intn(len(ids))
			}
			next = string(ids[j])
		}
		tr := Transition{
			ID: "tick", From: "S", To: "S", Kind: KindTimeTransition, Duration: d,
			Guard: func(c GuardContext) bool {
				v, _ := c.Prop("open")
				return v == true
			},
		}
		if next != "" {
			tr.Cascade = &CascadeSpec{
				Target: func(c GuardContext) (InstanceID, bool) {
					v, ok := c.Prop("next")
					if !ok {
						return "", false
					}
					s, ok := v.(string)
					return InstanceID(s), ok && s != ""
				},
				Transition: "forced",
			}
		}
		ot := &ObjectType{
			ID:          ObjectTypeID("dnode-" + string(id)),
			States:      []StateID{"S"},
			Initial:     "S",
			Transitions: []Transition{tr, {ID: "forced", From: "S", To: "S", Kind: KindActionTransition}},
		}
		if err := eng.RegisterType(ot); err != nil {
			t.Fatal(err)
		}
		if err := sc.RegisterType(ot); err != nil {
			t.Fatal(err)
		}
	}
	return ids, durations
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b []byte
	for i > 0 {
		b = append([]byte{byte('0' + i%10)}, b...)
		i /= 10
	}
	return string(b)
}

// TestRandomDifferential drives the lazy engine and the independent naive
// scanner through identical random event streams (time jumps, guard toggles,
// links, actions) and asserts state equality at every access and on random
// historical probes.
func TestRandomDifferential(t *testing.T) {
	const runs, steps, nodes = 40, 300, 4
	for run := 0; run < runs; run++ {
		rng := rand.New(rand.NewSource(int64(run*1_000_003 + 42)))
		clk := NewFixedClock(t0())
		eng := NewEngine(clk)
		eng.SetLogger(NewStdLogger(testWriter{t}))
		sc := NewScanner(time.Minute, t0())

		ids, _ := buildDiffWorld(t, eng, sc, nodes, rng)
		for _, id := range ids {
			props := map[string]any{"open": rng.Intn(2) == 0}
			if rng.Intn(2) == 0 {
				props["next"] = string(ids[rng.Intn(len(ids))])
			}
			if err := eng.Spawn(ObjectTypeID("dnode-"+string(id)), id, "", props); err != nil {
				t.Fatal(err)
			}
			if err := sc.Spawn(ObjectTypeID("dnode-"+string(id)), id, "", props); err != nil {
				t.Fatal(err)
			}
		}

		virtual := t0()
		for s := 0; s < steps; s++ {
			// Both models share the exact same virtual time progression.
			virtual = virtual.Add(time.Duration(rng.Intn(4)) * time.Minute)
			clk.Set(virtual)
			sc.Tick(virtual)

			id := ids[rng.Intn(len(ids))]
			switch rng.Intn(6) {
			case 0, 1:
				esnap, _ := eng.Read(id)
				got, want := esnap.State, sc.State(id)
				if got != want {
					t.Fatalf("run=%d step=%d id=%s lazy=%s scan=%s", run, s, id, got, want)
				}
			case 2:
				eng.mu.Lock()
				eng.instances[id].props["open"] = rng.Intn(2) == 0
				eng.mu.Unlock()
				sc.mu.Lock()
				sc.instances[id].props["open"] = engProp(eng, id, "open")
				sc.mu.Unlock()
			case 3:
				target := ids[rng.Intn(len(ids))]
				eng.AddLink(id, target)
				sc.AddLink(id, target)
				eng.mu.Lock()
				eng.instances[id].props["next"] = string(target)
				eng.mu.Unlock()
				sc.mu.Lock()
				sc.instances[id].props["next"] = string(target)
				sc.mu.Unlock()
			case 4:
				target := ids[rng.Intn(len(ids))]
				eng.RemoveLink(id, target)
				sc.RemoveLink(id, target)
			case 5:
				// Historical replay must agree with the scanner's own replay
				// at a random past instant.
				at := t0().Add(time.Duration(rng.Int63n(int64(virtual.Sub(t0())) + 1)))
				got, err := eng.StateAt(id, at)
				if err != nil {
					t.Fatal(err)
				}
				want := sc.StateAt(id, at)
				if got != want {
					t.Fatalf("run=%d step=%d StateAt id=%s at=%v lazy=%s scan=%s", run, s, id, at, got, want)
				}
			}
		}
		for _, id := range ids {
			if snap, _ := eng.Read(id); snap.State != sc.State(id) {
				t.Fatalf("run=%d final mismatch id=%s lazy=%s scan=%s", run, id, snap.State, sc.State(id))
			}
		}
	}
}

func engProp(e *Engine, id InstanceID, key string) any {
	e.mu.Lock()
	defer e.mu.Unlock()
	val, _ := e.instances[id].props[key]
	return val
}

type testWriter struct{ t *testing.T }

func (w testWriter) Write(p []byte) (int, error) {
	w.t.Logf("%s", p)
	return len(p), nil
}

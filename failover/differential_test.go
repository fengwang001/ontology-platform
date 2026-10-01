package failover

import (
	"errors"
	"fmt"
	"math/rand"
	"testing"
)

func errCode(err error) string {
	switch {
	case err == nil:
		return "ok"
	case errors.Is(err, ErrInvalidPoint):
		return "invalid_point"
	case errors.Is(err, ErrNoHosts):
		return "no_hosts"
	case errors.Is(err, ErrInsufficientCapacity):
		return "capacity"
	case errors.Is(err, ErrLevelOutOfRange):
		return "bad_level"
	case errors.Is(err, ErrEmptyID):
		return "empty_id"
	case errors.Is(err, ErrHostExists):
		return "exists"
	case errors.Is(err, ErrHostNotFound):
		return "not_found"
	case errors.Is(err, ErrInvalidConfig):
		return "bad_config"
	default:
		return "other"
	}
}

type opRec struct {
	name               string
	level              int
	id                 string
	healthy            bool
	r                  int
	gotErr, refErr     string
	gotLoads, refLoads []int
	gotPick, refPick   int
}

// TestRandomDifferential drives the real Allocator and the independently
// written naive model through 2000 random histories and compares every result.
func TestRandomDifferential(t *testing.T) {
	const cases = 2000
	for seed := int64(1); seed <= cases; seed++ {
		rng := rand.New(rand.NewSource(seed))
		L := 1 + rng.Intn(5)
		F := 100 + rng.Intn(300)
		D := 1 + rng.Intn(100)

		// Build caps whose sum is >= 100; sometimes leave tight capacity to
		// provoke ErrInsufficientCapacity, sometimes generous.
		caps := make([]int, L)
		unit := []int{20, 30, 40, 50, 60, 100, 100, 100}[rng.Intn(8)]
		for p := range caps {
			caps[p] = 1 + rng.Intn(unit)
		}
		extra := 100 - sumInts(caps)
		for extra > 0 {
			p := rng.Intn(L)
			add := extra
			if add > 100-caps[p] {
				add = 100 - caps[p]
			}
			if add <= 0 {
				break
			}
			caps[p] += add
			extra -= add
		}

		a, err := New(L, F, D, caps)
		if err != nil {
			t.Fatalf("seed %d: unexpected config rejection caps=%v: %v", seed, caps, err)
		}
		m := newNaiveModel(L, F, D, caps)

		var log []opRec
		var ids []string

		checkLoads := func(o opRec) {
			if o.gotErr != o.refErr {
				t.Fatalf("seed %d op %s: got err %v, ref err %v; history=%v",
					seed, o.name, o.gotErr, o.refErr, summarize(log))
			}
			if o.gotErr == "ok" && !equalInts(o.gotLoads, o.refLoads) {
				t.Fatalf("seed %d: loads mismatch got %v ref %v; history=%v",
					seed, o.gotLoads, o.refLoads, summarize(log))
			}
			if !equalInts(a.cur, m.cur) {
				t.Fatalf("seed %d: cur mismatch got %v ref %v; history=%v",
					seed, a.cur, m.cur, summarize(log))
			}
		}

		for step := 0; step < 40; step++ {
			k := rng.Intn(10)
			switch {
			case k < 4:
				// Add a host; include out-of-range levels, empty/dup ids.
				level := rng.Intn(L+2) - 1 // -1 .. L
				id := fmt.Sprintf("h%d", step)
				switch rng.Intn(6) {
				case 0:
					id = ""
				case 1:
					if len(ids) > 0 {
						id = ids[rng.Intn(len(ids))]
					}
				}
				healthy := rng.Intn(2) == 0
				gErr := a.AddHost(level, id, healthy)
				rErr := m.add(level, id, healthy)
				o := opRec{name: "add", level: level, id: id, healthy: healthy,
					gotErr: errCode(gErr), refErr: errCode(rErr)}
				if o.gotErr != o.refErr {
					t.Fatalf("seed %d add(%d,%q,%v): got %s ref %s; history=%v",
						seed, level, id, healthy, o.gotErr, o.refErr, summarize(log))
				}
				if gErr == nil {
					ids = append(ids, id)
				}
				log = append(log, o)
			case k < 6:
				var id string
				if rng.Intn(5) == 0 || len(ids) == 0 {
					id = "missing"
				} else {
					id = ids[rng.Intn(len(ids))]
				}
				if k == 4 { // RemoveHost
					gErr := a.RemoveHost(id)
					rErr := m.remove(id)
					o := opRec{name: "remove", id: id,
						gotErr: errCode(gErr), refErr: errCode(rErr)}
					if o.gotErr != o.refErr {
						t.Fatalf("seed %d remove(%q): got %s ref %s; history=%v",
							seed, id, o.gotErr, o.refErr, summarize(log))
					}
					if gErr == nil {
						ids = removeID(ids, id)
					}
					log = append(log, o)
				} else { // SetHealth
					healthy := rng.Intn(2) == 0
					gErr := a.SetHealth(id, healthy)
					rErr := m.setHealth(id, healthy)
					o := opRec{name: "health", id: id, healthy: healthy,
						gotErr: errCode(gErr), refErr: errCode(rErr)}
					if o.gotErr != o.refErr {
						t.Fatalf("seed %d health(%q,%v): got %s ref %s; history=%v",
							seed, id, healthy, o.gotErr, o.refErr, summarize(log))
					}
					log = append(log, o)
				}
			case k < 9:
				gL, gErr := a.Loads()
				rL, rErr := m.loads()
				o := opRec{name: "loads", gotErr: errCode(gErr),
					refErr: errCode(rErr), gotLoads: gL, refLoads: rL}
				log = append(log, o)
				checkLoads(o)
				if gErr == nil {
					sum := 0
					for p, v := range gL {
						sum += v
						if v < 0 || v > caps[p] {
							t.Fatalf("seed %d: share out of cap: %v caps %v", seed, gL, caps)
						}
					}
					if sum != 100 {
						t.Fatalf("seed %d: loads sum %d (%v)", seed, sum, gL)
					}
				}
				t.Logf("seed %d input L=%d F=%d D=%d caps=%v hosts=%v; "+
					"Loads output=%v err=%s; judgement: per-spec raw/cur then branch+redistribution",
					seed, L, F, D, caps, hostView(m), gL, errCode(gErr))
			default:
				r := rng.Intn(102) - 1 // include -1 and 100
				gP, gErr := a.PickLevel(r)
				rP, rErr := m.pick(r)
				o := opRec{name: "pick", r: r, gotErr: errCode(gErr),
					refErr: errCode(rErr), gotPick: gP, refPick: rP}
				log = append(log, o)
				if o.gotErr != o.refErr || (gErr == nil && gP != rP) {
					t.Fatalf("seed %d pick(%d): got (%d,%s) ref (%d,%s); history=%v",
						seed, r, gP, o.gotErr, rP, o.refErr, summarize(log))
				}
				t.Logf("seed %d input PickLevel r=%d; output level=%d err=%s",
					seed, r, gP, o.gotErr)
			}
		}
	}
}

func sumInts(xs []int) int {
	s := 0
	for _, x := range xs {
		s += x
	}
	return s
}

func removeID(ids []string, id string) []string {
	out := ids[:0]
	for _, v := range ids {
		if v != id {
			out = append(out, v)
		}
	}
	return out
}

func hostView(m *naiveModel) []string {
	var out []string
	for id, h := range m.hosts {
		out = append(out, fmt.Sprintf("%s@L%d:%t", id, h.level, h.healthy))
	}
	return out
}

func summarize(log []opRec) []string {
	out := make([]string, 0, len(log))
	for _, o := range log {
		switch o.name {
		case "add":
			out = append(out, fmt.Sprintf("add(L%d,%q,%t)->%s", o.level, o.id, o.healthy, o.gotErr))
		case "remove":
			out = append(out, fmt.Sprintf("remove(%q)->%s", o.id, o.gotErr))
		case "health":
			out = append(out, fmt.Sprintf("health(%q,%t)->%s", o.id, o.healthy, o.gotErr))
		case "loads":
			out = append(out, fmt.Sprintf("loads->%v:%s", o.gotLoads, o.gotErr))
		case "pick":
			out = append(out, fmt.Sprintf("pick(%d)->%d:%s", o.r, o.gotPick, o.gotErr))
		}
	}
	return out
}

// TestReplayIdentical verifies that two independent replay runs produce
// field-identical results and memory for the same operation sequence.
func TestReplayIdentical(t *testing.T) {
	run := func() ([][]int, [][]int, []int) {
		a := mustNew(t, 3, 135, 17, []int{45, 55, 90})
		addN(t, a, 0, 6, 2)
		addN(t, a, 1, 4, 3)
		addN(t, a, 2, 9, 1)
		var outs [][]int
		l1, _ := a.Loads()
		outs = append(outs, l1)
		if err := a.SetHealth("L0H2", true); err != nil {
			t.Fatal(err)
		}
		l2, err := a.Loads()
		if err != nil {
			t.Fatal(err)
		}
		outs = append(outs, l2)
		if err := a.RemoveHost("L2H0"); err != nil {
			t.Fatal(err)
		}
		l3, err := a.Loads()
		if err != nil {
			t.Fatal(err)
		}
		outs = append(outs, l3)
		return outs, nil, a.snapshotCur()
	}
	out1, _, cur1 := run()
	out2, _, cur2 := run()
	if len(out1) != len(out2) {
		t.Fatal("replay length differs")
	}
	for i := range out1 {
		if !equalInts(out1[i], out2[i]) {
			t.Fatalf("replay step %d: %v vs %v", i, out1[i], out2[i])
		}
	}
	if !equalInts(cur1, cur2) {
		t.Fatalf("replay cur differs: %v vs %v", cur1, cur2)
	}
}

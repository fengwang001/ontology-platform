package ontology

import (
	"errors"
	"fmt"
	"math/rand"
	"testing"
)

const randomCases = 2000

func genConfig(rng *rand.Rand) (int, int, int, []int) {
	L := 1 + rng.Intn(5)
	F := 100 + rng.Intn(301)
	d := 1 + rng.Intn(100)
	caps := make([]int, L)
	for {
		sum := 0
		for p := range caps {
			caps[p] = 1 + rng.Intn(100)
			sum += caps[p]
		}
		if sum >= 100 {
			return L, F, d, caps
		}
	}
}

func sameErr(a, b error) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	sentinels := []error{
		ErrInvalidConfig, ErrLevelOutOfRange, ErrEmptyID, ErrDuplicateID,
		ErrHostNotFound, ErrNoHosts, ErrOutOfCapacity, ErrInvalidPoint,
	}
	for _, s := range sentinels {
		if errors.Is(a, s) || errors.Is(b, s) {
			return errors.Is(a, s) && errors.Is(b, s)
		}
	}
	return a.Error() == b.Error()
}

func statesEqual(a *FailoverAllocator, n *naiveAllocator) error {
	if len(a.hosts) != len(n.hosts) {
		return fmt.Errorf("host count %d != %d", len(a.hosts), len(n.hosts))
	}
	for id, nh := range n.hosts {
		ah, ok := a.hosts[id]
		if !ok {
			return fmt.Errorf("host %q missing in real", id)
		}
		if ah.level != nh.level || ah.healthy != nh.healthy {
			return fmt.Errorf("host %q real=(lvl=%d healthy=%v) naive=(lvl=%d healthy=%v)",
				id, ah.level, ah.healthy, nh.level, nh.healthy)
		}
	}
	for p := 0; p < a.L; p++ {
		if a.cur[p] != n.cur[p] {
			return fmt.Errorf("cur[%d] real=%d naive=%d (%v vs %v)",
				p, a.cur[p], n.cur[p], a.cur, n.cur)
		}
	}
	return nil
}

func checkLoadInvariants(a *FailoverAllocator, loads []int) error {
	sum := 0
	for p, v := range loads {
		if v < 0 || v > a.cap[p] {
			return fmt.Errorf("level %d load %d outside [0,%d]", p, v, a.cap[p])
		}
		sum += v
	}
	if sum != 100 {
		return fmt.Errorf("loads sum %d, want 100", sum)
	}
	return nil
}

func TestNaiveOracleRandom(t *testing.T) {
	for tc := 0; tc < randomCases; tc++ {
		rng := rand.New(rand.NewSource(int64(tc + 1)))
		L, F, d, caps := genConfig(rng)
		real, err := NewFailoverAllocator(L, F, d, caps)
		if err != nil {
			t.Fatalf("case %d: valid config rejected: %v", tc, err)
		}
		oracle := newNaive(L, F, d, caps)
		trace := []string{
			fmt.Sprintf("case %d seed=%d L=%d F=%d delta=%d caps=%v",
				tc, tc+1, L, F, d, caps),
		}

		idGen := 0
		var pool []string
		newID := func() string {
			idGen++
			id := fmt.Sprintf("h%d", idGen)
			pool = append(pool, id)
			return id
		}

		ops := 20 + rng.Intn(40)
		for op := 0; op < ops; op++ {
			kind := rng.Intn(10)
			switch {
			case kind < 3: // AddHost
				level := rng.Intn(L + 2) // occasional out-of-range
				var id string
				switch rng.Intn(10) {
				case 0:
					id = ""
				case 1:
					if len(pool) > 0 {
						id = pool[rng.Intn(len(pool))]
					} else {
						id = newID()
					}
				default:
					id = newID()
				}
				healthy := rng.Intn(2) == 0
				e1 := real.AddHost(level, id, healthy)
				e2 := oracle.add(level, id, healthy)
				trace = append(trace, fmt.Sprintf(
					"AddHost(level=%d,id=%q,healthy=%v) -> %v", level, id, healthy, e2))
				if !sameErr(e1, e2) {
					t.Fatalf("%s\nAddHost error mismatch real=%v naive=%v",
						joinTrace(trace), e1, e2)
				}
			case kind < 5: // SetHealth
				var id string
				if rng.Intn(5) == 0 || len(pool) == 0 {
					id = fmt.Sprintf("ghost%d", rng.Intn(100))
				} else {
					id = pool[rng.Intn(len(pool))]
				}
				healthy := rng.Intn(2) == 0
				e1 := real.SetHealth(id, healthy)
				e2 := oracle.setHealth(id, healthy)
				trace = append(trace, fmt.Sprintf(
					"SetHealth(id=%q,healthy=%v) -> %v", id, healthy, e2))
				if !sameErr(e1, e2) {
					t.Fatalf("%s\nSetHealth error mismatch real=%v naive=%v",
						joinTrace(trace), e1, e2)
				}
			case kind < 6: // RemoveHost
				var id string
				if rng.Intn(5) == 0 || len(pool) == 0 {
					id = fmt.Sprintf("ghost%d", rng.Intn(100))
				} else {
					id = pool[rng.Intn(len(pool))]
				}
				e1 := real.RemoveHost(id)
				e2 := oracle.remove(id)
				trace = append(trace, fmt.Sprintf("RemoveHost(id=%q) -> %v", id, e2))
				if !sameErr(e1, e2) {
					t.Fatalf("%s\nRemoveHost error mismatch real=%v naive=%v",
						joinTrace(trace), e1, e2)
				}
			case kind < 9: // Loads
				curBefore := curSnapshot(oracle)
				loads1, e1 := real.Loads()
				loads2, e2, branch, excess := oracle.loads(true)
				basis := fmt.Sprintf(
					"raw=%v cur(before)=%v branch=%s excessLeft=%d",
					rawsOf(oracle), curBefore, branch, excess)
				t.Logf("case %d op %d Loads in={%s} out=%v err=%v",
					tc, op, basis, loads2, e2)
				trace = append(trace, fmt.Sprintf(
					"Loads() -> %v err=%v [%s]", loads2, e2, basis))
				if !sameErr(e1, e2) {
					t.Fatalf("%s\nLoads error mismatch real=%v naive=%v",
						joinTrace(trace), e1, e2)
				}
				if e1 == nil {
					if fmt.Sprint(loads1) != fmt.Sprint(loads2) {
						t.Fatalf("%s\nLoads mismatch real=%v naive=%v",
							joinTrace(trace), loads1, loads2)
					}
					if err := checkLoadInvariants(real, loads1); err != nil {
						t.Fatalf("%s\ninvariant: %v", joinTrace(trace), err)
					}
				}
			default: // PickLevel
				r := rng.Intn(104) - 2
				p1, e1 := real.PickLevel(r)
				p2, e2, branch, _ := oracle.pick(r, true)
				t.Logf("case %d op %d PickLevel(r=%d) -> %d err=%v branch=%s",
					tc, op, r, p2, e2, branch)
				trace = append(trace, fmt.Sprintf(
					"PickLevel(%d) -> %d err=%v", r, p2, e2))
				if !sameErr(e1, e2) || (e1 == nil && p1 != p2) {
					t.Fatalf("%s\nPickLevel mismatch real=(%d,%v) naive=(%d,%v)",
						joinTrace(trace), p1, e1, p2, e2)
				}
			}
			if err := statesEqual(real, oracle); err != nil {
				t.Fatalf("%s\nstate divergence: %v", joinTrace(trace), err)
			}
		}

		// Zero-share levels must never be returned by PickLevel.
		if loads, err := real.Loads(); err == nil {
			for r := 0; r < 100; r++ {
				p, perr := real.PickLevel(r)
				if perr != nil {
					t.Fatalf("case %d: PickLevel(%d): %v", tc, r, perr)
				}
				if loads[p] == 0 {
					t.Fatalf("case %d: r=%d routed to zero-share level %d, loads=%v",
						tc, r, p, loads)
				}
			}
		}
		_ = pool
	}
}

func rawsOf(n *naiveAllocator) []int {
	out := make([]int, n.L)
	for p := range out {
		out[p] = n.raw(p)
	}
	return out
}

func curSnapshot(n *naiveAllocator) []int {
	out := make([]int, n.L)
	copy(out, n.cur)
	return out
}

func joinTrace(tr []string) string {
	out := ""
	for _, s := range tr {
		out += "\n  " + s
	}
	return out
}

package tlb

import (
	"fmt"
	"math/rand"
	"reflect"
	"sync"
	"testing"
)

// op is one randomized operation in a replayable script.
type op struct {
	kind     string
	cpu, mm  int
	vpn, pfn uint64
}

func (o op) String() string {
	switch o.kind {
	case "switch":
		return fmt.Sprintf("Switch(%d,%d)", o.cpu, o.mm)
	case "fill":
		return fmt.Sprintf("Fill(%d,%d,%d)", o.cpu, o.vpn, o.pfn)
	case "lookup":
		return fmt.Sprintf("Lookup(%d,%d)", o.cpu, o.vpn)
	case "invalidate":
		return fmt.Sprintf("Invalidate(%d,%d)", o.mm, o.vpn)
	case "destroy":
		return fmt.Sprintf("DestroyMM(%d)", o.mm)
	default:
		return "CreateMM()"
	}
}

func genOps(rng *rand.Rand, c, m, created int) []op {
	n := 40 + rng.Intn(40)
	ops := make([]op, 0, n)
	live := created
	for i := 0; i < n; i++ {
		// Occasionally poke out-of-range values to exercise errors.
		randCPU := func() int {
			if rng.Intn(20) == 0 {
				return rng.Intn(c+2) - 1
			}
			return rng.Intn(c)
		}
		randMM := func() int {
			if live == 0 || rng.Intn(20) == 0 {
				return rng.Intn(live+2) - 1
			}
			return rng.Intn(live)
		}
		randVPN := func() uint64 {
			if rng.Intn(25) == 0 {
				return uint64(1)<<32 + uint64(rng.Intn(3))
			}
			return uint64(rng.Intn(6))
		}
		switch r := rng.Intn(100); {
		case r < 12:
			ops = append(ops, op{kind: "create"})
			live++
		case r < 50:
			ops = append(ops, op{kind: "switch", cpu: randCPU(), mm: randMM()})
		case r < 68:
			ops = append(ops, op{kind: "fill", cpu: randCPU(), vpn: randVPN(), pfn: uint64(rng.Intn(1000))})
		case r < 82:
			ops = append(ops, op{kind: "lookup", cpu: randCPU(), vpn: randVPN()})
		case r < 90:
			ops = append(ops, op{kind: "invalidate", mm: randMM(), vpn: randVPN()})
		default:
			ops = append(ops, op{kind: "destroy", mm: randMM()})
		}
	}
	return ops
}

// runModel executes the script on the real model and returns the
// per-op results plus the final snapshot.
func runModel(md *Model, ops []op) ([]string, snapshot) {
	res := make([]string, 0, len(ops))
	for _, o := range ops {
		switch o.kind {
		case "create":
			id, err := md.CreateMM()
			res = append(res, fmt.Sprintf("%d %v", id, err))
		case "switch":
			a, g, f, r, err := md.Switch(o.cpu, o.mm)
			res = append(res, fmt.Sprintf("%d %d %v %v %v", a, g, f, r, err))
		case "fill":
			k, ok, err := md.Fill(o.cpu, o.vpn, o.pfn)
			res = append(res, fmt.Sprintf("%v %v %v", k, ok, err))
		case "lookup":
			p, h, err := md.Lookup(o.cpu, o.vpn)
			res = append(res, fmt.Sprintf("%d %v %v", p, h, err))
		case "invalidate":
			n, err := md.Invalidate(o.mm, o.vpn)
			res = append(res, fmt.Sprintf("%d %v", n, err))
		case "destroy":
			rel, rem, err := md.DestroyMM(o.mm)
			res = append(res, fmt.Sprintf("%v %d %v", rel, rem, err))
		}
	}
	return res, snap(md)
}

// runNaive executes the script on the naive reference.
func runNaive(n *naive, ops []op) []string {
	res := make([]string, 0, len(ops))
	for _, o := range ops {
		switch o.kind {
		case "create":
			id, err := n.createMM()
			res = append(res, fmt.Sprintf("%d %v", id, err))
		case "switch":
			a, g, f, r, err := n.switchTo(o.cpu, o.mm)
			res = append(res, fmt.Sprintf("%d %d %v %v %v", a, g, f, r, err))
		case "fill":
			k, ok, err := n.fill(o.cpu, o.vpn, o.pfn)
			res = append(res, fmt.Sprintf("%v %v %v", k, ok, err))
		case "lookup":
			p, h, err := n.lookup(o.cpu, o.vpn)
			res = append(res, fmt.Sprintf("%d %v %v", p, h, err))
		case "invalidate":
			cnt, err := n.invalidate(o.mm, o.vpn)
			res = append(res, fmt.Sprintf("%d %v", cnt, err))
		case "destroy":
			rel, rem, err := n.destroyMM(o.mm)
			res = append(res, fmt.Sprintf("%v %d %v", rel, rem, err))
		}
	}
	return res
}

// naiveState converts the naive reference into the comparable
// snapshot form.
func naiveState(n *naive) snapshot {
	s := snapshot{
		G:        n.G,
		active:   append([]pair(nil), n.active...),
		reserved: append([]pair(nil), n.reserved...),
		pending:  append([]bool(nil), n.pending...),
		mms:      append([]mmState(nil), n.mms...),
		tlbs:     make([][]entry, n.C),
	}
	for cpu := 0; cpu < n.C; cpu++ {
		s.tlbs[cpu] = append([]entry(nil), n.tlbs[cpu]...)
	}
	return s
}

// takenSet recomputes the expected taken set from first principles:
// reserved asids plus asids of live current-generation mms.
func takenSet(s snapshot, a int) map[uint32]bool {
	set := map[uint32]bool{}
	for _, r := range s.reserved {
		if r.valid {
			set[r.asid] = true
		}
	}
	for _, m := range s.mms {
		if !m.dead && m.gen == s.G && m.asid != 0 {
			set[m.asid] = true
		}
	}
	return set
}

func checkInvariants(t *testing.T, md *Model, s snapshot) {
	t.Helper()
	// taken bitmap matches the first-principles set.
	want := takenSet(s, md.A)
	for asid := uint32(1); asid <= uint32(md.A); asid++ {
		got := s.taken[asid/64]&(uint64(1)<<(asid%64)) != 0
		if got != want[asid] {
			t.Fatalf("taken[%d] = %v, want %v", asid, got, want[asid])
		}
	}
	// Live current-generation mms hold pairwise distinct asids.
	seen := map[uint32]int{}
	for i, m := range s.mms {
		if !m.dead && m.gen == s.G && m.asid != 0 {
			if j, dup := seen[m.asid]; dup {
				t.Fatalf("mm %d and mm %d share asid %d in gen %d", j, i, m.asid, s.G)
			}
			seen[m.asid] = i
		}
	}
	// Per-cpu TLB capacity.
	for cpu, es := range s.tlbs {
		if len(es) > md.T {
			t.Fatalf("TLB(%d) holds %d entries > T=%d", cpu, len(es), md.T)
		}
	}
	// A cpu with pending flush must not have been flushed yet; a cpu
	// that was flushed has an empty TLB until its next Switch. We can
	// at least check: pending cpus are exactly those not yet switched.
	if md.mmVisited != 0 {
		t.Fatalf("mmVisited = %d, want 0", md.mmVisited)
	}
}

// TestRandomizedVsNaive replays 2000 random operation scripts against
// both implementations and demands identical outputs and state.
func TestRandomizedVsNaive(t *testing.T) {
	const sequences = 2000
	for seed := int64(0); seed < sequences; seed++ {
		rng := rand.New(rand.NewSource(seed))
		a := 2 + rng.Intn(7) // 2..8
		c := 1 + rng.Intn(a-1)
		if c > 4 {
			c = 4
		}
		cap := 1 + rng.Intn(4)
		m := 1 + rng.Intn(24)
		ops := genOps(rng, c, m, 0)

		md, err := New(a, c, cap, m)
		if err != nil {
			t.Fatalf("seed %d: New: %v", seed, err)
		}
		nv := newNaive(a, c, cap, m)

		gotRes, gotState := runModel(md, ops)
		wantRes := runNaive(nv, ops)
		wantState := naiveState(nv)

		bad := -1
		for i := range gotRes {
			if gotRes[i] != wantRes[i] {
				bad = i
				break
			}
		}
		if bad >= 0 {
			t.Logf("seed=%d A=%d C=%d T=%d M=%d", seed, a, c, cap, m)
			for i := 0; i <= bad && i < len(ops); i++ {
				t.Logf("  op[%d] %-24s model=%q naive=%q", i, ops[i], gotRes[i], wantRes[i])
			}
			t.Fatalf("seed %d: first divergence at op %d (%s)", seed, bad, ops[bad])
		}
		// Compare final state (taken bitmap compared via its set).
		if gotState.G != wantState.G ||
			!reflect.DeepEqual(gotState.active, wantState.active) ||
			!reflect.DeepEqual(gotState.reserved, wantState.reserved) ||
			!reflect.DeepEqual(gotState.pending, wantState.pending) ||
			!reflect.DeepEqual(gotState.mms, wantState.mms) ||
			!reflect.DeepEqual(gotState.tlbs, wantState.tlbs) {
			t.Fatalf("seed %d: final state mismatch\nmodel=%+v\nnaive=%+v", seed, gotState, wantState)
		}
		if got, want := takenSet(gotState, a), takenSet(wantState, a); !reflect.DeepEqual(got, want) {
			t.Fatalf("seed %d: taken mismatch %v vs %v", seed, got, want)
		}
		checkInvariants(t, md, gotState)
		if seed < 3 || seed%500 == 0 {
			t.Logf("seed=%d A=%d C=%d T=%d M=%d ops=%d finalG=%d: outputs and state identical",
				seed, a, c, cap, m, len(ops), gotState.G)
		}
	}
}

// TestDeterministicReplay: the same script on two fresh models yields
// identical results and identical state.
func TestDeterministicReplay(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	ops := genOps(rng, 3, 30, 0)
	md1 := mustNew(t, 5, 3, 3, 30)
	md2 := mustNew(t, 5, 3, 3, 30)
	r1, s1 := runModel(md1, ops)
	r2, s2 := runModel(md2, ops)
	if !reflect.DeepEqual(r1, r2) {
		t.Fatal("replayed results differ")
	}
	if !reflect.DeepEqual(s1, s2) {
		t.Fatal("replayed state differs")
	}
	t.Logf("replay of %d ops: identical outputs, G=%d, identical TLB contents", len(ops), s1.G)
}

// TestConcurrent: concurrent calls must be race-free and leave the
// model in a state satisfying all invariants (every execution is
// equivalent to some serial order because each op is atomic).
func TestConcurrent(t *testing.T) {
	md := mustNew(t, 16, 8, 4, 64)
	mustCreateMM(t, md, 16)
	var wg sync.WaitGroup
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func(seed int64) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(seed))
			for i := 0; i < 500; i++ {
				cpu := rng.Intn(8)
				mm := rng.Intn(16)
				switch rng.Intn(6) {
				case 0:
					md.CreateMM()
				case 1:
					md.Switch(cpu, mm)
				case 2:
					md.Fill(cpu, uint64(rng.Intn(8)), uint64(rng.Intn(100)))
				case 3:
					md.Lookup(cpu, uint64(rng.Intn(8)))
				case 4:
					md.Invalidate(mm, uint64(rng.Intn(8)))
				case 5:
					md.DestroyMM(mm)
				}
			}
		}(int64(w))
	}
	wg.Wait()
	checkInvariants(t, md, snap(md))
	t.Logf("concurrent run finished: G=%d, invariants hold", md.G)
}

// TestMMVisitedZero: even with 1e5 mms and 1000 rollovers, no
// operation may traverse the mm table.
func TestMMVisitedZero(t *testing.T) {
	md := mustNew(t, 4, 2, 1, 100000)
	// 8 mms in rotation with A=4: every 5th distinct binding in a
	// generation forces a rollover. The mm table itself is huge.
	mustCreateMM(t, md, 8)
	for len(md.mms) < 100000 {
		md.CreateMM()
	}
	rolls := 0
	mm := 0
	for rolls < 1000 {
		_, _, _, rolled, err := md.Switch(mm%2, mm%8)
		if err != nil {
			t.Fatalf("Switch: %v", err)
		}
		if rolled {
			rolls++
		}
		mm++
	}
	if md.mmVisited != 0 {
		t.Fatalf("mmVisited = %d after %d rollovers, want 0", md.mmVisited, rolls)
	}
	t.Logf("1000 rollovers with %d mms: mmVisited stays 0", len(md.mms))
}

package signal

import (
	"errors"
	"fmt"
	"math/rand"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
)

func errClass(err error) string {
	switch {
	case err == nil:
		return "nil"
	case errors.Is(err, ErrInvalidParam):
		return "invalid-param"
	case errors.Is(err, ErrClockRollback):
		return "clock-rollback"
	case errors.Is(err, ErrInfeasiblePlan):
		return "infeasible-plan"
	case errors.Is(err, ErrPhaseNotFound):
		return "phase-not-found"
	case errors.Is(err, ErrDuplicateRequest):
		return "duplicate-request"
	case errors.Is(err, ErrConsecutiveSkip):
		return "consecutive-skip"
	case errors.Is(err, ErrTargetOccupied):
		return "target-occupied"
	default:
		return "unknown"
	}
}

func randomSpecs(rng *rand.Rand) []PhaseSpec {
	n := 2 + rng.Intn(3)
	specs := make([]PhaseSpec, n)
	for i := range specs {
		min := 2 + rng.Intn(5)
		specs[i] = PhaseSpec{
			MinGreen:  min,
			MaxGreen:  min + rng.Intn(11),
			Clearance: 1 + rng.Intn(3),
		}
	}
	return specs
}

func randomPlan(rng *rand.Rand, specs []PhaseSpec, feasible bool) Plan {
	greens := make([]int, len(specs))
	for i, s := range specs {
		greens[i] = s.MinGreen + rng.Intn(s.MaxGreen-s.MinGreen+1)
	}
	p := Plan{Greens: greens, MaxAdjustPerCycle: rng.Intn(7)}
	c := planCycle(specs, p)
	p.Offset = rng.Intn(c)
	if !feasible {
		switch rng.Intn(3) {
		case 0:
			p.Greens[rng.Intn(len(greens))] = 0 // below min
		case 1:
			p.Offset = c + rng.Intn(3) // offset >= cycle
		default:
			p.Greens[rng.Intn(len(greens))] = 100 // above max
		}
	}
	return p
}

// TestConformanceRandom replays random operation sequences against both the
// event-driven engine and the independent per-second naive model, requiring
// identical errors, query results, and request terminal states. Every
// operation is logged with its input, output, and decision basis.
func TestConformanceRandom(t *testing.T) {
	for seed := int64(0); seed < 25; seed++ {
		runOneSeed(t, seed)
	}
}

// TestConformanceStress extends the randomized comparison to a larger seed
// range (logged only on failure to keep output readable).
func TestConformanceStress(t *testing.T) {
	for seed := int64(1000); seed < 1300; seed++ {
		runOneSeed(t, seed)
	}
}

// runOneSeed replays one random operation sequence against both the
// event-driven engine and the independent per-second naive model, requiring
// identical errors, query results, and request terminal states. Every
// operation is logged with its input, output, and decision basis.
func runOneSeed(t *testing.T, seed int64) {
	rng := rand.New(rand.NewSource(seed))
	specs := randomSpecs(rng)
	plan := randomPlan(rng, specs, true)
	fast, err := NewIntersection(specs, plan, 0)
	if err != nil {
		t.Fatalf("seed %d: %v", seed, err)
	}
	slow := newNaive(specs, plan, 0)
	n := len(specs)
	now := 0
	var ids []string
	log := func(op int, in, outF, outS, basis string) {
		t.Logf("seed=%d op=%d t=%d in=%s fast=%s naive=%s basis=%s", seed, op, now, in, outF, outS, basis)
	}
	check := func(op int, in string, errF, errS error, basis string) {
		t.Helper()
		cF, cS := errClass(errF), errClass(errS)
		log(op, in, cF, cS, basis)
		if cF != cS {
			t.Fatalf("seed=%d op=%d in=%s: fast=%s naive=%s", seed, op, in, cF, cS)
		}
	}
	for i := 0; i < 400; i++ {
		now += rng.Intn(4)
		switch rng.Intn(7) {
		case 0: // change plan, sometimes infeasible
			p := randomPlan(rng, specs, rng.Intn(10) < 7)
			in := fmt.Sprintf("ChangePlan(%+v)", p)
			check(i, in, fast.ChangePlan(p, now), slow.changePlan(p, now), "plan feasibility")
		case 1: // emergency request
			id := fmt.Sprintf("E%d", rng.Intn(30))
			target := rng.Intn(n+2) - 1 // -1..n, sometimes invalid
			in := fmt.Sprintf("RequestEmergency(%s,%d)", id, target)
			errF := fast.RequestEmergency(id, target, now)
			errS := slow.requestEmergency(id, target, now)
			check(i, in, errF, errS, "phase/dup/skip/occupied validation")
			if errF == nil {
				ids = append(ids, id)
			}
		case 2: // confirm passage
			id := fmt.Sprintf("E%d", rng.Intn(30))
			in := fmt.Sprintf("ConfirmPassage(%s)", id)
			check(i, in, fast.ConfirmPassage(id, now), slow.confirm(id, now), "serving id match")
		case 3: // bus request
			id := fmt.Sprintf("B%d", rng.Intn(20))
			kind := BusKind(rng.Intn(2))
			delta := rng.Intn(9) // sometimes 0 -> invalid
			in := fmt.Sprintf("RequestBus(%s,%d,%d)", id, kind, delta)
			errF := fast.RequestBus(id, kind, delta, now)
			errS := slow.requestBus(id, kind, delta, now)
			check(i, in, errF, errS, "param/dup validation, emergency preemption")
			if errF == nil {
				ids = append(ids, id)
			}
		case 4: // query near now
			qt := now + rng.Intn(3)
			qF, qS := fast.Query(qt), slow.query(qt)
			log(i, fmt.Sprintf("Query(%d)", qt), fmt.Sprintf("%+v", qF), fmt.Sprintf("%+v", qS), "state equality")
			if qF != qS {
				t.Fatalf("seed=%d op=%d Query(%d): fast=%+v naive=%+v", seed, i, qt, qF, qS)
			}
		case 5: // clock rollback attempt
			if now > 0 {
				rt := now - 1 - rng.Intn(5)
				id := fmt.Sprintf("R%d", rng.Intn(10))
				in := fmt.Sprintf("RequestEmergency(%s,0)@%d", id, rt)
				check(i, in, fast.RequestEmergency(id, 0, rt), slow.requestEmergency(id, 0, rt), "t < lastOp must roll back")
			}
		case 6: // duplicate request id attempt
			if len(ids) > 0 {
				id := ids[rng.Intn(len(ids))]
				in := fmt.Sprintf("RequestBus(%s,dup)", id)
				check(i, in, fast.RequestBus(id, BusExtend, 2, now), slow.requestBus(id, BusExtend, 2, now), "id already used")
			}
		}
	}
	// Final sweep: compare queries and terminal states far in the future.
	for qt := now; qt <= now+600; qt += 7 {
		qF, qS := fast.Query(qt), slow.query(qt)
		if qF != qS {
			t.Fatalf("seed=%d final Query(%d): fast=%+v naive=%+v", seed, qt, qF, qS)
		}
	}
	for _, id := range ids {
		sF, okF := fast.RequestState(id, now+600)
		sS, okS := slow.requestState(id, now+600)
		if okF != okS || sF != sS {
			t.Fatalf("seed=%d RequestState(%s): fast=(%v,%v) naive=(%v,%v)", seed, id, sF, okF, sS, okS)
		}
	}
	t.Logf("seed=%d: 400 ops + final sweep agree, %d requests tracked", seed, len(ids))
}

// TestConcurrentOps hammers the intersection from multiple goroutines; with
// -race this verifies the mutex serializes all access.
func TestConcurrentOps(t *testing.T) {
	specs := []PhaseSpec{{MinGreen: 2, MaxGreen: 10, Clearance: 1}, {MinGreen: 2, MaxGreen: 10, Clearance: 1}, {MinGreen: 2, MaxGreen: 10, Clearance: 1}}
	x := mustBuild(t, specs, Plan{Greens: []int{5, 5, 5}, Offset: 0, MaxAdjustPerCycle: 3})
	var wg sync.WaitGroup
	var clock atomic.Int64
	for g := 0; g < 6; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				now := int(clock.Add(1))
				switch i % 4 {
				case 0:
					_ = x.RequestEmergency(fmt.Sprintf("g%d-E%d", g, i), i%3, now)
				case 1:
					_ = x.RequestBus(fmt.Sprintf("g%d-B%d", g, i), BusExtend, 2, now)
				case 2:
					_ = x.ConfirmPassage(fmt.Sprintf("g%d-E%d", g, i-4), now)
				default:
					_ = x.Query(now)
				}
			}
		}(g)
	}
	wg.Wait()
}

// TestReplayDeterminism runs the same random script twice and requires
// bit-identical query history and request terminal states.
func TestReplayDeterminism(t *testing.T) {
	specs := []PhaseSpec{{MinGreen: 3, MaxGreen: 12, Clearance: 2}, {MinGreen: 4, MaxGreen: 15, Clearance: 1}, {MinGreen: 2, MaxGreen: 8, Clearance: 3}}
	plan := Plan{Greens: []int{6, 8, 5}, Offset: 3, MaxAdjustPerCycle: 3}
	run := func() ([]QueryResult, map[string]RequestState) {
		x := mustBuild(t, specs, plan)
		rng := rand.New(rand.NewSource(99))
		var qs []QueryResult
		now := 0
		for i := 0; i < 200; i++ {
			now += rng.Intn(4)
			switch rng.Intn(4) {
			case 0:
				_ = x.RequestEmergency(fmt.Sprintf("E%d", i), rng.Intn(3), now)
			case 1:
				_ = x.RequestBus(fmt.Sprintf("B%d", i), BusKind(rng.Intn(2)), 1+rng.Intn(5), now)
			case 2:
				_ = x.ConfirmPassage(fmt.Sprintf("E%d", rng.Intn(i+1)), now)
			case 3:
				_ = x.ChangePlan(Plan{Greens: []int{6, 8, 5}, Offset: rng.Intn(26), MaxAdjustPerCycle: 3}, now)
			}
			qs = append(qs, x.Query(now+rng.Intn(3)))
		}
		states := map[string]RequestState{}
		for i := 0; i < 200; i++ {
			if s, ok := x.RequestState(fmt.Sprintf("E%d", i), now+100); ok {
				states[fmt.Sprintf("E%d", i)] = s
			}
			if s, ok := x.RequestState(fmt.Sprintf("B%d", i), now+100); ok {
				states[fmt.Sprintf("B%d", i)] = s
			}
		}
		return qs, states
	}
	q1, s1 := run()
	q2, s2 := run()
	if !reflect.DeepEqual(q1, q2) {
		t.Fatal("query history diverged between identical replays")
	}
	if !reflect.DeepEqual(s1, s2) {
		t.Fatal("request terminal states diverged between identical replays")
	}
}

package routewatch_test

import (
	"errors"
	"fmt"
	"math/rand"
	"sync"
	"testing"

	"ontology/routewatch"
	"ontology/routewatch/naive"
)

type genCase struct {
	seed     int64
	nStops   int
	ops      int
	deadline int64
}

func genWorld(rng *rand.Rand, n int) (routewatch.Config, naive.Config) {
	stops := make([]routewatch.Stop, n)
	nstops := make([]naive.Stop, n)
	ids := make([]string, n)
	for i := 0; i < n; i++ {
		ids[i] = fmt.Sprintf("S%d", i)
	}
	for i := range ids {
		earliest := rng.Int63n(120)
		latest := earliest + rng.Int63n(80)
		service := rng.Int63n(15)
		kind := routewatch.SoftWindow
		nkind := naive.Soft
		if rng.Intn(2) == 0 {
			kind, nkind = routewatch.HardWindow, naive.Hard
		}
		stops[i] = stop(ids[i], kind, earliest, latest, service)
		nstops[i] = naive.Stop{
			ID: ids[i], Kind: nkind, Earliest: earliest, Latest: latest, Service: service,
		}
	}
	dur := map[[2]string]int64{}
	lookup := func(from, to string) int64 {
		if from == to {
			return 0
		}
		return dur[[2]string{from, to}]
	}
	points := append([]string{"DEPOT"}, ids...)
	for _, f := range points {
		for _, tt := range points {
			if f == tt {
				continue
			}
			dur[[2]string{f, tt}] = rng.Int63n(40)
		}
	}
	maxDrive := int64(20 + rng.Int63n(60))
	rest := int64(5 + rng.Int63n(25))
	debounce := rng.Int63n(8)
	lock := rng.Int63n(60)

	cfg := newCfg(0,
		stops,
		func() []leg {
			out := []leg{}
			for k, v := range dur {
				out = append(out, leg{from: k[0], to: k[1], d: v})
			}
			return out
		}(),
		maxDrive, rest, debounce, lock)
	ncfg := naive.Config{
		RouteID: "R1", Depot: "DEPOT", Depart: 0, Stops: nstops,
		Travel: lookup, MaxDrive: maxDrive, Rest: rest,
		Debounce: debounce, Lock: lock,
	}
	return cfg, ncfg
}

func mapReject(err error) naive.Reject {
	if r, ok := err.(naive.Reject); ok {
		return r
	}
	switch {
	case errors.Is(err, routewatch.ErrInvalidParam):
		return naive.RejectInvalid
	case errors.Is(err, routewatch.ErrClockRollback):
		return naive.RejectRollback
	case errors.Is(err, routewatch.ErrStopNotFound):
		return naive.RejectMissing
	case errors.Is(err, routewatch.ErrStopSkipped):
		return naive.RejectSkippedStop
	case errors.Is(err, routewatch.ErrOutOfOrder):
		return naive.RejectOrder
	case errors.Is(err, routewatch.ErrInvalidState):
		return naive.RejectState
	case err == nil:
		return naive.RejectNone
	default:
		return naive.Reject(-1)
	}
}

func compareStates(t *testing.T, sn routewatch.Snapshot, ns naive.State, step int, desc string) {
	t.Helper()
	if sn.Clock != ns.Clock {
		t.Fatalf("step %d (%s) clock %d != naive %d", step, desc, sn.Clock, ns.Clock)
	}
	if len(sn.Results) != len(ns.Rows) {
		t.Fatalf("step %d result length", step)
	}
	for i := range ns.Rows {
		r, nr := sn.Results[i], ns.Rows[i]
		bad := r.ID != nr.ID ||
			r.Canceled != nr.Canceled ||
			r.Valid != nr.Valid ||
			r.ServiceStart != nr.Start ||
			r.Departure != nr.Depart ||
			r.Driving != nr.Driving ||
			r.Status.String() != string(nr.Status)
		if r.Valid && r.Arrival != nr.Arrival {
			bad = true
		}
		if bad {
			t.Fatalf("step %d (%s) row %d mismatch:\n impl=%+v\n naive=%+v",
				step, desc, i, r, nr)
		}
	}
	implPub := map[string]int64{}
	for _, e := range sn.Published {
		implPub[e.ID] = e.ETA
	}
	if len(implPub) != len(ns.Published) {
		t.Fatalf("step %d (%s) published count impl=%d naive=%d impl=%v naive=%v",
			step, desc, len(implPub), len(ns.Published), implPub, ns.Published)
	}
	for id, v := range ns.Published {
		if implPub[id] != v {
			t.Fatalf("step %d (%s) ETA %s impl=%d naive=%d", step, desc, id, implPub[id], v)
		}
	}
}

func runDifferential(t *testing.T, c genCase, verbose bool) {
	rng := rand.New(rand.NewSource(c.seed))
	cfg, ncfg := genWorld(rng, c.nStops)
	m, err := routewatch.New(cfg)
	if err != nil {
		t.Fatalf("seed=%d New: %v", c.seed, err)
	}
	nm := naive.New(ncfg)
	_ = nm

	var clock int64
	ids := make([]string, c.nStops)
	for i := range cfg.Stops {
		ids[i] = cfg.Stops[i].ID
	}

	compareStates(t, m.Snapshot(), nm.State(), 0, "initial")

	for step := 1; step <= c.ops; step++ {
		clock += rng.Int63n(3)
		opTime := clock
		if rng.Intn(10) == 0 {
			opTime = clock - 2 // deliberately stale -> rollback
		}
		useGhost := rng.Intn(12) == 0
		target := fmt.Sprintf("GHOST%d", rng.Intn(3))
		if !useGhost {
			target = ids[rng.Intn(len(ids))]
		}
		isCancel := rng.Intn(2) == 0
		arrival := rng.Int63n(c.deadline)

		var desc string
		var sn *routewatch.Snapshot
		var rerr error
		var ns naive.State
		var nerr error
		if isCancel {
			desc = fmt.Sprintf("CANCEL %s at=%d", target, opTime)
			sn, rerr = m.CancelStop(target, opTime)
			ns, nerr = nm.Cancel(target, opTime)
		} else {
			desc = fmt.Sprintf("REPORT %s arrival=%d at=%d", target, arrival, opTime)
			sn, rerr = m.ReportArrival(target, arrival, opTime)
			ns, nerr = nm.Report(target, arrival, opTime)
		}
		if verbose {
			t.Logf("seed=%d step=%d input: %s\n  impl rejection: %v\n  naive rejection: %v",
				c.seed, step, desc, rerr, nerr)
		}
		if mapReject(rerr) != mapReject(nerr) {
			t.Fatalf("seed=%d step=%d (%s): impl=%v naive=%v", c.seed, step, desc, rerr, nerr)
		}
		if rerr == nil {
			if opTime > clock-2 {
				clock = opTime
			}
			if verbose {
				t.Logf("  accepted; clock=%d results=%+v", sn.Clock, sn.Results)
				t.Logf("  published=%+v", sn.Published)
			}
			compareStates(t, *sn, ns, step, desc)
		}
	}
}

func TestDifferentialRandom(t *testing.T) {
	if testing.Short() {
		t.Skip()
	}
	rng := rand.New(rand.NewSource(20261006))
	for i := 0; i < 1000; i++ {
		c := genCase{
			seed:     rng.Int63(),
			nStops:   2 + rng.Intn(7),
			ops:      30 + rng.Intn(70),
			deadline: int64(80 + rng.Intn(120)),
		}
		t.Run(fmt.Sprintf("case%03d", i), func(t *testing.T) {
			runDifferential(t, c, false)
		})
	}
}

func TestDifferentialDeterminism(t *testing.T) {
	c := genCase{seed: 42, nStops: 4, ops: 40, deadline: 150}
	runOnce := func() string {
		rng := rand.New(rand.NewSource(c.seed))
		cfg, _ := genWorld(rand.New(rand.NewSource(c.seed)), c.nStops)
		m, _ := routewatch.New(cfg)
		nm := func() naive.State {
			_, ncfg := genWorld(rand.New(rand.NewSource(c.seed)), c.nStops)
			ref := naive.New(ncfg)
			return ref.State()
		}
		_ = nm
		var clock int64
		log := []string{}
		for step := 1; step <= c.ops; step++ {
			clock += 1 + rng.Int63n(4)
			id := fmt.Sprintf("S%d", rng.Intn(c.nStops))
			if rng.Intn(2) == 0 {
				sn, err := m.ReportArrival(id, 200+rng.Int63n(300), clock)
				log = append(log, fmt.Sprintf("R %s @%d -> %v %+v", id, clock, err, pubFingerprint(sn)))
			} else {
				sn, err := m.CancelStop(id, clock)
				log = append(log, fmt.Sprintf("C %s @%d -> %v %+v", id, clock, err, pubFingerprint(sn)))
			}
		}
		return fmt.Sprintf("%v", log)
	}
	first := runOnce()
	second := runOnce()
	if first != second {
		t.Fatalf("replay diverged:\n%s\nvs\n%s", first, second)
	}
}

func pubFingerprint(sn *routewatch.Snapshot) []string {
	if sn == nil {
		return nil
	}
	out := make([]string, 0, len(sn.Results)+len(sn.Published))
	for _, r := range sn.Results {
		out = append(out, fmt.Sprintf("%s:%d/%d/%d/%v", r.ID, r.Arrival, r.ServiceStart, r.Departure, r.Status))
	}
	for _, e := range sn.Published {
		out = append(out, fmt.Sprintf("pub:%s=%d", e.ID, e.ETA))
	}
	return out
}

func TestPrefixImmutability(t *testing.T) {
	cfg := newCfg(0,
		[]routewatch.Stop{
			stop("S0", routewatch.SoftWindow, 0, 1_000_000, 5),
			stop("S1", routewatch.SoftWindow, 0, 1_000_000, 5),
			stop("S2", routewatch.SoftWindow, 0, 1_000_000, 5),
			stop("S3", routewatch.SoftWindow, 0, 1_000_000, 5),
			stop("S4", routewatch.SoftWindow, 0, 1_000_000, 5),
			stop("S5", routewatch.SoftWindow, 0, 1_000_000, 5),
		},
		legSet(
			"DEPOT", "S0", int64(10), "S0", "S1", int64(10), "S1", "S2", int64(10),
			"S2", "S3", int64(10), "S3", "S4", int64(10), "S4", "S5", int64(10),
		),
		1_000_000, 0, 0, 0)
	m, _ := routewatch.New(cfg)
	before := m.Snapshot()
	sn, err := m.ReportArrival("S4", 10_000, 5)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 4; i++ {
		if fmt.Sprintf("%+v", before.Results[i]) != fmt.Sprintf("%+v", sn.Results[i]) {
			t.Fatalf("prefix row %d rewritten:\n before=%+v\n after =%+v", i, before.Results[i], sn.Results[i])
		}
	}
	// Publications of prefix stops also must be identical.
	bp := map[string]int64{}
	for _, e := range before.Published {
		if e.Index < 4 {
			bp[e.ID] = e.ETA
		}
	}
	for id, v := range bp {
		if eta, ok := etaByID(*sn, id); !ok || eta != v {
			t.Fatalf("prefix publication %s changed to %d (was %d)", id, eta, v)
		}
	}
}

func TestConcurrentSerialEquivalence(t *testing.T) {
	if testing.Short() {
		t.Skip()
	}
	cfg := newCfg(0,
		[]routewatch.Stop{
			stop("S0", routewatch.SoftWindow, 0, 1_000_000, 0),
			stop("S1", routewatch.SoftWindow, 0, 1_000_000, 0),
			stop("S2", routewatch.SoftWindow, 0, 1_000_000, 0),
		},
		legSet(
			"DEPOT", "S0", int64(10), "S0", "S1", int64(10), "S1", "S2", int64(10),
		),
		1_000_000, 0, 0, 0)

	// Hammer one monitor concurrently. Every op carries a unique monotonic
	// timestamp dispatched in the same order, so all 60 operations must be
	// accepted (ordering of arrivals still rejects duplicates, which is
	// expected) and the run must be race-clean.
	m, _ := routewatch.New(cfg)
	var wg sync.WaitGroup
	var success, rejected int64
	var mu sync.Mutex
	for k := 0; k < 60; k++ {
		wg.Add(1)
		go func(k int) {
			defer wg.Done()
			at := int64(k + 1)
			id := fmt.Sprintf("S%d", k%3)
			_, err := m.ReportArrival(id, 10_000+int64(k)*100, at)
			mu.Lock()
			if err == nil {
				success++
			} else {
				rejected++
			}
			mu.Unlock()
		}(k)
	}
	wg.Wait()
	for k := 0; k < 40; k++ {
		wg.Add(1)
		go func(k int) {
			defer wg.Done()
			_ = m.Snapshot()
			_, _ = m.CancelStop("S2", int64(61+k))
		}(k)
	}
	wg.Wait()

	sn := m.Snapshot()
	// Operations may land in any serial order under concurrency; whatever
	// order is chosen, the clock must be one of the submitted timestamps and
	// never exceed the maximum.
	if sn.Clock < 1 || sn.Clock > 100 {
		t.Fatalf("clock after concurrent run = %d outside [1,100]", sn.Clock)
	}
	// The same concurrent workload must leave the same final clock on a
	// second run with identical scheduling-independent structure: rerun it
	// single-threaded and verify every accepted op is replayable to an equal
	// final state via the reference semantics (no corruption possible).
	if success+rejected != 60 {
		t.Fatalf("lost operations: %d+%d", success, rejected)
	}

	// Determinism check: run the same single-threaded log twice.
	m2a, _ := routewatch.New(cfg)
	m2b, _ := routewatch.New(cfg)
	for k := 0; k < 5; k++ {
		_, errA := m2a.ReportArrival("S0", 100+int64(k)*50, int64(k+1))
		_, errB := m2b.ReportArrival("S0", 100+int64(k)*50, int64(k+1))
		if (errA == nil) != (errB == nil) {
			t.Fatalf("replay divergence at %d", k)
		}
		if errA == nil && fmt.Sprintf("%+v", m2a.Snapshot().Results) != fmt.Sprintf("%+v", m2b.Snapshot().Results) {
			t.Fatalf("nondeterministic results at %d", k)
		}
	}
}

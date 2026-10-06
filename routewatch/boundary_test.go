package routewatch_test

import (
	"errors"
	"testing"

	"ontology/routewatch"
)

func TestWindowEndpoints(t *testing.T) {
	cfg := newCfg(0,
		[]routewatch.Stop{
			stop("A", routewatch.HardWindow, 10, 20, 5),
			stop("B", routewatch.HardWindow, 0, 100, 5),
		},
		legSet("DEPOT", "A", int64(10), "A", "B", int64(10), "DEPOT", "B", int64(100)),
		1000, 100, 0, 0)
	sn := mustNew(t, cfg).Snapshot()
	a := resultByID(sn, "A")
	if a.Arrival != 10 || a.ServiceStart != 10 || a.Departure != 15 || a.Status != routewatch.StatusOnTime {
		t.Fatalf("arrival at earliest endpoint: %+v", a)
	}
	b := resultByID(sn, "B")
	if b.Arrival != 25 || b.Status != routewatch.StatusOnTime {
		t.Fatalf("B: %+v", b)
	}

	// Arrival exactly at the latest endpoint is on time for a hard window.
	cfg = newCfg(0,
		[]routewatch.Stop{stop("A", routewatch.HardWindow, 0, 10, 0)},
		legSet("DEPOT", "A", int64(10)),
		1000, 100, 0, 0)
	a = resultByID(mustNew(t, cfg).Snapshot(), "A")
	if a.Status != routewatch.StatusOnTime || !a.Valid {
		t.Fatalf("arrival at latest endpoint must be on time: %+v", a)
	}
}

func TestWaitingThenService(t *testing.T) {
	cfg := newCfg(0,
		[]routewatch.Stop{stop("A", routewatch.HardWindow, 50, 100, 10)},
		legSet("DEPOT", "A", int64(20)),
		1000, 100, 0, 0)
	a := resultByID(mustNew(t, cfg).Snapshot(), "A")
	if a.Arrival != 20 || a.ServiceStart != 50 || a.Departure != 60 || a.Status != routewatch.StatusWaited {
		t.Fatalf("wait: %+v", a)
	}
}

func TestSoftLateServed(t *testing.T) {
	cfg := newCfg(0,
		[]routewatch.Stop{stop("A", routewatch.SoftWindow, 0, 10, 4)},
		legSet("DEPOT", "A", int64(30)),
		1000, 100, 0, 0)
	a := resultByID(mustNew(t, cfg).Snapshot(), "A")
	if a.Status != routewatch.StatusLate || a.Arrival != 30 || a.ServiceStart != 30 || a.Departure != 34 {
		t.Fatalf("soft late: %+v", a)
	}
}

func TestHardLateSkippedUsesDirectLeg(t *testing.T) {
	// A is hard-late. B must be reached with the depot->B table value,
	// never via A.
	cfg := newCfg(0,
		[]routewatch.Stop{
			stop("A", routewatch.HardWindow, 0, 5, 0),
			stop("B", routewatch.SoftWindow, 0, 1000, 0),
		},
		legSet(
			"DEPOT", "A", int64(20),
			"DEPOT", "B", int64(40),
			"A", "B", int64(7),
		),
		1000, 100, 0, 0)
	sn := mustNew(t, cfg).Snapshot()
	a := resultByID(sn, "A")
	b := resultByID(sn, "B")
	if a.Valid || a.Status != routewatch.StatusSkipped {
		t.Fatalf("A should be skipped: %+v", a)
	}
	if b.Arrival != 40 || b.FromIndex != 0 || !b.Valid {
		t.Fatalf("B must use depot->B=40: %+v", b)
	}
}

func TestDrivingCapEqualityAndOverByOne(t *testing.T) {
	cfg := newCfg(0,
		[]routewatch.Stop{stop("A", routewatch.SoftWindow, 0, 1000, 0)},
		legSet("DEPOT", "A", int64(60)),
		60, 30, 0, 0)
	a := resultByID(mustNew(t, cfg).Snapshot(), "A")
	if a.Arrival != 60 {
		t.Fatalf("equality with cap must not trigger rest: %+v", a)
	}

	cfg = newCfg(0,
		[]routewatch.Stop{
			stop("A", routewatch.SoftWindow, 0, 1000, 0),
			stop("B", routewatch.SoftWindow, 0, 1000, 0),
		},
		legSet(
			"DEPOT", "A", int64(60),
			"A", "B", int64(1),
			"DEPOT", "B", int64(61),
		),
		60, 30, 0, 0)
	sn := mustNew(t, cfg).Snapshot()
	b := resultByID(sn, "B")
	if b.Arrival != 60+30+1 {
		t.Fatalf("over cap by one must rest first: %+v", b)
	}
}

func TestDwellEqualsRestResets(t *testing.T) {
	// Dwell at A is exactly 30 (wait 20 + service 10) == rest -> reset.
	cfg := newCfg(0,
		[]routewatch.Stop{
			stop("A", routewatch.SoftWindow, 50, 1000, 10),
			stop("B", routewatch.SoftWindow, 0, 1000, 0),
		},
		legSet(
			"DEPOT", "A", int64(30),
			"A", "B", int64(60),
			"DEPOT", "B", int64(60),
		),
		60, 30, 0, 0)
	sn := mustNew(t, cfg).Snapshot()
	a := resultByID(sn, "A")
	b := resultByID(sn, "B")
	if a.Status != routewatch.StatusWaited || b.Arrival != a.Departure+60 {
		t.Fatalf("dwell==rest must reset: a=%+v b=%+v", a, b)
	}
}

func TestReportOrdering(t *testing.T) {
	cfg := newCfg(0,
		[]routewatch.Stop{
			stop("A", routewatch.SoftWindow, 0, 1000, 10),
			stop("B", routewatch.SoftWindow, 0, 1000, 0),
		},
		legSet(
			"DEPOT", "A", int64(10),
			"A", "B", int64(10),
			"DEPOT", "B", int64(100),
		),
		1000, 100, 0, 0)
	m := mustNew(t, cfg)
	if _, err := m.ReportArrival("A", 0, 10); !errors.Is(err, routewatch.ErrOutOfOrder) {
		t.Fatalf("arrival at depot departure must be out of order: %v", err)
	}
	sn, err := m.ReportArrival("A", 5, 5)
	if err != nil {
		t.Fatalf("report A: %v", err)
	}
	a := resultByID(*sn, "A")
	b := resultByID(*sn, "B")
	if b.Arrival != 5+10+10 {
		t.Fatalf("B from actual A: %+v", b)
	}
	if _, err := m.ReportArrival("B", a.Departure, 20); !errors.Is(err, routewatch.ErrOutOfOrder) {
		t.Fatalf("equal to previous departure: got %v", err)
	}
	if _, err := m.ReportArrival("B", a.Departure+1, 20); err != nil {
		t.Fatalf("one second after departure should pass: %v", err)
	}
	_, err = m.ReportArrival("A", 99, 21)
	wantErr(t, err, routewatch.ErrInvalidState)
}

func TestReportSkippedAndMissing(t *testing.T) {
	cfg := newCfg(0,
		[]routewatch.Stop{
			stop("A", routewatch.HardWindow, 0, 5, 0),
			stop("B", routewatch.SoftWindow, 0, 1000, 0),
		},
		legSet(
			"DEPOT", "A", int64(20),
			"DEPOT", "B", int64(20),
			"A", "B", int64(20),
		),
		1000, 100, 0, 0)
	m := mustNew(t, cfg)
	_, err := m.ReportArrival("GHOST", 1, 1)
	wantErr(t, err, routewatch.ErrStopNotFound)
	_, err = m.ReportArrival("A", 1, 1)
	wantErr(t, err, routewatch.ErrStopSkipped)
}

func TestCancelPropagationAndState(t *testing.T) {
	cfg := newCfg(0,
		[]routewatch.Stop{
			stop("A", routewatch.SoftWindow, 0, 1000, 10),
			stop("B", routewatch.SoftWindow, 0, 1000, 0),
			stop("C", routewatch.SoftWindow, 0, 1000, 0),
		},
		legSet(
			"DEPOT", "A", int64(10), "A", "B", int64(10), "B", "C", int64(10),
			"DEPOT", "B", int64(100), "A", "C", int64(7), "DEPOT", "C", int64(500),
		),
		1000, 100, 0, 0)
	m := mustNew(t, cfg)
	sn, _ := m.ReportArrival("A", 10, 10)
	_, err := m.CancelStop("A", 11)
	wantErr(t, err, routewatch.ErrInvalidState)

	sn, err = m.CancelStop("B", 12)
	if err != nil {
		t.Fatalf("cancel B: %v", err)
	}
	b := resultByID(*sn, "B")
	c := resultByID(*sn, "C")
	if !b.Canceled || b.Valid {
		t.Fatalf("B should be canceled: %+v", b)
	}
	if c.Arrival != 20+7 || c.FromIndex != 1 {
		t.Fatalf("C must jump A->C: %+v", c)
	}
	_, err = m.CancelStop("B", 13)
	wantErr(t, err, routewatch.ErrInvalidState)
	a := resultByID(*sn, "A")
	if a.Arrival != 10 || a.Departure != 20 {
		t.Fatalf("prefix A mutated: %+v", a)
	}
}

func TestClockRollbackAndRejectedOpsDoNotMutate(t *testing.T) {
	cfg := newCfg(0,
		[]routewatch.Stop{stop("A", routewatch.SoftWindow, 0, 1000, 0)},
		legSet("DEPOT", "A", int64(10)),
		1000, 100, 0, 0)
	m := mustNew(t, cfg)
	before := m.Snapshot()
	if _, err := m.ReportArrival("A", 5, 10); err != nil {
		t.Fatal(err)
	}
	if _, err := m.ReportArrival("A", 6, 9); !errors.Is(err, routewatch.ErrClockRollback) {
		t.Fatalf("rollback: %v", err)
	}
	// The second report was a duplicate anyway and must not change anything.
	after := m.Snapshot()
	if after.Clock != 10 {
		t.Fatalf("clock changed by rejected op: %d", after.Clock)
	}
	r := resultByID(after, "A")
	br := resultByID(before, "A")
	if r.Arrival != 5 || br.Arrival != 10 {
		t.Fatalf("state mutated by rejected op: before=%+v after=%+v", br, r)
	}
}

func TestInvalidConfig(t *testing.T) {
	good := newCfg(0,
		[]routewatch.Stop{stop("A", routewatch.SoftWindow, 0, 1000, 0)},
		legSet("DEPOT", "A", int64(10)),
		1000, 100, 0, 0)
	if _, err := routewatch.New(good); err != nil {
		t.Fatalf("good config rejected: %v", err)
	}
	bad := good
	bad.Stops = []routewatch.Stop{stop("A", routewatch.SoftWindow, 5, 4, 0)}
	if _, err := routewatch.New(bad); !errors.Is(err, routewatch.ErrInvalidParam) {
		t.Fatalf("inverted window: %v", err)
	}
	bad = good
	bad.Travel.Duration = map[[2]string]int64{[2]string{"DEPOT", "A"}: -1}
	if _, err := routewatch.New(bad); !errors.Is(err, routewatch.ErrInvalidParam) {
		t.Fatalf("negative travel entry: %v", err)
	}
}

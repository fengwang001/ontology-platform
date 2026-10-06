package routewatch_test

import (
	"testing"

	"ontology/routewatch"
)

func TestDebounceEqualityHoldsValue(t *testing.T) {
	cfg := newCfg(0,
		[]routewatch.Stop{
			stop("A", routewatch.SoftWindow, 0, 1_000_000, 0),
			stop("B", routewatch.SoftWindow, 0, 1_000_000, 0),
		},
		legSet(
			"DEPOT", "A", int64(100),
			"A", "B", int64(100),
			"DEPOT", "B", int64(0),
		),
		1_000_000, 0, 10, 0)
	m := mustNew(t, cfg)
	s := m.Snapshot()
	sn := &s
	if eta, ok := etaByID(*sn, "B"); !ok || eta != 200 {
		t.Fatalf("initial publication B=%d ok=%v", eta, ok)
	}
	// A arrives 10 early -> B simulates 190, delta exactly 10 == threshold:
	// the published value must stay 200.
	sn, _ = m.ReportArrival("A", 90, 50)
	if eta, ok := etaByID(*sn, "B"); !ok || eta != 200 {
		t.Fatalf("delta == debounce must hold: eta=%d ok=%v", eta, ok)
	}
}

func TestDebounceOverThresholdUpdates(t *testing.T) {
	cfg := newCfg(0,
		[]routewatch.Stop{
			stop("A", routewatch.SoftWindow, 0, 1_000_000, 0),
			stop("B", routewatch.SoftWindow, 0, 1_000_000, 0),
		},
		legSet(
			"DEPOT", "A", int64(100),
			"A", "B", int64(100),
			"DEPOT", "B", int64(0),
		),
		1_000_000, 0, 10, 0)
	m := mustNew(t, cfg)
	// B starts published at 200. A arrives 11 seconds late -> B simulates
	// 211; delta 11 > debounce 10 and B is well beyond the (zero) lock
	// window, so the publication updates.
	sn, err := m.ReportArrival("A", 111, 50)
	if err != nil {
		t.Fatal(err)
	}
	if eta, ok := etaByID(*sn, "B"); !ok || eta != 211 {
		t.Fatalf("B should update across threshold: eta=%d ok=%v", eta, ok)
	}
}

func TestLockWindowEqualityFreezes(t *testing.T) {
	// Initial B = 200. After A is reported late at op time 100, B simulates
	// 201; B-clock == 101 <= lock 101 -> frozen at 200 regardless of delta.
	cfg := newCfg(0,
		[]routewatch.Stop{
			stop("A", routewatch.SoftWindow, 0, 1_000_000, 0),
			stop("B", routewatch.SoftWindow, 0, 1_000_000, 0),
		},
		legSet(
			"DEPOT", "A", int64(100),
			"A", "B", int64(100),
			"DEPOT", "B", int64(0),
		),
		1_000_000, 0, 50, 101)
	m := mustNew(t, cfg)
	sn, err := m.ReportArrival("A", 101, 100)
	if err != nil {
		t.Fatal(err)
	}
	simB := resultByID(*sn, "B")
	if simB.Arrival != 201 {
		t.Fatalf("simulated B=%d", simB.Arrival)
	}
	if eta, ok := etaByID(*sn, "B"); !ok || eta != 200 {
		t.Fatalf("inside lock window (==) must freeze: eta=%d ok=%v", eta, ok)
	}
}

func TestArrivedAndSkippedLosePublication(t *testing.T) {
	cfg := newCfg(0,
		[]routewatch.Stop{
			stop("A", routewatch.SoftWindow, 0, 1_000_000, 0),
			stop("B", routewatch.HardWindow, 0, 5, 0),
		},
		legSet(
			"DEPOT", "A", int64(10),
			"A", "B", int64(10),
			"DEPOT", "B", int64(20),
		),
		1_000_000, 0, 0, 0)
	m := mustNew(t, cfg)
	sn, _ := m.ReportArrival("A", 10, 1)
	if _, ok := etaByID(*sn, "A"); ok {
		t.Fatal("arrived A must lose publication")
	}
	if _, ok := etaByID(*sn, "B"); ok {
		t.Fatal("skipped B must lose publication")
	}
}

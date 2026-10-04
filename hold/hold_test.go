package hold_test

import (
	"errors"
	"testing"

	"ontology/hold"
	"ontology/routing"
	"ontology/wip"
)

type holdEnv struct {
	reg *routing.Registry
	m   *wip.Manager
	sys *hold.System
	rid string
}

func newHoldEnv(t *testing.T, y int, nmin int64) holdEnv {
	t.Helper()
	reg := routing.NewRegistry()
	rid := "r"
	if _, err := reg.Define(rid, 3, []int{1, 2, 2}, []bool{false, true, false}, 2); err != nil {
		t.Fatal(err)
	}
	m := wip.NewManager(reg)
	sys, err := hold.New(m, y, nmin)
	if err != nil {
		t.Fatal(err)
	}
	return holdEnv{reg: reg, m: m, sys: sys, rid: rid}
}

func (e holdEnv) open(t *testing.T, wo string, q int64) {
	t.Helper()
	if err := e.m.Open(wo, e.rid, q); err != nil {
		t.Fatal(err)
	}
}

func TestNewValidation(t *testing.T) {
	reg := routing.NewRegistry()
	m := wip.NewManager(reg)
	for _, c := range []struct {
		y    int
		nmin int64
	}{
		{0, 1}, {101, 1}, {90, 0},
	} {
		if _, err := hold.New(m, c.y, c.nmin); !errors.Is(err, hold.ErrInvalid) {
			t.Fatalf("y=%d nmin=%d got %v", c.y, c.nmin, err)
		}
	}
}

func TestFirstPassThreshold(t *testing.T) {
	cases := []struct {
		name     string
		good     int64
		scrap    int64
		rework   int64
		wantHeld bool
	}{
		{"exact threshold pass", 90, 4, 6, false}, // 9000 == 90*100
		{"one good short holds", 89, 5, 6, true},  // 8900 < 9000
		{"perfect pass", 100, 0, 0, false},
		{"all scrap holds", 0, 50, 0, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newHoldEnv(t, 90, 50)
			e.open(t, "wo", 100)
			if err := e.sys.Report("wo", 1, 0, 100, 0, 0); err != nil {
				t.Fatal(err)
			}
			if err := e.sys.Report("wo", 2, 0, tc.good, tc.scrap, tc.rework); err != nil {
				t.Fatal(err)
			}
			st, _ := e.m.Snapshot("wo")
			if st.Held != tc.wantHeld {
				t.Fatalf("held=%v want %v", st.Held, tc.wantHeld)
			}
			fp, _ := e.sys.Stats("wo")
			if fp[2].Good != tc.good || fp[2].Total != tc.good+tc.scrap+tc.rework {
				t.Fatalf("fp=%v", fp)
			}
		})
	}
}

func TestNminBoundary(t *testing.T) {
	e := newHoldEnv(t, 90, 50)
	e.open(t, "wo", 200)
	if err := e.sys.Report("wo", 1, 0, 200, 0, 0); err != nil {
		t.Fatal(err)
	}
	// sample 10: 80% yield but below Nmin -> no hold
	if err := e.sys.Report("wo", 2, 0, 8, 2, 0); err != nil {
		t.Fatal(err)
	}
	st, _ := e.m.Snapshot("wo")
	if st.Held {
		t.Fatal("should not hold below Nmin")
	}
	// next batch brings total exactly to 50 with 45 good -> 90% exactly -> no hold
	if err := e.sys.Report("wo", 2, 0, 37, 0, 3); err != nil {
		t.Fatal(err)
	}
	st, _ = e.m.Snapshot("wo")
	if st.Held {
		t.Fatal("exact yield must not hold")
	}
	fp, _ := e.sys.Stats("wo")
	if fp[2].Total != 50 || fp[2].Good != 45 {
		t.Fatalf("fp=%v", fp)
	}
	// one more bad report -> 45/51 < 90% holds
	if err := e.sys.Report("wo", 2, 0, 0, 1, 0); err != nil {
		t.Fatal(err)
	}
	st, _ = e.m.Snapshot("wo")
	if !st.Held {
		t.Fatal("expected hold")
	}
}

func TestReworkLayerNotCounted(t *testing.T) {
	e := newHoldEnv(t, 90, 50)
	e.open(t, "wo", 100)
	if err := e.sys.Report("wo", 1, 0, 100, 0, 0); err != nil {
		t.Fatal(err)
	}
	// 50 first-pass units all rework: fGood=0 fTotal=50 -> would hold
	if err := e.sys.Report("wo", 2, 0, 0, 0, 50); err != nil {
		t.Fatal(err)
	}
	st, _ := e.m.Snapshot("wo")
	if !st.Held {
		t.Fatal("first-pass all rework must hold")
	}
	// while held, k=1 reports are rejected too
	if err := e.sys.Report("wo", 2, 1, 1, 0, 0); !errors.Is(err, wip.ErrHeld) {
		t.Fatalf("got %v", err)
	}
	qe := hold.Identity{Name: "alice", Role: hold.QE}
	if err := e.sys.Resume("wo", qe); err != nil {
		t.Fatal(err)
	}
	fp, _ := e.sys.Stats("wo")
	if len(fp) != 0 {
		t.Fatalf("stats not reset: %v", fp)
	}
	// rework-layer reports never enter statistics: 40 scrap at k=1 would be
	// catastrophic yield but must not hold
	if err := e.sys.Report("wo", 2, 1, 0, 40, 10); err != nil {
		t.Fatal(err)
	}
	// those 10 rework to (2,2); good 0 scrap 40 keeps 10 at level 2
	fp, _ = e.sys.Stats("wo")
	if len(fp) != 0 {
		t.Fatalf("rework layer counted: %v", fp)
	}
	st, _ = e.m.Snapshot("wo")
	if st.Held {
		t.Fatal("rework-level report must not hold")
	}
}

func TestTriggeringReportIsEffective(t *testing.T) {
	e := newHoldEnv(t, 90, 10)
	e.open(t, "wo", 50)
	if err := e.sys.Report("wo", 1, 0, 50, 0, 0); err != nil {
		t.Fatal(err)
	}
	if err := e.sys.Report("wo", 2, 0, 5, 5, 0); err != nil {
		t.Fatal(err)
	}
	st, _ := e.m.Snapshot("wo")
	if !st.Held {
		t.Fatal("want hold")
	}
	if st.At(3, 0) != 5 || st.Scrapped != 5 || st.At(2, 0) != 40 {
		t.Fatalf("triggering report not applied: %v", st.Queue)
	}
	// all mutations blocked while held
	if err := e.sys.Report("wo", 2, 0, 1, 0, 0); !errors.Is(err, wip.ErrHeld) {
		t.Fatal(err)
	}
	if err := e.sys.Split("wo", "w2", 2, 0, 1); !errors.Is(err, wip.ErrHeld) {
		t.Fatal(err)
	}
	if _, err := e.sys.Close("wo"); !errors.Is(err, wip.ErrHeld) {
		t.Fatal(err)
	}
}

func TestResumePermissions(t *testing.T) {
	e := newHoldEnv(t, 90, 10)
	e.open(t, "wo", 20)
	if err := e.sys.Report("wo", 1, 0, 20, 0, 0); err != nil {
		t.Fatal(err)
	}
	if err := e.sys.Report("wo", 2, 0, 0, 10, 0); err != nil {
		t.Fatal(err)
	}

	// invalid beats permission beats existence beats state
	if err := e.sys.Resume("", hold.Identity{Name: "x", Role: hold.QE}); !errors.Is(err, hold.ErrInvalid) {
		t.Fatal(err)
	}
	op := hold.Identity{Name: "bob", Role: "operator"}
	if err := e.sys.Resume("ghost", op); !errors.Is(err, hold.ErrUnauthorized) {
		t.Fatalf("permission precedes existence, got %v", err)
	}
	if err := e.sys.Resume("wo", op); !errors.Is(err, hold.ErrUnauthorized) {
		t.Fatal(err)
	}
	qe := hold.Identity{Name: "alice", Role: hold.QE}
	if err := e.sys.Resume("ghost", qe); !errors.Is(err, hold.ErrNotFound) {
		t.Fatal(err)
	}
	if err := e.sys.Resume("wo", qe); err != nil {
		t.Fatal(err)
	}
	if err := e.sys.Resume("wo", qe); !errors.Is(err, hold.ErrState) {
		t.Fatalf("resume when not held: %v", err)
	}
	// after resume, accumulation restarts: fresh 10 perfect samples no hold
	if err := e.sys.Report("wo", 2, 0, 10, 0, 0); err != nil {
		t.Fatal(err)
	}
	st, _ := e.m.Snapshot("wo")
	if st.Held {
		t.Fatal("re-accumulation after resume must start clean")
	}
}

func TestSplitResetChildState(t *testing.T) {
	e := newHoldEnv(t, 50, 100)
	e.open(t, "p", 100)
	if err := e.sys.Report("p", 1, 0, 100, 0, 0); err != nil {
		t.Fatal(err)
	}
	// parent accumulates first-pass samples (20/20 good) but below Nmin
	if err := e.sys.Report("p", 2, 0, 20, 0, 0); err != nil {
		t.Fatal(err)
	}
	if err := e.sys.Split("p", "c", 3, 0, 20); err != nil {
		t.Fatal(err)
	}
	child, _ := e.m.Snapshot("c")
	if child.Q != 20 || child.At(3, 0) != 20 || child.Held || child.Scrapped != 0 {
		t.Fatalf("child=%+v", child)
	}
	// child has no prior stats: 50% over exactly 20 samples (Nmin=100 so below)
	if err := e.sys.Report("c", 3, 0, 20, 0, 0); err != nil {
		t.Fatal(err)
	}
	if res, err := e.sys.Close("c"); err != nil || res.Done != 20 {
		t.Fatalf("close child: %v %+v", err, res)
	}
}

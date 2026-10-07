package orphan

import (
	"fmt"
	"testing"
)

// The number of presence probes for one verdict depends only on configured
// types/groups, never on the number of inbound edges. This test makes that
// claim reproducible: with 3 configured types, the worst case is exactly
// 1 independent probe + 2 joint-member probes, at fan-in 50 and fan-in 5000.
func TestCheckCountIndependentOfEdgeCount(t *testing.T) {
	cfg := Config{
		Types: map[string]TypeConfig{
			"ind": {Kind: Independent},
			"p":   {Kind: Joint, Requires: []string{"q"}},
			"q":   {Kind: Joint, Requires: []string{"p"}},
		},
		GraceGen1Ms: 100,
		GraceGen2Ms: 100,
	}
	measure := func(fanIn int) RetainReport {
		s, _ := newTestSys(t, cfg, 0)
		s.AddObject("o")
		for i := 0; i < fanIn; i++ {
			id := fmt.Sprintf("x%d", i)
			s.AddObject(id)
			mustOK(t, s.AddEdge("ind", id, "o"))
		}
		_, r, _ := s.Evaluate("o")
		if r.PresentCounts["ind"] != fanIn {
			t.Fatalf("audit counts must reflect fan-in %d, got %d", fanIn, r.PresentCounts["ind"])
		}
		return r
	}
	r50 := measure(50)
	r5000 := measure(5000)
	// Independent hit on first configured independent type: constant 1.
	if r50.IndependentChecks != 1 || r5000.IndependentChecks != 1 {
		t.Fatalf("independent probes must stay 1: %d %d", r50.IndependentChecks, r5000.IndependentChecks)
	}
	if r50.JointMemberChecks != 0 || r5000.JointMemberChecks != 0 {
		t.Fatalf("layer 2 must never run after layer-1 retention")
	}

	// Worst case (orphan): every configured type is probed once. Inbound
	// edges of unrelated multiplicity never add probes.
	s, _ := newTestSys(t, cfg, 0)
	s.AddObject("o")
	for i := 0; i < 5000; i++ {
		id := fmt.Sprintf("y%d", i)
		s.AddObject(id)
	}
	// 5000 edges of an incomplete joint type: p present, q absent.
	for i := 0; i < 5000; i++ {
		mustOK(t, s.AddEdge("p", fmt.Sprintf("y%d", i), "o"))
	}
	_, r, _ := s.Evaluate("o")
	if r.Retained {
		t.Fatalf("incomplete joint group must not retain")
	}
	if r.IndependentChecks != 1 {
		t.Fatalf("want exactly 1 independent probe, got %d", r.IndependentChecks)
	}
	if r.JointGroupsTried != 1 || r.JointMemberChecks != 2 {
		t.Fatalf("want 1 group / 2 member probes, got %d/%d", r.JointGroupsTried, r.JointMemberChecks)
	}
}

// Every evaluation and every generation advancement logs inputs, outputs and
// the exact inbound-edge combination behind the verdict.
func TestDecisionLogContents(t *testing.T) {
	s, clk := newTestSys(t, baseCfg(), 0)
	s.AddObject("o")
	s.AddObject("a")
	s.AddObject("b")
	mustOK(t, s.AddEdge("partOf", "a", "o"))
	mustOK(t, s.AddEdge("partOf", "b", "o"))
	mustOK(t, s.AddEdge("locatedIn", "b", "o"))

	var evals []LogEntry
	for _, e := range s.Logs() {
		if e.Op == "evaluate" && e.Object == "o" {
			evals = append(evals, e)
		}
	}
	if len(evals) == 0 {
		t.Fatalf("expected evaluate entries")
	}
	last := evals[len(evals)-1]
	// group id is the deterministic union-find root (first member in sorted
	// order): "locatedIn" < "partOf".
	if !last.Retained || last.Reason != "joint:locatedIn" {
		t.Fatalf("want retained by joint:locatedIn, got %q", last.Reason)
	}
	if last.PresentCounts["partOf"] != 2 || last.PresentCounts["locatedIn"] != 1 {
		t.Fatalf("log must record exact edge combination, got %v", last.PresentCounts)
	}
	if last.FromGen != Gen1 || last.ToGen != NoGen {
		t.Fatalf("log must record queue transition gen1->none, got %v->%v", last.FromGen, last.ToGen)
	}

	// Promotions and cleanups are logged with inputs and outputs.
	for _, e := range [][3]string{
		{"partOf", "a", "o"},
		{"partOf", "b", "o"},
		{"locatedIn", "b", "o"},
	} {
		ok, err := s.RemoveEdge(e[0], e[1], e[2])
		mustOK(t, err)
		if !ok {
			t.Fatalf("missing edge %v", e)
		}
	}
	clk.Set(10)
	s.Scan()
	clk.Set(15)
	s.Scan()
	ops := map[string]bool{}
	for _, e := range s.Logs() {
		if e.Object == "o" {
			ops[e.Op] = true
		}
	}
	for _, want := range []string{"promote", "cleanup_begin", "cleanup_end"} {
		if !ops[want] {
			t.Fatalf("missing log op %q; have %v", want, ops)
		}
	}
}

// External Logger sink receives the same entries.
type sink struct{ entries []LogEntry }

func (s *sink) LogEntry(e LogEntry) { s.entries = append(s.entries, e) }

func TestExternalLogger(t *testing.T) {
	s, _ := newTestSys(t, baseCfg(), 0)
	sn := &sink{}
	s.AddLogger(sn)
	s.AddObject("o")
	if len(sn.entries) != 1 || sn.entries[0].Object != "o" {
		t.Fatalf("external sink did not receive evaluate entry: %+v", sn.entries)
	}
}

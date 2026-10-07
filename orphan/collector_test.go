package orphan

import (
	"errors"
	"testing"
)

func baseCfg() Config {
	return Config{
		Types: map[string]TypeConfig{
			"authoredBy":  {Kind: Independent},
			"derivedFrom": {Kind: Independent},
			"partOf":      {Kind: Joint, Requires: []string{"locatedIn"}},
			"locatedIn":   {Kind: Joint, Requires: []string{"partOf"}},
			"tagged":      {Kind: Joint, Requires: []string{"reviewedBy"}},
			"reviewedBy":  {Kind: Joint, Requires: []string{"tagged"}},
		},
		GraceGen1Ms: 10,
		GraceGen2Ms: 5,
	}
}

func newTestSys(t *testing.T, cfg Config, ms int64) (*System, *ManualClock) {
	t.Helper()
	clk := &ManualClock{}
	clk.Set(ms)
	s, err := New(cfg, clk, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return s, clk
}

func mustOK(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// Independent edge alone retains, regardless of joint state (layer 1).
func TestIndependentRetention(t *testing.T) {
	s, clk := newTestSys(t, baseCfg(), 0)
	s.AddObject("o")
	s.AddObject("a")
	if st, _ := s.StateOf("o"); st.Generation != Gen1 {
		t.Fatalf("fresh orphan should be gen1, got %v", st.Generation)
	}
	mustOK(t, s.AddEdge("authoredBy", "a", "o"))
	if st, _ := s.StateOf("o"); st.Generation != NoGen {
		t.Fatalf("independent edge must retain, gen=%v", st.Generation)
	}
	s.AddObject("b")
	mustOK(t, s.AddEdge("partOf", "b", "o")) // incomplete joint, irrelevant
	clk.Set(100)
	s.Scan()
	if !s.Exists("o") {
		t.Fatalf("object with independent inbound edge must survive scan")
	}
}

// Layer-2 effects and the fixed order between layers.
func TestJointRetentionAndLayerOrder(t *testing.T) {
	s, _ := newTestSys(t, baseCfg(), 0)
	s.AddObject("o")
	s.AddObject("a")
	s.AddObject("b")
	mustOK(t, s.AddEdge("partOf", "a", "o"))
	if st, _ := s.StateOf("o"); st.Generation != Gen1 {
		t.Fatalf("single joint member must not retain, gen=%v", st.Generation)
	}
	mustOK(t, s.AddEdge("locatedIn", "b", "o"))
	if st, _ := s.StateOf("o"); st.Generation != NoGen {
		t.Fatalf("complete joint group must retain, gen=%v", st.Generation)
	}
	s.AddObject("o2")
	mustOK(t, s.AddEdge("tagged", "a", "o2"))
	mustOK(t, s.AddEdge("partOf", "b", "o2")) // two different half groups
	if st, _ := s.StateOf("o2"); st.Generation != Gen1 {
		t.Fatalf("two half groups must not combine, gen=%v", st.Generation)
	}
	s.AddObject("o3")
	s.AddObject("c")
	mustOK(t, s.AddEdge("partOf", "a", "o3"))
	mustOK(t, s.AddEdge("locatedIn", "b", "o3"))
	mustOK(t, s.AddEdge("derivedFrom", "c", "o3"))
	_, r, _ := s.Evaluate("o3")
	if !r.Retained || r.Reason != "independent:derivedFrom" {
		t.Fatalf("layer 1 must win, reason=%q retained=%v", r.Reason, r.Retained)
	}
	if r.JointGroupsTried != 0 || r.JointMemberChecks != 0 {
		t.Fatalf("layer 2 must not be probed after layer-1 hit: %+v", r)
	}
}

// Grace boundary: exactly due advances; one ms before does not.
func TestGraceBoundaryExactVsJustBefore(t *testing.T) {
	s, clk := newTestSys(t, baseCfg(), 0)
	s.AddObject("o")
	clk.Set(9)
	s.Scan()
	if st, _ := s.StateOf("o"); st.Generation != Gen1 {
		t.Fatalf("at 9/10 must remain gen1")
	}
	clk.Set(10)
	s.Scan()
	if st, _ := s.StateOf("o"); st.Generation != Gen2 || st.SinceMs != 10 {
		t.Fatalf("at 10/10 must promote gen2 with fresh timer, got %+v", st)
	}
	clk.Set(14)
	s.Scan()
	if !s.Exists("o") {
		t.Fatalf("at 14/15 must still exist")
	}
	clk.Set(15)
	s.Scan()
	if s.Exists("o") {
		t.Fatalf("at 15/15 must be cleaned")
	}
	s2, clk2 := newTestSys(t, baseCfg(), 0)
	s2.AddObject("o")
	clk2.Set(10)
	s2.Scan()
	clk2.Set(16) // past both deadlines
	s2.Scan()
	if s2.Exists("o") {
		t.Fatalf("past both deadlines must be cleaned")
	}
}

// Re-linked in gen1 clears the queue; re-orphan restarts gen1 from zero.
func TestGen1RelinkCancelsAndRestarts(t *testing.T) {
	s, clk := newTestSys(t, baseCfg(), 0)
	s.AddObject("o")
	s.AddObject("a")
	clk.Set(9)
	mustOK(t, s.AddEdge("authoredBy", "a", "o"))
	if st, _ := s.StateOf("o"); st.Generation != NoGen || st.SinceMs != 0 {
		t.Fatalf("queue record must be cleared, got %+v", st)
	}
	clk.Set(20)
	removed, err := s.RemoveEdge("authoredBy", "a", "o")
	mustOK(t, err)
	if !removed {
		t.Fatalf("edge should have existed")
	}
	st, _ := s.StateOf("o")
	if st.Generation != Gen1 || st.SinceMs != 20 {
		t.Fatalf("re-orphan must restart gen1 at t=20, got %+v", st)
	}
	clk.Set(29)
	s.Scan()
	if got, _ := s.StateOf("o"); got.Generation != Gen1 {
		t.Fatalf("old elapsed time must not carry over, got %v", got.Generation)
	}
	clk.Set(30)
	s.Scan()
	if got, _ := s.StateOf("o"); got.Generation != Gen2 {
		t.Fatalf("must promote after fresh gen1 window, got %v", got.Generation)
	}
}

// Re-linked in gen2 releases directly; no memory affects a later episode.
func TestGen2RelinkNoMemory(t *testing.T) {
	s, clk := newTestSys(t, baseCfg(), 0)
	s.AddObject("o")
	s.AddObject("a")
	clk.Set(10)
	s.Scan()
	if st, _ := s.StateOf("o"); st.Generation != Gen2 {
		t.Fatalf("expected gen2")
	}
	mustOK(t, s.AddEdge("authoredBy", "a", "o"))
	if st, _ := s.StateOf("o"); st.Generation != NoGen {
		t.Fatalf("gen2 relink must release directly, got %v", st.Generation)
	}
	clk.Set(1000)
	removed2, err2 := s.RemoveEdge("authoredBy", "a", "o")
	mustOK(t, err2)
	if !removed2 {
		t.Fatalf("edge should have existed")
	}
	st, _ := s.StateOf("o")
	if st.Generation != Gen1 || st.SinceMs != 1000 {
		t.Fatalf("future episode must start gen1 fresh, got %+v", st)
	}
}

// Escape verdict uses the exact same two-layer rule (joint path).
func TestEscapeUsesSameRuleJoint(t *testing.T) {
	s, clk := newTestSys(t, baseCfg(), 0)
	s.AddObject("o")
	s.AddObject("a")
	s.AddObject("b")
	clk.Set(10)
	s.Scan()
	mustOK(t, s.AddEdge("tagged", "a", "o"))
	if st, _ := s.StateOf("o"); st.Generation != Gen2 {
		t.Fatalf("half joint group must not release gen2, got %v", st.Generation)
	}
	mustOK(t, s.AddEdge("reviewedBy", "b", "o"))
	if st, _ := s.StateOf("o"); st.Generation != NoGen {
		t.Fatalf("complete joint group must release, got %v", st.Generation)
	}
}

// Cleanup atomically applies cascade rules to outgoing edges.
func TestCleanupCascadesOutboundEdges(t *testing.T) {
	s, clk := newTestSys(t, baseCfg(), 0)
	for _, id := range []string{"o", "a", "b", "c"} {
		s.AddObject(id)
	}
	mustOK(t, s.AddEdge("authoredBy", "o", "b")) // b retained only by o
	mustOK(t, s.AddEdge("authoredBy", "c", "a")) // a retained by c
	clk.Set(10)
	s.Scan() // o (orphan) -> gen2
	clk.Set(15)
	s.Scan() // o cleaned; o->b removed; b becomes gen1
	if s.Exists("o") {
		t.Fatalf("o should be cleaned")
	}
	if st, _ := s.StateOf("b"); st.Generation != Gen1 {
		t.Fatalf("b must become gen1 after cascade, got %+v", st)
	}
	if !s.Exists("a") {
		t.Fatalf("a must remain (retained by c->a)")
	}
}

// The four error classes are mutually exclusive and reported in fixed order.
func TestErrorOrdering(t *testing.T) {
	cfg := Config{
		Types: map[string]TypeConfig{
			"ok":      {Kind: Independent},
			"j":       {Kind: Joint, Requires: []string{"missing"}},
			"unknown": {Kind: Kind(0)},
		},
		GraceGen1Ms: 0,
		GraceGen2Ms: -1,
	}
	if _, err := New(cfg, nil, nil); !errors.Is(err, ErrTypeNotConfigured) {
		t.Fatalf("want ErrTypeNotConfigured, got %v", err)
	}
	cfg2 := cfg
	delete(cfg2.Types, "unknown")
	if _, err := New(cfg2, nil, nil); !errors.Is(err, ErrUndefinedJointRef) {
		t.Fatalf("want ErrUndefinedJointRef, got %v", err)
	}
	cfg3 := cfg2
	cfg3.Types["j"] = TypeConfig{Kind: Joint}
	if _, err := New(cfg3, nil, nil); !errors.Is(err, ErrNonPositiveGrace) {
		t.Fatalf("want ErrNonPositiveGrace, got %v", err)
	}
	s, _ := newTestSys(t, baseCfg(), 0)
	s.AddObject("a")
	if err := s.AddEdge("no-such-type", "a", "ghost"); !errors.Is(err, ErrObjectNotFound) {
		t.Fatalf("missing dst must win over bad type, got %v", err)
	}
	if err := s.AddEdge("no-such-type", "ghost2", "a"); !errors.Is(err, ErrObjectNotFound) {
		t.Fatalf("missing src is still class 1, got %v", err)
	}
	if err := s.AddEdge("no-such-type", "a", "a"); !errors.Is(err, ErrTypeNotConfigured) {
		t.Fatalf("both endpoints exist => class 2, got %v", err)
	}
}

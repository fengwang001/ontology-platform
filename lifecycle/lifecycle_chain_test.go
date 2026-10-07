package lifecycle

import "testing"

// chainTypes 构造三级链式触发类型：A.go -> B.go -> C.go。
// B.go 带属性前置条件 ready=1，便于制造链中失败验证整体撤销。
func chainTypes() map[string]*ObjectType {
	return map[string]*ObjectType{
		"A": {
			Name:    "A",
			States:  []State{"a0", "a1"},
			Initial: "a0",
			Transitions: map[string]*TransitionRule{
				"go": {
					Name:     "go",
					From:     []State{"a0"},
					To:       "a1",
					Cascades: []Cascade{{Link: "drives", ToRule: "go"}},
				},
			},
		},
		"B": {
			Name:    "B",
			States:  []State{"b0", "b1"},
			Initial: "b0",
			Transitions: map[string]*TransitionRule{
				"go": {
					Name: "go",
					From: []State{"b0"},
					To:   "b1",
					Preconditions: []Precondition{
						{Attr: &AttrCheck{Key: "ready", Op: CmpEq, Value: int64(1)}},
					},
					Cascades: []Cascade{{Link: "drives", ToRule: "go"}},
				},
			},
		},
		"C": {
			Name:    "C",
			States:  []State{"c0", "c1"},
			Initial: "c0",
			Transitions: map[string]*TransitionRule{
				"go": {Name: "go", From: []State{"c0"}, To: "c1"},
			},
		},
	}
}

func buildChainWorld(t *testing.T) (*Engine, *Store) {
	t.Helper()
	s := NewStore()
	for _, typ := range chainTypes() {
		mustRegister(t, s, typ)
	}
	e := NewEngine(s, NewMemoryLogger())
	mustCreate(t, s, "a", "A")
	mustCreate(t, s, "b", "B")
	mustCreate(t, s, "c", "C")
	mustLink(t, e, "a", "drives", "b")
	mustLink(t, e, "b", "drives", "c")
	return e, s
}

// 5a) 链式触发整体生效。
func TestCascadeAllCommit(t *testing.T) {
	e, s := buildChainWorld(t)
	mustSetAttrs(t, e, "b", AttrOp{Key: "ready", Op: AttrSet, Value: int64(1)})

	out := e.Execute(TransitionRequest{Instance: "a", Rule: "go"})
	if !out[0].OK() {
		t.Fatalf("cascade accept want nil, got %+v", out[0].Err)
	}
	for id, want := range map[InstanceID]State{"a": "a1", "b": "b1", "c": "c1"} {
		if got := stateOf(t, s, id); got != want {
			t.Fatalf("%s state=%s want %s", id, got, want)
		}
	}
}

// 5b) 链中一环失败 -> 此前已模拟生效的环节整体撤销；时钟戳不变。
func TestCascadeRollback(t *testing.T) {
	e, s := buildChainWorld(t)
	clockBefore := map[InstanceID]uint64{}
	for _, id := range []InstanceID{"a", "b", "c"} {
		inst, _ := s.SnapshotInstance(id)
		clockBefore[id] = inst.Clock
	}
	out := e.Execute(TransitionRequest{Instance: "a", Rule: "go"})
	if out[0].Err == nil || out[0].Err.Code != ErrPrecondition {
		t.Fatalf("want precondition reject on chain, got %+v", out[0].Err)
	}
	for _, id := range []InstanceID{"a", "b", "c"} {
		inst, _ := s.SnapshotInstance(id)
		want := State(string(id) + "0")
		if inst.State != want {
			t.Fatalf("%s state=%s want %s (chain not rolled back)", id, inst.State, want)
		}
		if inst.Clock != clockBefore[id] {
			t.Fatalf("%s clock=%d want %d (reject must not bump clock)",
				id, inst.Clock, clockBefore[id])
		}
	}
}

// 6) 循环触发在任何一环生效之前被检测并拒绝（含自环），不靠计数截断。
func TestCascadeCycleDetected(t *testing.T) {
	typ := &ObjectType{
		Name:    "N",
		States:  []State{"n0", "n1"},
		Initial: "n0",
		Transitions: map[string]*TransitionRule{
			"go": {
				Name:     "go",
				From:     []State{"n0"},
				To:       "n1",
				Cascades: []Cascade{{Link: "next", ToRule: "go"}},
			},
		},
	}

	s := NewStore()
	mustRegister(t, s, typ)
	e := NewEngine(s, NewMemoryLogger())
	mustCreate(t, s, "n1", "N")
	mustCreate(t, s, "n2", "N")
	mustCreate(t, s, "n3", "N")
	mustLink(t, e, "n1", "next", "n2")
	mustLink(t, e, "n2", "next", "n3")
	mustLink(t, e, "n3", "next", "n1")

	out := e.Execute(TransitionRequest{Instance: "n1", Rule: "go"})
	if out[0].Err == nil || out[0].Err.Code != ErrCycle {
		t.Fatalf("want cycle reject, got %+v", out[0].Err)
	}
	for _, id := range []InstanceID{"n1", "n2", "n3"} {
		if got := stateOf(t, s, id); got != "n0" {
			t.Fatalf("%s changed to %s despite cycle", id, got)
		}
	}

	s2 := NewStore()
	mustRegister(t, s2, typ)
	e2 := NewEngine(s2, NewMemoryLogger())
	mustCreate(t, s2, "z", "N")
	mustLink(t, e2, "z", "next", "z")
	if out := e2.Execute(TransitionRequest{Instance: "z", Rule: "go"}); out[0].Err.Code != ErrCycle {
		t.Fatalf("self-loop want cycle, got %+v", out[0].Err)
	}
}

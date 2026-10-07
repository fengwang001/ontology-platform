package lifecycle_test

import (
	"testing"

	"ontology/internal/naive"
	"ontology/lifecycle"
)

// 链式结构（含可成功链、一环失败链、循环链）在两套实现上的结果必须一致。
func TestCascadeStructuresDifferential(t *testing.T) {
	spec := lifecycleCycleSpec()
	nspec := naiveCycleSpec()

	cases := []struct {
		name   string
		seeds  func(*lifecycle.Store, *naive.Engine)
		op     lifecycle.Op
		nop    naive.Op
		accept bool
	}{
		{
			name: "chain-ok",
			seeds: func(st *lifecycle.Store, ne *naive.Engine) {
				seedCyclePair(st, ne, "a", "b", "ready")
			},
			op:     lifecycle.Fire("a", "go"),
			nop:    naive.Op{Kind: naive.OpFire, InstanceID: "a", Transition: "go"},
			accept: true,
		},
		{
			name: "chain-broken",
			seeds: func(st *lifecycle.Store, ne *naive.Engine) {
				seedCyclePair(st, ne, "a", "b", "blocked")
			},
			op:     lifecycle.Fire("a", "go"),
			nop:    naive.Op{Kind: naive.OpFire, InstanceID: "a", Transition: "go"},
			accept: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st := lifecycle.NewStore()
			ne := naive.NewEngine(nspec)
			tc.seeds(st, ne)
			eng := lifecycle.NewEngine(spec, st, lifecycle.DiscardLogger{})

			r, _ := eng.Batch([]lifecycle.Op{tc.op})
			nr := ne.Batch([]naive.Op{tc.nop})
			if r.Committed != nr.Committed {
				t.Fatalf("committed 不一致: prod=%v naive=%v",
					r.Committed, nr.Committed)
			}
			if r.Committed != tc.accept {
				t.Fatalf("期望 accept=%v", tc.accept)
			}
			if !r.Committed {
				pc := int(r.Outcomes[0].Err.Code)
				nc := int(nr.Ops[0].Err.Code)
				if pc != nc {
					t.Fatalf("拒绝码不一致: prod=%d naive=%d", pc, nc)
				}
			}
		})
	}
}

// 直接验证循环检测（生产侧独立结构）。
func TestCyclePairLoop(t *testing.T) {
	spec := lifecycleCycleSpec()
	st := lifecycle.NewStore()
	for _, id := range []string{"a", "b"} {
		st.AddInstance(&lifecycle.Instance{ID: id, Type: "node", State: "ready"})
	}
	eng := lifecycle.NewEngine(spec, st, lifecycle.DiscardLogger{})
	st.AddLinkForSeed("next", "a", "b")
	st.AddLinkForSeed("next", "b", "a")
	r, _ := eng.Batch([]lifecycle.Op{lifecycle.Fire("a", "go")})
	if r.Committed || r.Outcomes[0].Err.Code != lifecycle.CodeCycle {
		t.Fatalf("期望循环拒绝, got committed=%v err=%+v",
			r.Committed, r.Outcomes[0].Err)
	}
}

func lifecycleCycleSpec() *lifecycle.Schema {
	return &lifecycle.Schema{Types: map[string]*lifecycle.ObjectType{
		"node": {
			Name:        "node",
			States:      []string{"ready", "blocked", "done"},
			FinalStates: map[string]bool{},
			Transitions: map[string]*lifecycle.Transition{
				"go": {
					Name: "go", From: "ready", To: "done",
					Hooks: []lifecycle.HookRule{
						{LinkType: "next", States: []string{"done"}},
					},
					Cascades: []lifecycle.CascadeRule{{
						LinkType: "next", WhenStates: []string{"ready"},
						Transition: "go",
					}},
				},
			},
		},
	}}
}

func naiveCycleSpec() *naive.Spec {
	return &naive.Spec{Types: map[string]*naive.ObjectType{
		"node": {
			Final: map[string]bool{},
			Transitions: map[string]*naive.Transition{
				"go": {
					Name: "go", From: "ready", To: "done",
					Hooks: []naive.Hook{
						{LinkType: "next", States: []string{"done"}},
					},
					Cascades: []naive.Cascade{{
						LinkType: "next", WhenStates: []string{"ready"},
						Transition: "go",
					}},
				},
			},
		},
	}}
}

func seedCyclePair(st *lifecycle.Store, ne *naive.Engine, a, b, bState string) {
	st.AddInstance(&lifecycle.Instance{ID: a, Type: "node", State: "ready"})
	st.AddInstance(&lifecycle.Instance{ID: b, Type: "node", State: bState})
	ne.AddInstance(&naive.Instance{ID: a, Type: "node", State: "ready"})
	ne.AddInstance(&naive.Instance{ID: b, Type: "node", State: bState})
	st.AddLinkForSeed("next", a, b)
	ne.SeedLink("next", a, b)
}

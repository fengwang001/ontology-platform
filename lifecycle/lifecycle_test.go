package lifecycle

import "testing"

func mustRegister(t *testing.T, s *Store, typ *ObjectType) {
	t.Helper()
	if err := s.RegisterType(typ); err != nil {
		t.Fatalf("register type %s: %v", typ.Name, err)
	}
}

func mustCreate(t *testing.T, s *Store, id InstanceID, typ string) *Instance {
	t.Helper()
	inst, err := s.CreateInstance(id, typ)
	if err != nil {
		t.Fatalf("create %s: %v", id, err)
	}
	return inst
}

func mustSetAttrs(t *testing.T, e *Engine, id InstanceID, ops ...AttrOp) {
	t.Helper()
	if err := e.SetAttrs(id, ops...); err != nil {
		t.Fatalf("SetAttrs(%s): %v", id, err)
	}
}

func mustLink(t *testing.T, e *Engine, src InstanceID, link LinkType, dst InstanceID) {
	t.Helper()
	if err := e.ModifyLinks(src, LinkOp{Link: link, Target: dst, Op: LinkAdd}); err != nil {
		t.Fatalf("add link %s -%s-> %s: %v", src, link, dst, err)
	}
}

func stateOf(t *testing.T, s *Store, id InstanceID) State {
	t.Helper()
	inst, ok := s.SnapshotInstance(id)
	if !ok {
		t.Fatalf("instance %s missing", id)
	}
	return inst.State
}

// 场景：订单 draft -> paid 需要 attr paid>=1 且经由 items 链接至少 1 个条目。
func orderTypes() *ObjectType {
	return &ObjectType{
		Name:          "Order",
		States:        []State{"draft", "paid", "shipped", "closed"},
		Initial:       "draft",
		TerminalsList: []State{"closed"},
		Transitions: map[string]*TransitionRule{
			"pay": {
				Name: "pay",
				From: []State{"draft"},
				To:   "paid",
				Preconditions: []Precondition{
					{Attr: &AttrCheck{Key: "balance", Op: CmpGe, Value: int64(1)}},
					{LinkCount: &LinkCountCheck{Link: "items", Min: 1, Max: -1}},
				},
				AttrBounds: []AttrBound{{Key: "balance", Min: ptr(1), Max: nil}},
			},
			"ship": {
				Name: "ship",
				From: []State{"paid"},
				To:   "shipped",
			},
			"close": {
				Name:       "close",
				From:       []State{"shipped"},
				To:         "closed",
				MutexGroup: "",
			},
		},
	}
}

func ptr(v int64) *int64 { return &v }

func newTestEngine() (*Engine, *Store, *MemoryLogger) {
	s := NewStore()
	log := NewMemoryLogger()
	return NewEngine(s, log), s, log
}

// 1) 单实例迁移前置条件判定。
func TestPreconditionSingleInstance(t *testing.T) {
	e, s, _ := newTestEngine()
	mustRegister(t, s, orderTypes())
	mustCreate(t, s, "o1", "Order")
	mustCreate(t, s, "i1", "Order")

	// 缺余额、缺条目：拒绝，状态不变、时钟不变。
	out := e.Execute(TransitionRequest{Instance: "o1", Rule: "pay"})
	if out[0].OK() || out[0].Err.Code != ErrPrecondition {
		t.Fatalf("want precondition reject, got %+v", out[0].Err)
	}
	if got := stateOf(t, s, "o1"); got != "draft" {
		t.Fatalf("state changed after reject: %s", got)
	}

	// 满足余额但缺条目：仍拒绝。
	mustSetAttrs(t, e, "o1", AttrOp{Key: "balance", Op: AttrSet, Value: int64(5)})
	if out := e.Execute(TransitionRequest{Instance: "o1", Rule: "pay"}); out[0].Err.Code != ErrPrecondition {
		t.Fatalf("want precondition reject (no items), got %+v", out[0].Err)
	}

	mustLink(t, e, "o1", "items", "i1")
	out = e.Execute(TransitionRequest{Instance: "o1", Rule: "pay"})
	if !out[0].OK() {
		t.Fatalf("want accept, got %+v", out[0].Err)
	}
	if got := stateOf(t, s, "o1"); got != "paid" {
		t.Fatalf("state = %s, want paid", got)
	}

	// 未声明规则 / 当前状态不允许：ErrUndeclared，且优先级高于前置条件。
	if out := e.Execute(TransitionRequest{Instance: "o1", Rule: "nope"}); out[0].Err.Code != ErrUndeclared {
		t.Fatalf("want undeclared, got %+v", out[0].Err)
	}
}

// 2) 互斥迁移按调用方声明的优先顺序放行，与到达顺序无关。
func TestMutexPriority(t *testing.T) {
	s := NewStore()
	mustRegister(t, s, &ObjectType{
		Name:    "Doc",
		States:  []State{"editing", "published", "archived"},
		Initial: "editing",
		Transitions: map[string]*TransitionRule{
			"publish": {Name: "publish", From: []State{"editing"}, To: "published", MutexGroup: "g1"},
			"archive": {Name: "archive", From: []State{"editing"}, To: "archived", MutexGroup: "g1"},
		},
	})
	mustCreate(t, s, "d1", "Doc")

	// 同一处理单元内两条互斥迁移前置条件都成立：只放行 Priority 小的。
	e := NewEngine(s, NewMemoryLogger())
	// 故意把高优先请求放在后面，证明不依赖到达顺序。
	out := e.Execute(
		TransitionRequest{Instance: "d1", Rule: "archive", Priority: 5},
		TransitionRequest{Instance: "d1", Rule: "publish", Priority: 1},
	)
	if out[0].Err.Code != ErrMutex {
		t.Fatalf("archive want mutex reject, got %+v", out[0].Err)
	}
	if !out[1].OK() {
		t.Fatalf("publish want accept, got %+v", out[1].Err)
	}
	if got := stateOf(t, s, "d1"); got != "published" {
		t.Fatalf("state=%s want published", got)
	}
}

// 3) 迁移后基数校验使用生效后的真实链接状态；超限则整体不生效。
func TestPostCardinalityTiming(t *testing.T) {
	s := NewStore()
	mustRegister(t, s, &ObjectType{
		Name:    "Basket",
		States:  []State{"open", "locked"},
		Initial: "open",
		Transitions: map[string]*TransitionRule{
			"lock": {
				Name: "lock",
				From: []State{"open"},
				To:   "locked",
				// 迁移后 items 出边必须 <= 2。
				Cardinality: []CardinalityBound{{Link: "items", Min: 0, Max: 2}},
			},
		},
	})
	e := NewEngine(s, NewMemoryLogger())
	mustCreate(t, s, "b", "Basket")
	for _, id := range []InstanceID{"x1", "x2", "x3"} {
		mustCreate(t, s, id, "Basket")
	}
	mustLink(t, e, "b", "items", "x1")

	// 迁移本身新增两条链接后总数为 3，超过上限 2：拒绝，状态与链接都不变。
	out := e.Execute(TransitionRequest{
		Instance: "b", Rule: "lock",
		Links: []LinkOp{
			{Link: "items", Target: "x2", Op: LinkAdd},
			{Link: "items", Target: "x3", Op: LinkAdd},
		},
	})
	if out[0].Err == nil || out[0].Err.Code != ErrCardinality {
		t.Fatalf("want cardinality reject, got %+v", out[0].Err)
	}
	if got := stateOf(t, s, "b"); got != "open" {
		t.Fatalf("state changed to %s", got)
	}
	if s.Neighbors("b", "items") != nil && len(s.Neighbors("b", "items")) != 1 {
		t.Fatalf("links changed after reject: %v", s.Neighbors("b", "items"))
	}

	// 只新增一条，迁移后总数为 2，恰好满足：接受。
	out = e.Execute(TransitionRequest{
		Instance: "b", Rule: "lock",
		Links: []LinkOp{{Link: "items", Target: "x2", Op: LinkAdd}},
	})
	if !out[0].OK() {
		t.Fatalf("want accept, got %+v", out[0].Err)
	}
	if got := stateOf(t, s, "b"); got != "locked" {
		t.Fatalf("state=%s", got)
	}
	if n := len(s.Neighbors("b", "items")); n != 2 {
		t.Fatalf("items=%d want 2", n)
	}
}

// 4) 跨实例钩子联动拒绝：被连接实例状态不满足时发起方也不生效。
func TestCrossInstanceHook(t *testing.T) {
	s := NewStore()
	mustRegister(t, s, &ObjectType{
		Name:    "Job",
		States:  []State{"ready", "running", "done"},
		Initial: "ready",
		Transitions: map[string]*TransitionRule{
			"run": {
				Name: "run",
				From: []State{"ready"},
				To:   "running",
				// 运行要求所有 depends-on 邻居已 done。
				Hooks: []Hook{{Link: "depends", RequireStates: []State{"done"}}},
			},
			"finish": {Name: "finish", From: []State{"ready", "running"}, To: "done"},
		},
	})
	e := NewEngine(s, NewMemoryLogger())
	mustCreate(t, s, "a", "Job")
	mustCreate(t, s, "b", "Job")
	mustLink(t, e, "a", "depends", "b")

	out := e.Execute(TransitionRequest{Instance: "a", Rule: "run"})
	if out[0].Err == nil || out[0].Err.Code != ErrHook {
		t.Fatalf("want hook reject, got %+v", out[0].Err)
	}
	if got := stateOf(t, s, "a"); got != "ready" {
		t.Fatalf("a changed to %s", got)
	}

	// 让 b 先完成，再触发 a：钩子通过。
	if out := e.Execute(TransitionRequest{Instance: "b", Rule: "finish"}); !out[0].OK() {
		t.Fatalf("b finish: %+v", out[0].Err)
	}
	if out := e.Execute(TransitionRequest{Instance: "a", Rule: "run"}); !out[0].OK() {
		t.Fatalf("a run after b done: %+v", out[0].Err)
	}
	if got := stateOf(t, s, "a"); got != "running" {
		t.Fatalf("a state=%s want running", got)
	}
}

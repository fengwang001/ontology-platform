package precheck

import (
	"reflect"
	"testing"
)

// testHook 是可按时刻/参数条件失败、并产出意图的可编程测试钩子。
type testHook struct {
	id           string
	preFail      bool
	postFail     bool
	intents      []Intent
	mutates      bool // 尝试修改入参与 scratch，用于验证只读冻结
	setScratch   string
	checkScratch string // post 校验 pre scratch 值
}

func (h *testHook) ID() string { return h.id }

func (h *testHook) Pre(ctx *PreContext) error {
	if h.mutates {
		// 这些写入只能影响本次演算的私有拷贝，不能外泄到调用者或世界状态。
		ctx.params["mutated"] = true
	}
	if h.setScratch != "" {
		ctx.SetScratch("mark", h.setScratch)
	}
	if h.preFail {
		return &HookError{Code: "pre_" + h.id, Message: "pre fail"}
	}
	return nil
}

func (h *testHook) Post(ctx *PostContext) ([]Intent, error) {
	if h.checkScratch != "" {
		v, _ := ctx.Scratch("mark")
		if v != h.checkScratch {
			return nil, &HookError{Code: "scratch_mismatch", Message: "post saw wrong pre snapshot"}
		}
	}
	if h.postFail {
		return nil, &HookError{Code: "post_" + h.id, Message: "post fail"}
	}
	return h.intents, nil
}

func mustApply(t *testing.T, e *Engine, ev Event) {
	t.Helper()
	if err := e.Apply(ev); err != nil {
		t.Fatalf("apply %v: %v", ev.Kind, err)
	}
}

func baseWorld(t *testing.T) (*Engine, *NaiveReplay) {
	t.Helper()
	reg := NewRegistry()
	for _, h := range []Hook{
		&testHook{id: "p1"},
		&testHook{id: "p2", preFail: true},
		&testHook{id: "q1", intents: []Intent{{Op: "upsert", ObjectKey: "derived", Value: "x", Source: "q1"}}},
		&testHook{id: "q2", postFail: true},
		&testHook{id: "writer", mutates: true, setScratch: "m"},
		&testHook{id: "reader", checkScratch: "m"},
	} {
		reg.Register(h)
	}
	e := NewEngine(reg)
	n := NewNaiveReplay(reg)

	mustApply(t, e, Event{At: 1, Kind: EvTypeSchema, ActionType: "A", Schema: &ParamSchema{
		Version: 1, Required: []string{"object_key"}, Types: map[string]string{"object_key": "string", "n": "int"},
		RequiredPermission: "P",
	}})
	mustApply(t, e, Event{At: 1, Kind: EvUserAdded, User: "alice"})
	mustApply(t, e, Event{At: 1, Kind: EvEdgeAdded, From: "alice", To: "role1"})
	mustApply(t, e, Event{At: 1, Kind: EvEdgeAdded, From: "role1", To: "P"})
	mustApply(t, e, Event{At: 2, Kind: EvHookSet, ActionType: "A", HookSet: &HookSet{Version: 2,
		Pre: []HookRef{{ID: "p1"}}, Post: []HookRef{{ID: "q1"}}}})

	n.Load(e.Events())
	return e, n
}

func crossCheck(t *testing.T, e *Engine, n *NaiveReplay, req PrecheckRequest) *PrecheckResult {
	t.Helper()
	res, err := e.Precheck(req)
	if err != nil {
		t.Fatal(err)
	}
	nres, _, err := n.Precheck(req)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(Canonical(res), Canonical(nres)) {
		t.Fatalf("model mismatch\nengine=%#v\nnaive =%#v", Canonical(res), Canonical(nres))
	}
	ok := e.Audit().Annotate(res.AuditSeq, CrossCheckRecord{Equivalent: true, Note: "naive replay"})
	if !ok {
		t.Fatal("audit annotation failed")
	}
	return res
}

func TestAllowedProducesIntents(t *testing.T) {
	e, n := baseWorld(t)
	req := PrecheckRequest{At: 5, ActionType: "A", Caller: "alice",
		Params: map[string]any{"object_key": "o1", "value": "v"}, Aggregation: FailFast}
	res := crossCheck(t, e, n, req)
	if res.Verdict != VerdictAllowed {
		t.Fatalf("want allowed, got %s: %v", res.Verdict, res.Failures)
	}
	if len(res.Intents) != 2 {
		t.Fatalf("want 2 intents (default + q1), got %d", len(res.Intents))
	}
	if res.SchemaVersion != 1 || res.HookSetVersion != 2 {
		t.Fatalf("versions wrong: %+v", res)
	}
	entry, ok := e.Audit().Get(res.AuditSeq)
	if !ok || entry.CrossCheck == nil || !entry.CrossCheck.Equivalent {
		t.Fatal("audit must carry cross-check record")
	}
	if entry.PermSnap == nil || !entry.PermSnap.Authorize("alice", "P") {
		t.Fatal("audit must record the permission snapshot used")
	}
}

func TestPrecheckIsReadOnly(t *testing.T) {
	e, _ := baseWorld(t)
	before := len(e.Events())
	params := map[string]any{"object_key": "o1"}
	req := PrecheckRequest{At: 5, ActionType: "A", Caller: "alice", Params: params, Aggregation: CollectAll}

	mustApply(t, e, Event{At: 2, Kind: EvHookSet, ActionType: "A", HookSet: &HookSet{Version: 2,
		Pre: []HookRef{{ID: "writer"}, {ID: "reader"}}}})

	res, err := e.Precheck(req)
	if err != nil {
		t.Fatal(err)
	}
	if res.Verdict != VerdictAllowed {
		t.Fatalf("reader should see writer's scratch via frozen pre snapshot: %v", res.Failures)
	}
	if params["mutated"] != nil {
		t.Fatal("precheck must not leak hook mutations into caller params")
	}
	if len(e.Events()) != before+1 {
		t.Fatal("precheck must not append any domain events")
	}
}

func TestErrorPriority(t *testing.T) {
	e, n := baseWorld(t)

	// 后续固定装置必须按不递减时刻追加（全局时钟单调）。
	mustApply(t, e, Event{At: 3, Kind: EvTypeSchema, ActionType: "Early", Schema: &ParamSchema{Version: 3, RequiredPermission: "P"}})
	mustApply(t, e, Event{At: 3, Kind: EvUserAdded, User: "bob"})

	// 同一请求同时满足 B(缺失)、A(未定义)、C(调用者不存在)、D(参数非法) 时，
	// 必须稳定地只报 B（快照缺失优先）。
	mustApply(t, e, Event{At: 6, Kind: EvHistoryGap, GapDomain: GapDomainSchema, GapKey: "Ghost", GapStart: 0, GapEnd: 100})
	n.Load(e.Events())
	req := PrecheckRequest{At: 10, ActionType: "Ghost", Caller: "nobody", Params: map[string]any{}}
	res := crossCheck(t, e, n, req)
	if res.ResolutionErr == nil || res.ResolutionErr.Kind != ErrSnapshotMissing {
		t.Fatalf("want snapshot_missing, got %+v", res.ResolutionErr)
	}

	// 仅 A：动作类型尚未定义。
	res = crossCheck(t, e, n, PrecheckRequest{At: 0, ActionType: "A", Caller: "alice", Params: map[string]any{"object_key": "x"}})
	if res.ResolutionErr.Kind != ErrActionNotYetDefined {
		t.Fatalf("want action_not_yet_defined, got %v", res.ResolutionErr.Kind)
	}

	// A 与 C 同时：A 优先于 C。
	res = crossCheck(t, e, n, PrecheckRequest{At: 0, ActionType: "A", Caller: "ghost", Params: map[string]any{"object_key": "x"}})
	if res.ResolutionErr.Kind != ErrActionNotYetDefined {
		t.Fatalf("A must beat C, got %v", res.ResolutionErr.Kind)
	}

	// 仅 C：调用者尚不存在（晚于类型定义、早于用户创建）。
	n.Load(e.Events())
	res = crossCheck(t, e, n, PrecheckRequest{At: 4, ActionType: "Early", Caller: "alice2", Params: map[string]any{}})
	if res.ResolutionErr.Kind != ErrCallerNotExist {
		t.Fatalf("want caller_not_exist, got %v", res.ResolutionErr.Kind)
	}

	// 仅 D：参数违反当时结构约束。
	res = crossCheck(t, e, n, PrecheckRequest{At: 5, ActionType: "A", Caller: "alice",
		Params: map[string]any{"n": "not-an-int"}})
	if res.ResolutionErr.Kind != ErrInvalidParams {
		t.Fatalf("want invalid_params, got %v", res.ResolutionErr.Kind)
	}

	// 权限不足是“拒绝”而非解析错误，且先于前置钩子。
	n.Load(e.Events())
	n.Load(e.Events())
	res = crossCheck(t, e, n, PrecheckRequest{At: 5, ActionType: "A", Caller: "bob",
		Params: map[string]any{"object_key": "x"}})
	if res.Verdict != VerdictDenied || len(res.Failures) != 1 ||
		res.Failures[0].HookID != authGateID || res.Failures[0].Stage != "pre" {
		t.Fatalf("want single pre-stage auth denial, got %+v", res)
	}
}

func TestPrePostAggregationRules(t *testing.T) {
	e, n := baseWorld(t)

	// 前置阶段：两个都会失败的前置钩子。
	mustApply(t, e, Event{At: 3, Kind: EvHookSet, ActionType: "A", HookSet: &HookSet{Version: 3,
		Pre: []HookRef{{ID: "p2"}, {ID: "p2"}}, Post: []HookRef{{ID: "q2"}}}})
	n.Load(e.Events())
	params := map[string]any{"object_key": "x"}

	fast := crossCheck(t, e, n, PrecheckRequest{At: 5, ActionType: "A", Caller: "alice", Params: params, Aggregation: FailFast})
	if fast.Verdict != VerdictDenied || len(fast.Failures) != 1 {
		t.Fatalf("fail-fast must report one pre failure, got %+v", fast.Failures)
	}

	all := crossCheck(t, e, n, PrecheckRequest{At: 5, ActionType: "A", Caller: "alice", Params: params, Aggregation: CollectAll})
	if all.Verdict != VerdictDenied || len(all.Failures) != 2 {
		t.Fatalf("collect-all must aggregate both pre failures, got %+v", all.Failures)
	}
	for _, f := range all.Failures {
		if f.Stage != "pre" {
			t.Fatal("pre failures must never be mixed with post failures")
		}
	}

	// 后置阶段：前置通过、两个后置钩子失败。
	mustApply(t, e, Event{At: 4, Kind: EvHookSet, ActionType: "A", HookSet: &HookSet{Version: 4,
		Pre: []HookRef{{ID: "p1"}}, Post: []HookRef{{ID: "q2"}, {ID: "q2"}}}})
	n.Load(e.Events())

	fastPost := crossCheck(t, e, n, PrecheckRequest{At: 5, ActionType: "A", Caller: "alice", Params: params, Aggregation: FailFast})
	if fastPost.Verdict != VerdictDenied || len(fastPost.Failures) != 1 || fastPost.Failures[0].Stage != "post" {
		t.Fatalf("want one post failure, got %+v", fastPost.Failures)
	}

	allPost := crossCheck(t, e, n, PrecheckRequest{At: 5, ActionType: "A", Caller: "alice", Params: params, Aggregation: CollectAll})
	if allPost.Verdict != VerdictDenied || len(allPost.Failures) != 2 {
		t.Fatalf("want two post failures, got %+v", allPost.Failures)
	}
	if len(allPost.Intents) != 0 {
		t.Fatal("denied actions must report no intents")
	}
}

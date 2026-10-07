package precheck

import "testing"

// TestBoundaryExhaustive 穷举钩子版本与权限继承快照在切换边界两侧的行为：
// 规定全部采用左闭右开——新版本在其生效时刻立即生效；
// 同一边界对引擎与朴素模型必须给出完全一致的判定。
func TestBoundaryExhaustive(t *testing.T) {
	reg := NewRegistry()
	hDenyPre := &testHook{id: "denyPreV2", preFail: true}
	hDenyPost := &testHook{id: "denyPostV2", postFail: true}
	reg.Register(&testHook{id: "noopPre"})
	reg.Register(hDenyPre)
	reg.Register(hDenyPost)

	e := NewEngine(reg)
	n := NewNaiveReplay(reg)

	// t=10: 类型定义、调用者、直接授权；钩子集合 v10（无钩子 -> 允许）。
	mustApply(t, e, Event{At: 10, Kind: EvTypeSchema, ActionType: "A", Schema: &ParamSchema{
		Version: 10, Required: []string{"object_key"}, RequiredPermission: "P"}})
	mustApply(t, e, Event{At: 10, Kind: EvUserAdded, User: "u"})
	mustApply(t, e, Event{At: 10, Kind: EvEdgeAdded, From: "u", To: "P"})
	mustApply(t, e, Event{At: 10, Kind: EvHookSet, ActionType: "A", HookSet: &HookSet{Version: 10}})

	// t=20: 钩子集合升级：新增一个必定失败的前置钩子。
	mustApply(t, e, Event{At: 20, Kind: EvHookSet, ActionType: "A", HookSet: &HookSet{Version: 20,
		Pre: []HookRef{{ID: "denyPreV2"}}}})

	// t=30: 权限继承关系调整：收回直接授权，改为 u -> role -> P（继承链）。
	mustApply(t, e, Event{At: 30, Kind: EvEdgeRemoved, From: "u", To: "P"})
	mustApply(t, e, Event{At: 30, Kind: EvEdgeAdded, From: "u", To: "role"})
	mustApply(t, e, Event{At: 30, Kind: EvEdgeAdded, From: "role", To: "P"})

	// t=40: 钩子再升级：前置放行、后置失败。
	mustApply(t, e, Event{At: 40, Kind: EvHookSet, ActionType: "A", HookSet: &HookSet{Version: 40,
		Pre: []HookRef{{ID: "noopPre"}}, Post: []HookRef{{ID: "denyPostV2"}}}})

	// t=50: 继承链被切断（role -> P 移除）-> 权限不足。
	mustApply(t, e, Event{At: 50, Kind: EvEdgeRemoved, From: "role", To: "P"})

	n.Load(e.Events())

	// 对每个边界时刻穷举 {t-1, t} 两侧；t-1 用旧版本/旧关系，t 用新版本/新关系。
	cases := []struct {
		name     string
		at       Moment
		verdict  Verdict
		failHook string
		stage    string
	}{
		{"before type defined", 9, VerdictResolutionError, "", ""},
		{"at type defined, no hooks", 10, VerdictAllowed, "", ""},
		{"just before pre-deny hook", 19, VerdictAllowed, "", ""},
		{"at pre-deny activation", 20, VerdictDenied, "denyPreV2", "pre"},
		{"still pre-deny", 29, VerdictDenied, "denyPreV2", "pre"},
		{"at inheritance switch (still denied by pre hook)", 30, VerdictDenied, "denyPreV2", "pre"},
		{"just before post-deny hook", 39, VerdictDenied, "denyPreV2", "pre"},
		{"at post-deny activation", 40, VerdictDenied, "denyPostV2", "post"},
		{"still post-deny, chain alive", 49, VerdictDenied, "denyPostV2", "post"},
		{"at permission cut (auth gate wins over hook)", 50, VerdictDenied, authGateID, "pre"},
		{"after permission cut", 60, VerdictDenied, authGateID, "pre"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			req := PrecheckRequest{At: c.at, ActionType: "A", Caller: "u",
				Params:      map[string]any{"object_key": "o"},
				Aggregation: FailFast}
			res := crossCheck(t, e, n, req)
			if res.Verdict != c.verdict {
				t.Fatalf("at %d want %s got %s (%v)", c.at, c.verdict, res.Verdict, res.Failures)
			}
			if c.verdict == VerdictDenied {
				if len(res.Failures) != 1 || res.Failures[0].HookID != c.failHook || res.Failures[0].Stage != c.stage {
					t.Fatalf("at %d want failure %s/%s, got %+v", c.at, c.failHook, c.stage, res.Failures)
				}
			}
		})
	}
}

// TestSchemaBoundaryParams 验证参数结构约束同样在边界时刻切换：
// 旧版本不要求某字段，t 起新版本要求该字段。
func TestSchemaBoundaryParams(t *testing.T) {
	reg := NewRegistry()
	e := NewEngine(reg)
	n := NewNaiveReplay(reg)

	mustApply(t, e, Event{At: 0, Kind: EvTypeSchema, ActionType: "A", Schema: &ParamSchema{Version: 0, RequiredPermission: "P"}})
	mustApply(t, e, Event{At: 0, Kind: EvUserAdded, User: "u"})
	mustApply(t, e, Event{At: 0, Kind: EvEdgeAdded, From: "u", To: "P"})
	mustApply(t, e, Event{At: 10, Kind: EvTypeSchema, ActionType: "A", Schema: &ParamSchema{
		Version: 10, Required: []string{"object_key"}, RequiredPermission: "P"}})
	n.Load(e.Events())

	params := map[string]any{}
	old := crossCheck(t, e, n, PrecheckRequest{At: 9, ActionType: "A", Caller: "u", Params: params})
	if old.Verdict != VerdictAllowed {
		t.Fatalf("old schema should allow empty params, got %+v", old.ResolutionErr)
	}
	at := crossCheck(t, e, n, PrecheckRequest{At: 10, ActionType: "A", Caller: "u", Params: params})
	if at.Verdict != VerdictResolutionError || at.ResolutionErr.Kind != ErrInvalidParams {
		t.Fatalf("new schema must reject at exact boundary, got %+v", at)
	}
}

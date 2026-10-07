package ontology

import (
	"reflect"
	"strings"
	"testing"
)

// TestDepthBoundary 覆盖传播深度上限的边界：恰好等于上限可通过，
// 少一级则报 ErrDepthExceeded，上限为 0 时任何出边都触发错误。
func TestDepthBoundary(t *testing.T) {
	cases := []struct {
		name    string
		depth   int
		wantErr ErrorKind
	}{
		{"exact-fit", 3, 0},
		{"one-short", 2, ErrDepthExceeded},
		{"zero-with-edge", 0, ErrDepthExceeded},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := chainStore()
			eng := NewEngine(s, allowAll())
			eng.RegisterAction(modifyDecl(tc.depth, InvisibleDeny, MergeAll))
			before := s.Snapshot()
			allowed, err := eng.Execute("subj", invOf(modifyOp("a0")))
			if tc.wantErr != 0 {
				if err == nil || err.Kind != tc.wantErr {
					t.Fatalf("want err %v, got allowed=%v err=%v", tc.wantErr, allowed, err)
				}
				if !reflect.DeepEqual(before, s.Snapshot()) {
					t.Fatalf("rejected action changed observable state")
				}
				return
			}
			if err != nil || !allowed {
				t.Fatalf("want allowed, got allowed=%v err=%v", allowed, err)
			}
			after := s.Snapshot()
			for _, id := range []InstanceID{"a0", "b1", "b2", "b3"} {
				if after.Instances[id].Version != 2 {
					t.Errorf("instance %s version=%d, want 2", id, after.Instances[id].Version)
				}
			}
			if after.Instances["a0"].Props["p"] != "w" {
				t.Errorf("modify not applied: %v", after.Instances["a0"].Props)
			}
			if after.Clock != before.Clock+1 {
				t.Errorf("clock advanced by %d, want 1", after.Clock-before.Clock)
			}
		})
	}
}

// TestExcludedLinkTypeStopsPropagation 声明沿某链接类型不传播后，
// 该方向的邻居既不检查也不触及。
func TestExcludedLinkTypeStopsPropagation(t *testing.T) {
	s := chainStore()
	counter := NewCountingAuthorizer(allowAll())
	eng := NewEngine(s, counter)
	d := modifyDecl(3, InvisibleDeny, MergeAll)
	d.Cascade.ExcludeLinkTypes = []LinkTypeID{"ab"}
	eng.RegisterAction(d)
	allowed, err := eng.Execute("subj", invOf(modifyOp("a0")))
	if err != nil || !allowed {
		t.Fatalf("want allowed, got allowed=%v err=%v", allowed, err)
	}
	after := s.Snapshot()
	if after.Instances["b1"].Version != 1 {
		t.Errorf("excluded link target was touched: version=%d", after.Instances["b1"].Version)
	}
	if counter.VisibleCalls != 1 || counter.AuthCalls != 1 {
		t.Errorf("checks visible=%d auth=%d, want 1/1 (direct target only)", counter.VisibleCalls, counter.AuthCalls)
	}
}

// TestCycleDedupAndTermination 环上的每个实例只被检查一次，
// 遍历必然终止，且环上实例只被触及一次。
func TestCycleDedupAndTermination(t *testing.T) {
	s := baseStore()
	putA(s, "a0")
	putA(s, "a1")
	putA(s, "a2")
	s.AddLink(Link{Type: "aa", From: "a0", To: "a1"})
	s.AddLink(Link{Type: "aa", From: "a1", To: "a2"})
	s.AddLink(Link{Type: "aa", From: "a2", To: "a0"})
	counter := NewCountingAuthorizer(allowAll())
	eng := NewEngine(s, counter)
	eng.RegisterAction(modifyDecl(5, InvisibleDeny, MergeAll))
	allowed, err := eng.Execute("subj", invOf(modifyOp("a0")))
	if err != nil || !allowed {
		t.Fatalf("want allowed, got allowed=%v err=%v", allowed, err)
	}
	if counter.VisibleCalls != 3 || counter.AuthCalls != 3 {
		t.Errorf("checks visible=%d auth=%d, want 3/3 (each instance once)", counter.VisibleCalls, counter.AuthCalls)
	}
	if len(counter.CheckedInsts) != 3 {
		t.Errorf("checked %d distinct instances, want 3", len(counter.CheckedInsts))
	}
	after := s.Snapshot()
	for _, id := range []InstanceID{"a0", "a1", "a2"} {
		if after.Instances[id].Version != 2 {
			t.Errorf("instance %s version=%d, want exactly one touch", id, after.Instances[id].Version)
		}
	}
}

// grantFunc 构造按实例与层级给结论的授权函数。
func grantFunc(f func(id InstanceID, depth int) bool) Authorizer {
	return AuthorizerFunc{
		VisibleFn:   func(SubjectID, InstanceID) bool { return true },
		AuthorizeFn: func(_ SubjectID, id InstanceID, d int) bool { return f(id, d) },
	}
}

// twoLevelStore 构造 a0 -> b1 的两层图。
func twoLevelStore() *Store {
	s := baseStore()
	putA(s, "a0")
	putB(s, "b1")
	s.AddLink(Link{Type: "ab", From: "a0", To: "b1"})
	return s
}

// TestMergeRules 验证两种合并规则的取舍：
// MergeAll 要求逐层全部放行，MergeAny 只要任一层级放行。
func TestMergeRules(t *testing.T) {
	cases := []struct {
		name    string
		merge   MergeMode
		grant   func(id InstanceID, depth int) bool
		allowed bool
	}{
		{"all-one-level-denies", MergeAll, func(id InstanceID, d int) bool { return id != "b1" }, false},
		{"all-every-level-grants", MergeAll, func(InstanceID, int) bool { return true }, true},
		{"any-single-level-grants", MergeAny, func(id InstanceID, d int) bool { return id == "b1" }, true},
		{"any-no-level-grants", MergeAny, func(InstanceID, int) bool { return false }, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := twoLevelStore()
			eng := NewEngine(s, grantFunc(tc.grant))
			eng.RegisterAction(modifyDecl(1, InvisibleDeny, tc.merge))
			before := s.Snapshot()
			allowed, err := eng.Execute("subj", invOf(modifyOp("a0")))
			if err != nil {
				t.Fatalf("unexpected err: %v", err)
			}
			if allowed != tc.allowed {
				t.Fatalf("allowed=%v, want %v", allowed, tc.allowed)
			}
			if !allowed && !reflect.DeepEqual(before, s.Snapshot()) {
				t.Fatalf("denied action changed observable state")
			}
		})
	}
}

// TestMergeOrderIndependence 同一图以不同链接插入顺序构建，
// 合并结果与最终状态必须一致。
func TestMergeOrderIndependence(t *testing.T) {
	build := func(order []Link) *Store {
		s := baseStore()
		putA(s, "a0")
		putA(s, "a1")
		putA(s, "a2")
		putA(s, "a3")
		for _, l := range order {
			s.AddLink(l)
		}
		return s
	}
	links := []Link{
		{Type: "aa", From: "a0", To: "a1"},
		{Type: "aa", From: "a0", To: "a2"},
		{Type: "aa", From: "a1", To: "a3"},
		{Type: "aa", From: "a2", To: "a3"},
	}
	reversed := []Link{links[3], links[2], links[1], links[0]}
	// 授权结论依赖层级：只有偶数层放行。
	auth := grantFunc(func(_ InstanceID, d int) bool { return d%2 == 0 })

	var outcomes [2]bool
	var snaps [2]State
	for i, order := range [][]Link{links, reversed} {
		s := build(order)
		eng := NewEngine(s, auth)
		eng.RegisterAction(modifyDecl(3, InvisibleDeny, MergeAny))
		allowed, err := eng.Execute("subj", invOf(modifyOp("a0")))
		if err != nil {
			t.Fatalf("order %d: unexpected err %v", i, err)
		}
		outcomes[i] = allowed
		snaps[i] = s.Snapshot()
	}
	if outcomes[0] != outcomes[1] {
		t.Fatalf("outcomes differ by traversal order: %v vs %v", outcomes[0], outcomes[1])
	}
	if !reflect.DeepEqual(snaps[0], snaps[1]) {
		t.Fatalf("final states differ by traversal order")
	}
}

// TestAtomicRollbackAllPhases 在全部拒绝路径上验证全有或全无：
// 实例、链接、审计记录与时钟都不得发生任何可观察变化。
func TestAtomicRollbackAllPhases(t *testing.T) {
	cases := []struct {
		name    string
		setup   func() (*Store, Authorizer, ActionDecl, Invocation)
		wantErr ErrorKind // 0 表示合并规则拒绝（非错误）
	}{
		{
			name: "invalid-params",
			setup: func() (*Store, Authorizer, ActionDecl, Invocation) {
				s := twoLevelStore()
				op := DirectOp{Kind: OpModify, Type: "A", Target: "a0", Props: map[string]string{"nope": "x"}}
				return s, allowAll(), modifyDecl(1, InvisibleDeny, MergeAll), invOf(op)
			},
			wantErr: ErrInvalidParams,
		},
		{
			name: "depth-exceeded",
			setup: func() (*Store, Authorizer, ActionDecl, Invocation) {
				s := chainStore()
				return s, allowAll(), modifyDecl(1, InvisibleDeny, MergeAll), invOf(modifyOp("a0"))
			},
			wantErr: ErrDepthExceeded,
		},
		{
			name: "target-invisible",
			setup: func() (*Store, Authorizer, ActionDecl, Invocation) {
				s := twoLevelStore()
				auth := AuthorizerFunc{
					VisibleFn:   func(_ SubjectID, id InstanceID) bool { return id != "a0" },
					AuthorizeFn: func(SubjectID, InstanceID, int) bool { return true },
				}
				return s, auth, modifyDecl(1, InvisibleDeny, MergeAll), invOf(modifyOp("a0"))
			},
			wantErr: ErrTargetInvisible,
		},
		{
			name: "cascade-invisible-deny",
			setup: func() (*Store, Authorizer, ActionDecl, Invocation) {
				s := twoLevelStore()
				auth := AuthorizerFunc{
					VisibleFn:   func(_ SubjectID, id InstanceID) bool { return id != "b1" },
					AuthorizeFn: func(SubjectID, InstanceID, int) bool { return true },
				}
				return s, auth, modifyDecl(1, InvisibleDeny, MergeAll), invOf(modifyOp("a0"))
			},
			wantErr: ErrCascadeInvisible,
		},
		{
			name: "merge-deny",
			setup: func() (*Store, Authorizer, ActionDecl, Invocation) {
				s := twoLevelStore()
				auth := grantFunc(func(InstanceID, int) bool { return false })
				return s, auth, modifyDecl(1, InvisibleDeny, MergeAll), invOf(modifyOp("a0"))
			},
			wantErr: 0,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, auth, d, inv := tc.setup()
			eng := NewEngine(s, auth)
			eng.RegisterAction(d)
			before := s.Snapshot()
			linksBefore := len(s.Links)
			allowed, err := eng.Execute("subj", inv)
			if allowed {
				t.Fatalf("action unexpectedly allowed")
			}
			if tc.wantErr == 0 && err != nil {
				t.Fatalf("want merge-deny without error, got %v", err)
			}
			if tc.wantErr != 0 && (err == nil || err.Kind != tc.wantErr) {
				t.Fatalf("want err %v, got %v", tc.wantErr, err)
			}
			if !reflect.DeepEqual(before, s.Snapshot()) {
				t.Fatalf("rejected action changed instances/audit/clock")
			}
			if len(s.Links) != linksBefore {
				t.Fatalf("rejected action changed links")
			}
		})
	}
}

// TestErrorPriority 验证错误类别按固定且唯一的优先顺序汇报。
func TestErrorPriority(t *testing.T) {
	t.Run("invalid-params-beats-depth", func(t *testing.T) {
		s := chainStore() // 深度 1 会超界，同时参数不合法
		eng := NewEngine(s, allowAll())
		eng.RegisterAction(modifyDecl(1, InvisibleDeny, MergeAll))
		op := DirectOp{Kind: OpModify, Type: "A", Target: "a0", Props: map[string]string{"bad": "x"}}
		_, err := eng.Execute("subj", invOf(op))
		if err == nil || err.Kind != ErrInvalidParams {
			t.Fatalf("want invalid-params, got %v", err)
		}
	})
	t.Run("depth-beats-target-invisible", func(t *testing.T) {
		s := chainStore()
		auth := AuthorizerFunc{
			VisibleFn:   func(_ SubjectID, id InstanceID) bool { return id != "a0" },
			AuthorizeFn: func(SubjectID, InstanceID, int) bool { return true },
		}
		eng := NewEngine(s, auth)
		eng.RegisterAction(modifyDecl(1, InvisibleDeny, MergeAll))
		_, err := eng.Execute("subj", invOf(modifyOp("a0")))
		if err == nil || err.Kind != ErrDepthExceeded {
			t.Fatalf("want depth-exceeded, got %v", err)
		}
	})
	t.Run("target-invisible-beats-cascade-invisible", func(t *testing.T) {
		s := twoLevelStore()
		auth := AuthorizerFunc{
			VisibleFn:   func(SubjectID, InstanceID) bool { return false },
			AuthorizeFn: func(SubjectID, InstanceID, int) bool { return true },
		}
		eng := NewEngine(s, auth)
		eng.RegisterAction(modifyDecl(1, InvisibleDeny, MergeAll))
		_, err := eng.Execute("subj", invOf(modifyOp("a0")))
		if err == nil || err.Kind != ErrTargetInvisible {
			t.Fatalf("want target-invisible, got %v", err)
		}
	})
}

// skipStore 构造 a0 -> b1（可见）与 a0 -> b2（不可见，含敏感属性值）。
func skipStore() *Store {
	s := baseStore()
	putA(s, "a0")
	putB(s, "b1")
	s.PutInstance(&Instance{ID: "b2", Type: "B", Props: map[string]string{"q": "secret"}, Version: 1})
	s.AddLink(Link{Type: "ab", From: "a0", To: "b1"})
	s.AddLink(Link{Type: "ab", From: "a0", To: "b2"})
	return s
}

func skipAuth() Authorizer {
	return AuthorizerFunc{
		VisibleFn:   func(_ SubjectID, id InstanceID) bool { return id != "b2" },
		AuthorizeFn: func(SubjectID, InstanceID, int) bool { return true },
	}
}

// TestSkipModeRecordsAndEffects 跳过模式下：动作继续执行，
// 不可见实例不被触及，跳过记录与实际效果完全一致，
// 且记录不泄露该实例的任何属性信息。
func TestSkipModeRecordsAndEffects(t *testing.T) {
	s := skipStore()
	eng := NewEngine(s, skipAuth())
	eng.RegisterAction(modifyDecl(1, InvisibleSkip, MergeAll))
	allowed, err := eng.Execute("subj", invOf(modifyOp("a0")))
	if err != nil || !allowed {
		t.Fatalf("want allowed, got allowed=%v err=%v", allowed, err)
	}
	after := s.Snapshot()
	if after.Instances["b1"].Version != 2 {
		t.Errorf("visible cascaded instance not touched: version=%d", after.Instances["b1"].Version)
	}
	skippedInst := after.Instances["b2"]
	if skippedInst.Version != 1 || skippedInst.CascadeMark != 0 || skippedInst.Props["q"] != "secret" {
		t.Errorf("skipped instance was modified: %+v", skippedInst)
	}
	recs := eng.Log()
	if len(recs) != 1 {
		t.Fatalf("want 1 decision record, got %d", len(recs))
	}
	rec := recs[0]
	if len(rec.Skipped) != 1 || rec.Skipped[0].Instance != "b2" {
		t.Fatalf("skip record mismatch: %+v", rec.Skipped)
	}
	if rec.Skipped[0].Reason != "invisible" || rec.Skipped[0].Impact == "" {
		t.Errorf("skip record missing reason/impact: %+v", rec.Skipped[0])
	}
	// 记录不得携带被跳过实例的属性信息。
	if strings.Contains(rec.Skipped[0].Impact, "secret") || strings.Contains(rec.Skipped[0].Reason, "secret") {
		t.Errorf("skip record leaks attribute data: %+v", rec.Skipped[0])
	}
	// 审计记录与判定日志一致。
	audit := s.Audit[len(s.Audit)-1]
	if !reflect.DeepEqual(audit.Skipped, []InstanceID{"b2"}) {
		t.Errorf("audit skipped=%v, want [b2]", audit.Skipped)
	}
	if !reflect.DeepEqual(audit.Touched, []InstanceID{"b1"}) {
		t.Errorf("audit touched=%v, want [b1]", audit.Touched)
	}
}

// TestDenyModeNoLeak 整体拒绝模式下，错误不得泄露不可见实例的存在。
func TestDenyModeNoLeak(t *testing.T) {
	s := skipStore()
	eng := NewEngine(s, skipAuth())
	eng.RegisterAction(modifyDecl(1, InvisibleDeny, MergeAll))
	before := s.Snapshot()
	allowed, err := eng.Execute("subj", invOf(modifyOp("a0")))
	if allowed || err == nil || err.Kind != ErrCascadeInvisible {
		t.Fatalf("want cascade-invisible rejection, got allowed=%v err=%v", allowed, err)
	}
	if err.Target != "" {
		t.Errorf("error carries instance id: %s", err.Target)
	}
	if strings.Contains(err.Error(), "b2") {
		t.Errorf("error message leaks instance identity: %v", err)
	}
	if !reflect.DeepEqual(before, s.Snapshot()) {
		t.Fatalf("rejected action changed observable state")
	}
}

// TestCreateModifyCascadeAtomic 创建、修改与级联影响作为同一整体生效；
// 拒绝时三者全部不生效。
func TestCreateModifyCascadeAtomic(t *testing.T) {
	build := func() (*Store, *Engine) {
		s := twoLevelStore()
		eng := NewEngine(s, allowAll())
		eng.RegisterAction(modifyDecl(1, InvisibleDeny, MergeAll))
		return s, eng
	}
	createOp := DirectOp{Kind: OpCreate, Type: "A", Target: "a9", Props: map[string]string{"p": "x"}}

	s, eng := build()
	allowed, err := eng.Execute("subj", invOf(createOp, modifyOp("a0")))
	if err != nil || !allowed {
		t.Fatalf("want allowed, got allowed=%v err=%v", allowed, err)
	}
	after := s.Snapshot()
	if after.Instances["a9"].Version != 1 || after.Instances["a9"].Props["p"] != "x" {
		t.Errorf("create not applied: %+v", after.Instances["a9"])
	}
	if after.Instances["a0"].Props["p"] != "w" || after.Instances["b1"].Version != 2 {
		t.Errorf("modify/cascade not applied atomically")
	}
	if after.Instances["a9"].CascadeMark != after.Clock {
		t.Errorf("created instance mark=%d, want commit clock %d", after.Instances["a9"].CascadeMark, after.Clock)
	}

	// 拒绝变体：新建实例不被授权（MergeAll），整体不生效。
	s2 := twoLevelStore()
	denyNew := AuthorizerFunc{
		VisibleFn:   func(SubjectID, InstanceID) bool { return true },
		AuthorizeFn: func(_ SubjectID, id InstanceID, _ int) bool { return id != "a9" },
	}
	eng2 := NewEngine(s2, denyNew)
	eng2.RegisterAction(modifyDecl(1, InvisibleDeny, MergeAll))
	before := s2.Snapshot()
	allowed, err = eng2.Execute("subj", invOf(createOp, modifyOp("a0")))
	if err != nil || allowed {
		t.Fatalf("want merge-deny, got allowed=%v err=%v", allowed, err)
	}
	if !reflect.DeepEqual(before, s2.Snapshot()) {
		t.Fatalf("partial effects leaked: create/modify/cascade not rolled back")
	}
}

// TestDecisionLogContents 判定日志完整记录输入、输出与传播路径依据。
func TestDecisionLogContents(t *testing.T) {
	s := chainStore()
	eng := NewEngine(s, allowAll())
	eng.RegisterAction(modifyDecl(3, InvisibleDeny, MergeAll))
	inv := invOf(modifyOp("a0"))
	if _, err := eng.Execute("subj", inv); err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	recs := eng.Log()
	if len(recs) != 1 {
		t.Fatalf("want 1 record, got %d", len(recs))
	}
	rec := recs[0]
	if !reflect.DeepEqual(rec.Inv, inv) {
		t.Errorf("logged input mismatch: %+v", rec.Inv)
	}
	if !rec.Allowed || rec.Err != 0 {
		t.Errorf("logged output mismatch: allowed=%v err=%v", rec.Allowed, rec.Err)
	}
	wantPath := []PathEntry{
		{Instance: "a0", Depth: 0},
		{Instance: "b1", Depth: 1, ViaLink: "ab"},
		{Instance: "b2", Depth: 2, ViaLink: "bb"},
		{Instance: "b3", Depth: 3, ViaLink: "bb"},
	}
	if !reflect.DeepEqual(rec.Path, wantPath) {
		t.Errorf("logged path mismatch:\n got %+v\nwant %+v", rec.Path, wantPath)
	}
	if rec.CheckCount != 8 { // 4 次可见性 + 4 次授权
		t.Errorf("check count=%d, want 8", rec.CheckCount)
	}
}

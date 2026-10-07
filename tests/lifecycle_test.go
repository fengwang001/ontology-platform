package ontology_test

import (
	"fmt"
	"testing"

	"ontology/ontology"
)

// mustType 在测试中创建对象类型，失败即终止用例。
func mustType(t *testing.T, name string, stages []string, allowed [][2]string, terminal []string) *ontology.ObjectType {
	t.Helper()
	ot, err := ontology.NewObjectType(name, stages, allowed, terminal)
	if err != nil {
		t.Fatalf("NewObjectType(%s) 失败: %v", name, err)
	}
	return ot
}

func failReason(err error) string {
	if err == nil {
		return "ok"
	}
	if le, ok := ontology.AsLifecycleError(err); ok {
		return string(le.Code)
	}
	return err.Error()
}

// TestSelfTransitionEntryAlwaysFires 覆盖自转移语义：
// 进入钩子始终触发；具体转移钩子仅在显式声明 (s,s) 时触发，不因阶段相同而默认跳过/触发。
func TestSelfTransitionEntryAlwaysFires(t *testing.T) {
	ot := mustType(t, "ticket",
		[]string{"draft"},
		[][2]string{{"draft", "draft"}}, // 必须显式声明自环，自转移才合法
		nil)
	reg := ot.Registry()

	var got []string
	reg.RegisterEntry("draft", ontology.Hook{
		ID: "entry-draft", Semantic: ontology.CommitImmediately,
		Check: func(*ontology.TransitionContext) error { got = append(got, "entry-draft"); return nil },
	})

	mgr := ontology.NewManager()
	in, err := mgr.CreateInstance(ot, "t1", "draft")
	if err != nil {
		t.Fatal(err)
	}

	// 情形 A：未声明 (draft,draft) 具体钩子 —— 只应触发进入钩子。
	res, err := mgr.Transition("t1", "draft")
	if err != nil {
		t.Fatalf("自转移被意外拒绝: %v", err)
	}
	fmt.Printf("输入: 自转移 draft->draft（无具体钩子声明）\n实际输出: fired=%v 当前阶段=%s\n判定依据: 自转移合法且仅进入钩子应命中\n",
		res.Fired, in.Current())
	if len(res.Fired) != 1 || res.Fired[0] != "entry-draft" {
		t.Fatalf("期望仅触发 entry-draft，实际 %v", res.Fired)
	}
	if in.Current() != "draft" || len(in.History()) != 1 {
		t.Fatalf("自转移后阶段/轨迹异常: %s %v", in.Current(), in.History())
	}

	// 情形 B：显式注册 (draft,draft) 具体钩子 —— 两类都触发，且具体钩子在先。
	reg.RegisterTransition("draft", "draft", ontology.Hook{
		ID: "edge-draft-draft", Semantic: ontology.CommitImmediately,
		Check: func(*ontology.TransitionContext) error { got = append(got, "edge-draft-draft"); return nil },
	})
	res, err = mgr.Transition("t1", "draft")
	if err != nil {
		t.Fatalf("第二次自转移被意外拒绝: %v", err)
	}
	fmt.Printf("输入: 自转移 draft->draft（已声明具体钩子）\n实际输出: fired=%v\n判定依据: 两类钩子同时命中，具体转移钩子先于进入钩子\n",
		res.Fired)
	if len(res.Fired) != 2 || res.Fired[0] != "edge-draft-draft" || res.Fired[1] != "entry-draft" {
		t.Fatalf("期望 [edge-draft-draft entry-draft]，实际 %v", res.Fired)
	}
}

// TestTerminalBlocksHooks 覆盖终态限制优先于钩子校验：钩子一个都不得触发。
func TestTerminalBlocksHooks(t *testing.T) {
	ot := mustType(t, "order",
		[]string{"open", "closed"},
		[][2]string{{"open", "closed"}},
		[]string{"closed"})
	reg := ot.Registry()

	edgeFired, entryFired := 0, 0
	// 即使注册了 closed->closed 的具体钩子与进入钩子，终态拦截也必须使它们静默。
	reg.RegisterTransition("closed", "closed", ontology.Hook{
		ID: "edge-closed-self", Semantic: ontology.CommitImmediately,
		Check: func(*ontology.TransitionContext) error { edgeFired++; return fmt.Errorf("不应被调用") },
	})
	reg.RegisterEntry("closed", ontology.Hook{
		ID: "entry-closed", Semantic: ontology.CommitImmediately,
		Check: func(*ontology.TransitionContext) error { entryFired++; return nil },
	})

	mgr := ontology.NewManager()
	in, _ := mgr.CreateInstance(ot, "o1", "open")
	if _, err := mgr.Transition("o1", "closed"); err != nil {
		t.Fatal(err)
	}
	// open->closed 合法进入终态时进入钩子会正常触发；重置计数，
	// 此后从终态发起的任何转移都不得再触发钩子。
	edgeFired, entryFired = 0, 0

	for _, target := range []string{"closed", "open"} {
		_, err := mgr.Transition("o1", target)
		le, _ := ontology.AsLifecycleError(err)
		fmt.Printf("输入: 终态实例 closed -> %s\n实际输出: 错误=%s\n判定依据: 终态不可转出（含转到自身），优先级高于钩子与关系校验\n",
			target, failReason(err))
		if le == nil || le.Code != ontology.ErrorTerminal {
			t.Fatalf("closed -> %s 期望 terminal_state，实际 %v", target, err)
		}
	}
	if edgeFired != 0 || entryFired != 0 {
		t.Fatalf("终态转移触发了钩子: edge=%d entry=%d", edgeFired, entryFired)
	}
	if in.Current() != "closed" || len(in.History()) != 1 {
		t.Fatalf("被拒绝转移改变了状态: %s %v", in.Current(), in.History())
	}
}

// TestHookOrderingEdgeBeforeEntry 覆盖具体转移钩子先于进入钩子，且均按注册序。
func TestHookOrderingEdgeBeforeEntry(t *testing.T) {
	ot := mustType(t, "doc",
		[]string{"a", "b"},
		[][2]string{{"a", "b"}}, nil)
	reg := ot.Registry()

	var order []string
	addEdge := func(id string) {
		reg.RegisterTransition("a", "b", ontology.Hook{
			ID: id, Semantic: ontology.CommitImmediately,
			Check: func(*ontology.TransitionContext) error { order = append(order, id); return nil },
		})
	}
	addEntry := func(id string) {
		reg.RegisterEntry("b", ontology.Hook{
			ID: id, Semantic: ontology.CommitImmediately,
			Check: func(*ontology.TransitionContext) error { order = append(order, id); return nil },
		})
	}
	addEntry("entry-b-1") // 交错注册，验证触发顺序不取决于注册位置
	addEdge("edge-ab-1")
	addEntry("entry-b-2")
	addEdge("edge-ab-2")

	mgr := ontology.NewManager()
	if _, err := mgr.CreateInstance(ot, "d1", "a"); err != nil {
		t.Fatal(err)
	}
	res, err := mgr.Transition("d1", "b")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"edge-ab-1", "edge-ab-2", "entry-b-1", "entry-b-2"}
	fmt.Printf("输入: a->b（交错注册 4 个钩子）\n实际输出: fired=%v\n判定依据: 具体转移钩子整体先于进入钩子，各类内部按注册序\n",
		res.Fired)
	if len(order) != 4 {
		t.Fatalf("触发数量异常: %v", order)
	}
	for i := range want {
		if order[i] != want[i] {
			t.Fatalf("触发顺序错误: 期望 %v，实际 %v", want, order)
		}
	}
}

// TestHookFailureRollbackSemantics 覆盖失败回滚：阶段不变 + 两种提交语义。
func TestHookFailureRollbackSemantics(t *testing.T) {
	ot := mustType(t, "pay",
		[]string{"init", "paid"},
		[][2]string{{"init", "paid"}}, nil)
	reg := ot.Registry()

	var log_ []string
	reg.RegisterTransition("init", "paid", ontology.Hook{
		ID: "tx-compensated", Semantic: ontology.CommitOnSuccess,
		Check: func(*ontology.TransitionContext) error {
			log_ = append(log_, "tx-compensated+")
			return nil
		},
		Compensate: func(*ontology.TransitionContext) { log_ = append(log_, "tx-compensated-") },
	})
	reg.RegisterEntry("paid", ontology.Hook{
		ID: "entry-immediate", Semantic: ontology.CommitImmediately,
		Check:      func(*ontology.TransitionContext) error { log_ = append(log_, "entry-immediate+"); return nil },
		Compensate: func(*ontology.TransitionContext) { log_ = append(log_, "SHOULD-NOT-COMPENSATE") },
	})
	reg.RegisterEntry("paid", ontology.Hook{
		ID: "entry-fails", Semantic: ontology.CommitImmediately,
		Check: func(*ontology.TransitionContext) error { return fmt.Errorf("拒绝进入 paid") },
	})

	mgr := ontology.NewManager()
	in, _ := mgr.CreateInstance(ot, "p1", "init")
	_, err := mgr.Transition("p1", "paid")
	le, _ := ontology.AsLifecycleError(err)
	fmt.Printf("输入: init->paid，末位钩子失败\n实际输出: 错误=%s hook=%s 阶段=%s 副作用日志=%v\n判定依据: 阶段回退；CommitOnSuccess 逆序补偿，CommitImmediately 保留\n",
		failReason(err), le.HookID, in.Current(), log_)
	if le == nil || le.Code != ontology.ErrorHookFailed || le.HookID != "entry-fails" {
		t.Fatalf("期望 hook_failed/entry-fails，实际 %v", err)
	}
	if in.Current() != "init" || len(in.History()) != 0 {
		t.Fatalf("失败转移后状态必须保持 init，实际 %s %v", in.Current(), in.History())
	}
	wantLog := []string{"tx-compensated+", "entry-immediate+", "tx-compensated-"}
	if len(log_) != len(wantLog) {
		t.Fatalf("副作用提交语义错误: %v", log_)
	}
	for i := range wantLog {
		if log_[i] != wantLog[i] {
			t.Fatalf("副作用顺序/内容错误: 期望 %v，实际 %v", wantLog, log_)
		}
	}
}

// TestErrorPriority 覆盖四类拒绝原因只报第一个命中者的固定次序。
func TestErrorPriority(t *testing.T) {
	ot := mustType(t, "prio",
		[]string{"s1", "s2", "term"},
		[][2]string{{"s1", "s2"}, {"s2", "term"}},
		[]string{"term"})
	// 在任何合法目标上都挂一个必失败钩子，用于验证前面三类原因优先时钩子不得执行。
	ot.Registry().RegisterEntry("s2", ontology.Hook{
		ID: "always-fail", Semantic: ontology.CommitImmediately,
		Check: func(*ontology.TransitionContext) error { return fmt.Errorf("boom") },
	})
	mgr := ontology.NewManager()
	_, _ = mgr.CreateInstance(ot, "x", "s1")

	cases := []struct {
		name   string
		id     string
		to     string
		reason ontology.ErrorCode
		basis  string
	}{
		{"实例不存在", "ghost", "s2", ontology.ErrorInvalidArgument, "参数非法优先级最高"},
		{"目标阶段未声明", "x", "nope", ontology.ErrorInvalidArgument, "参数非法先于一切"},
	}
	for _, tc := range cases {
		_, err := mgr.Transition(tc.id, tc.to)
		le, _ := ontology.AsLifecycleError(err)
		fmt.Printf("输入: %s（%s -> %s）\n实际输出: %s\n判定依据: %s\n", tc.name, tc.id, tc.to, failReason(err), tc.basis)
		if le == nil || le.Code != tc.reason {
			t.Fatalf("%s: 期望 %s，实际 %v", tc.name, tc.reason, err)
		}
	}

	// 先合法走到终态 term，再请求转出，应为 terminal（即使目标未声明也要先判实例/阶段，
	// 而这里目标已声明，故终态优先于关系）。
	ot.Registry().RegisterEntry("term", ontology.Hook{ID: "term-entry",
		Check: func(*ontology.TransitionContext) error { return nil }})
	if _, err := mgr.Transition("x", "s2"); err == nil {
		t.Fatal("s1->s2 应被 always-fail 钩子拒绝")
	}
	// 移除失败影响：换一个干净实例走完全程到终态。
	_, _ = mgr.CreateInstance(ot, "y", "s2")
	if _, err := mgr.Transition("y", "term"); err != nil {
		t.Fatalf("s2->term: %v", err)
	}
	_, err := mgr.Transition("y", "s2") // 关系未声明，但终态优先
	fmt.Printf("输入: 终态 term -> s2（关系也未声明）\n实际输出: %s\n判定依据: 终态原因先于关系原因\n", failReason(err))
	if le, _ := ontology.AsLifecycleError(err); le == nil || le.Code != ontology.ErrorTerminal {
		t.Fatalf("期望 terminal_state，实际 %v", err)
	}

	// 关系未允许优先于钩子失败：s1->term 未声明，钩子（挂在 term 上）不得执行。
	_, _ = mgr.CreateInstance(ot, "z", "s1")
	_, err = mgr.Transition("z", "term")
	fmt.Printf("输入: s1 -> term（关系未声明，term 有进入钩子）\n实际输出: %s\n判定依据: 关系未允许先于钩子校验\n", failReason(err))
	if le, _ := ontology.AsLifecycleError(err); le == nil || le.Code != ontology.ErrorTransitionNotAllowed {
		t.Fatalf("期望 transition_not_allowed，实际 %v", err)
	}
}

package ontology

import (
	"sync"
	"testing"
)

// spendAction 构造一个“消费”动作：前置只检查金额为正，
// 余额非负由后置条件保证——用于制造“前置通过、后置失败”的路径。
// extraFailingPost 为 true 时追加一个必然失败的后置条件，
// 用于验证后置只返回决定性条件。
func spendAction(extraFailingPost bool) ActionType {
	posts := []PostCondition{
		{
			ID: "no-negative-balance",
			Eval: func(in PostInput) bool {
				obj, ok := in.State.GetObject(ObjectID(in.Params["target"].(string)))
				if !ok {
					return false
				}
				return obj.Props["balance"].(int64) >= 0
			},
		},
	}
	if extraFailingPost {
		posts = append(posts, PostCondition{
			ID:   "always-fails",
			Eval: func(in PostInput) bool { return false },
		})
	}
	return ActionType{
		ID: "spend",
		Preconditions: []PreCondition{
			{
				ID:   "positive-amount",
				Eval: func(in PreInput) bool { return in.Params["amount"].(int64) > 0 },
			},
		},
		Postconditions: posts,
		Apply: func(ctx *ApplyContext) error {
			id := ObjectID(ctx.Params["target"].(string))
			obj, _ := ctx.State.GetObject(id)
			return ctx.Plan.Update(id, map[string]any{
				"balance": obj.Props["balance"].(int64) - ctx.Params["amount"].(int64),
			})
		},
	}
}

func spendCall(id string, target ObjectID, amount int64) Call {
	return Call{
		ID:         id,
		ActionType: "spend",
		Params:     map[string]any{"target": string(target), "amount": amount},
		Targets:    []ObjectID{target},
	}
}

// 前置失败：返回全部不通过条件；状态、版本号、历史序列完全不变。
func TestPreFailureReturnsAllAndKeepsState(t *testing.T) {
	store := NewStore()
	reg := NewRegistry()
	at := transferAction()
	at.Preconditions = append(at.Preconditions, PreCondition{
		ID:   "max-amount",
		Eval: func(in PreInput) bool { return in.Params["amount"].(int64) <= 200 },
	})
	mustRegister(t, reg, at)
	seedAccounts(store, 2, 100)
	exec := NewExecutor(store, reg)

	// 余额不足（sufficient-funds 失败）且超过限额（max-amount 失败）。
	res := exec.Execute(transferCall("c1", accountID(0), accountID(1), 500))
	if res.Status != StatusRejected || res.Reject.Category != RejectPrecondition {
		t.Fatalf("expected precondition rejection, got %+v", res)
	}
	want := []string{"sufficient-funds", "max-amount"}
	if len(res.Reject.FailedPreconditions) != len(want) {
		t.Fatalf("expected all failed preconditions %v, got %v", want, res.Reject.FailedPreconditions)
	}
	for i, id := range want {
		if res.Reject.FailedPreconditions[i] != id {
			t.Fatalf("failed preconditions = %v, want %v", res.Reject.FailedPreconditions, want)
		}
	}
	// 状态不变性：余额、版本号、提交序号、历史序列均无变化。
	if got := balanceOf(t, store, accountID(0)); got != 100 {
		t.Fatalf("balance changed after pre failure: %d", got)
	}
	obj, _ := store.GetObject(accountID(0))
	if obj.Version != 0 {
		t.Fatalf("version consumed by rejected call: %d", obj.Version)
	}
	if store.CommitSeq() != 0 {
		t.Fatalf("commit seq consumed by rejected call: %d", store.CommitSeq())
	}
	if len(store.ObjectHistory(accountID(0))) != 0 {
		t.Fatalf("rejected call entered object history")
	}
	if len(store.ActionHistory("transfer")) != 0 {
		t.Fatalf("rejected call entered action-type history")
	}
	// 前置失败不进入失败轨迹，但校验记录必须留存。
	if len(store.FailureTrail()) != 0 {
		t.Fatalf("pre failure must not enter failure trail")
	}
	log := store.ValidationLog()
	if len(log) != 1 || log[0].Phase != PhasePre || log[0].PassedAll {
		t.Fatalf("expected one failed pre validation record, got %+v", log)
	}
	if len(log[0].Outcomes) != 3 {
		t.Fatalf("pre validation must record all condition outcomes, got %+v", log[0].Outcomes)
	}
}

// 前置通过、后置失败：写入计划整体放弃，效果与从未尝试等价；
// 失败进入独立失败轨迹；只返回决定性条件，不穷举其余后置条件。
func TestPostFailureDiscardsPlan(t *testing.T) {
	store := NewStore()
	reg := NewRegistry()
	mustRegister(t, reg, spendAction(true))
	seedAccounts(store, 1, 100)
	exec := NewExecutor(store, reg)

	// 余额 100 消费 150：前置通过（金额>0），计划余额 -50，后置失败。
	res := exec.Execute(spendCall("c1", accountID(0), 150))
	if res.Status != StatusRejected || res.Reject.Category != RejectPostcondition {
		t.Fatalf("expected postcondition rejection, got %+v", res)
	}
	// 不对称性：只返回决定性条件，不报告 always-fails。
	if res.Reject.DecisivePostcondition != "no-negative-balance" {
		t.Fatalf("decisive postcondition = %q, want no-negative-balance", res.Reject.DecisivePostcondition)
	}
	// 效果与从未尝试等价。
	if got := balanceOf(t, store, accountID(0)); got != 100 {
		t.Fatalf("balance changed after post failure: %d", got)
	}
	obj, _ := store.GetObject(accountID(0))
	if obj.Version != 0 {
		t.Fatalf("version consumed by post-failed call: %d", obj.Version)
	}
	if store.CommitSeq() != 0 {
		t.Fatalf("commit seq consumed by post-failed call")
	}
	if len(store.ObjectHistory(accountID(0))) != 0 || len(store.ActionHistory("spend")) != 0 {
		t.Fatalf("post-failed call entered history sequences")
	}
	// 失败轨迹：允许且必须独立于对象状态。
	trail := store.FailureTrail()
	if len(trail) != 1 || trail[0].Category != RejectPostcondition || trail[0].CallID != "c1" {
		t.Fatalf("expected one postcondition failure record, got %+v", trail)
	}
	// 后置校验记录只包含到决定性条件为止的前缀（不穷举）。
	log := store.ValidationLog()
	if len(log) != 2 {
		t.Fatalf("expected pre+post validation records, got %d", len(log))
	}
	post := log[1]
	if post.Phase != PhasePost || post.PassedAll {
		t.Fatalf("post validation record malformed: %+v", post)
	}
	if len(post.Outcomes) != 1 || post.Outcomes[0].ID != "no-negative-balance" || post.Outcomes[0].Passed {
		t.Fatalf("post outcomes must stop at decisive condition, got %+v", post.Outcomes)
	}
}

// 拒绝不消耗任何版本号或序号：混合多类失败后，成功调用的
// 版本号与各类序号必须从初始值连续增长、无空洞。
func TestRejectionConsumesNoVersionsOrSequences(t *testing.T) {
	store := NewStore()
	reg := NewRegistry()
	mustRegister(t, reg, spendAction(false))
	seedAccounts(store, 1, 100)
	exec := NewExecutor(store, reg)

	exec.Execute(spendCall("f1", accountID(0), -1))  // 前置失败
	exec.Execute(spendCall("f2", accountID(0), 500)) // 后置失败
	exec.Execute(Call{ID: "f3", ActionType: "spend", // 目标缺失（并发撤销类别）
		Params: map[string]any{"target": "ghost", "amount": int64(1)}, Targets: []ObjectID{"ghost"}})

	res := exec.Execute(spendCall("ok", accountID(0), 30))
	if !res.Accepted() {
		t.Fatalf("expected acceptance, got %+v", res)
	}
	obj, _ := store.GetObject(accountID(0))
	if obj.Version != 1 {
		t.Fatalf("object version = %d, want 1 (rejections must not consume versions)", obj.Version)
	}
	if res.CommitSeq != 1 {
		t.Fatalf("commit seq = %d, want 1", res.CommitSeq)
	}
	hist := store.ObjectHistory(accountID(0))
	if len(hist) != 1 || hist[0].Seq != 1 || hist[0].CallID != "ok" {
		t.Fatalf("object history = %+v, want single accepted entry", hist)
	}
	ah := store.ActionHistory("spend")
	if len(ah) != 1 || ah[0].Seq != 1 {
		t.Fatalf("action history = %+v, want single accepted entry", ah)
	}
}

// 后置阶段不得重新评估前置条件：计划在目标对象上产生的写入使
// 前置条件在计划状态下不再成立，调用仍必须被接受。
func TestPreconditionNotReevaluatedInPostPhase(t *testing.T) {
	store := NewStore()
	reg := NewRegistry()
	at := ActionType{
		ID: "increment-capped",
		Preconditions: []PreCondition{
			{
				ID: "below-cap",
				Eval: func(in PreInput) bool {
					obj, _ := in.State.GetObject("counter")
					return obj.Props["n"].(int64) < 10
				},
			},
		},
		Postconditions: []PostCondition{
			{
				ID: "non-negative",
				Eval: func(in PostInput) bool {
					obj, _ := in.State.GetObject("counter")
					return obj.Props["n"].(int64) >= 0
				},
			},
		},
		Apply: func(ctx *ApplyContext) error {
			obj, _ := ctx.State.GetObject("counter")
			return ctx.Plan.Update("counter", map[string]any{"n": obj.Props["n"].(int64) + 1})
		},
	}
	mustRegister(t, reg, at)
	store.Seed(Object{ID: "counter", Type: "counter", Props: map[string]any{"n": int64(9)}})
	exec := NewExecutor(store, reg)

	// 提交状态 n=9 通过前置；计划状态 n=10 使 below-cap 不再成立。
	// 若后置阶段重新评估前置条件，本调用会被错误拒绝。
	res := exec.Execute(Call{ID: "inc", ActionType: "increment-capped", Targets: []ObjectID{"counter"}})
	if !res.Accepted() {
		t.Fatalf("precondition must not be re-evaluated in post phase, got %+v", res.Reject)
	}
	obj, _ := store.GetObject("counter")
	if obj.Props["n"].(int64) != 10 {
		t.Fatalf("counter = %v, want 10", obj.Props["n"])
	}
}

// 多对象写入的统一快照视角：后置校验必须看到全部对象的最终计划，
// 且同一对象被后续步骤覆盖时只能看到最终值。
func TestPostValidationSeesUnifiedFinalPlan(t *testing.T) {
	store := NewStore()
	reg := NewRegistry()
	at := ActionType{
		ID: "rebalance",
		Postconditions: []PostCondition{
			{
				// 不变量：两账户计划余额之和必须保持 100。
				ID: "sum-preserved",
				Eval: func(in PostInput) bool {
					a, _ := in.State.GetObject("a")
					b, _ := in.State.GetObject("b")
					return a.Props["v"].(int64)+b.Props["v"].(int64) == 100
				},
			},
			{
				// 同一对象的中间写入（v=5）不可见，只能看到最终值 60。
				ID: "final-write-visible",
				Eval: func(in PostInput) bool {
					a, _ := in.State.GetObject("a")
					return a.Props["v"].(int64) == 60
				},
			},
		},
		Apply: func(ctx *ApplyContext) error {
			// 中间写入：若后置看到中间状态，sum-preserved 会失败（5+0 != 100）。
			if err := ctx.Plan.Update("a", map[string]any{"v": int64(5)}); err != nil {
				return err
			}
			// 后续步骤覆盖同一对象，并补齐另一对象。
			if err := ctx.Plan.Update("a", map[string]any{"v": int64(60)}); err != nil {
				return err
			}
			return ctx.Plan.Update("b", map[string]any{"v": int64(40)})
		},
	}
	mustRegister(t, reg, at)
	store.Seed(Object{ID: "a", Type: "acct", Props: map[string]any{"v": int64(100)}})
	store.Seed(Object{ID: "b", Type: "acct", Props: map[string]any{"v": int64(0)}})
	exec := NewExecutor(store, reg)

	res := exec.Execute(Call{ID: "r1", ActionType: "rebalance", Targets: []ObjectID{"a", "b"}})
	if !res.Accepted() {
		t.Fatalf("post validation must see unified final plan, got %+v", res.Reject)
	}
	a, _ := store.GetObject("a")
	b, _ := store.GetObject("b")
	if a.Props["v"].(int64) != 60 || b.Props["v"].(int64) != 40 {
		t.Fatalf("final state = (%v,%v), want (60,40)", a.Props["v"], b.Props["v"])
	}
}

// 目标对象在执行期间被并发撤销：提交必须失败且类别为并发撤销，
// 不产生任何状态变化。
func TestConcurrentRevocationDuringExecution(t *testing.T) {
	store := NewStore()
	reg := NewRegistry()
	started := make(chan struct{})
	release := make(chan struct{})
	slow := ActionType{
		ID: "slow-update",
		Apply: func(ctx *ApplyContext) error {
			obj, _ := ctx.State.GetObject("victim")
			close(started)
			<-release
			return ctx.Plan.Update("victim", map[string]any{"v": obj.Props["v"].(int64) + 1})
		},
	}
	deleter := ActionType{
		ID: "delete-object",
		Apply: func(ctx *ApplyContext) error {
			return ctx.Plan.Delete(ObjectID(ctx.Params["target"].(string)))
		},
	}
	mustRegister(t, reg, slow)
	mustRegister(t, reg, deleter)
	store.Seed(Object{ID: "victim", Type: "doc", Props: map[string]any{"v": int64(1)}})
	exec := NewExecutor(store, reg)

	var res Result
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		res = exec.Execute(Call{ID: "slow", ActionType: "slow-update", Targets: []ObjectID{"victim"}})
	}()
	<-started
	// 并发撤销目标对象。
	del := exec.Execute(Call{ID: "del", ActionType: "delete-object",
		Params: map[string]any{"target": "victim"}, Targets: []ObjectID{"victim"}})
	if !del.Accepted() {
		t.Fatalf("delete should be accepted, got %+v", del.Reject)
	}
	close(release)
	wg.Wait()

	if res.Status != StatusRejected || res.Reject.Category != RejectConcurrentInvalidation {
		t.Fatalf("expected concurrent invalidation, got %+v", res)
	}
	if _, ok := store.GetObject("victim"); ok {
		t.Fatalf("victim should remain deleted")
	}
	// 只有删除一次被接受。
	if store.CommitSeq() != 1 {
		t.Fatalf("commit seq = %d, want 1", store.CommitSeq())
	}
	trail := store.FailureTrail()
	if len(trail) != 1 || trail[0].Category != RejectConcurrentInvalidation {
		t.Fatalf("expected one concurrent-invalidation failure record, got %+v", trail)
	}
}

// 目标缺失与前置失败的相对优先级必须稳定：本实现固定为
// 目标存在性检查在前，相同输入多次执行结果一致。
func TestMissingTargetPriorityIsStable(t *testing.T) {
	store := NewStore()
	reg := NewRegistry()
	mustRegister(t, reg, spendAction(false))
	exec := NewExecutor(store, reg)

	call := Call{
		ID: "x", ActionType: "spend",
		// 金额为负：若评估前置条件必然失败；但目标同样缺失。
		Params:  map[string]any{"target": "ghost", "amount": int64(-1)},
		Targets: []ObjectID{"ghost"},
	}
	for i := 0; i < 5; i++ {
		res := exec.Execute(call)
		if res.Status != StatusRejected || res.Reject.Category != RejectConcurrentInvalidation {
			t.Fatalf("iteration %d: expected stable concurrent-invalidation, got %+v", i, res.Reject)
		}
	}
}

// 链接参与前置校验与并发控制：前置基于已提交链接状态判断；
// 并发的链接写入会使基于旧链接代际的提交失败。
func TestLinkValidationAndConcurrentLinkConflict(t *testing.T) {
	store := NewStore()
	reg := NewRegistry()
	started := make(chan struct{})
	release := make(chan struct{})
	linker := ActionType{
		ID: "link-once",
		Preconditions: []PreCondition{
			{
				ID: "no-outgoing-link",
				Eval: func(in PreInput) bool {
					return len(in.State.LinksFrom("a")) == 0
				},
			},
		},
		Postconditions: []PostCondition{
			{
				ID: "link-created",
				Eval: func(in PostInput) bool {
					return len(in.State.LinksFrom("a")) == 1
				},
			},
		},
		Apply: func(ctx *ApplyContext) error {
			close(started)
			<-release
			return ctx.Plan.PutLink("l1", "owns", "a", "b")
		},
	}
	fastLinker := ActionType{
		ID: "link-fast",
		Apply: func(ctx *ApplyContext) error {
			return ctx.Plan.PutLink("l2", "owns", "a", "c")
		},
	}
	mustRegister(t, reg, linker)
	mustRegister(t, reg, fastLinker)
	store.Seed(Object{ID: "a", Type: "node", Props: map[string]any{}})
	store.Seed(Object{ID: "b", Type: "node", Props: map[string]any{}})
	store.Seed(Object{ID: "c", Type: "node", Props: map[string]any{}})
	exec := NewExecutor(store, reg)

	var res Result
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		res = exec.Execute(Call{ID: "slow-link", ActionType: "link-once", Targets: []ObjectID{"a"}})
	}()
	<-started
	fast := exec.Execute(Call{ID: "fast-link", ActionType: "link-fast", Targets: []ObjectID{"a"}})
	if !fast.Accepted() {
		t.Fatalf("fast link should be accepted, got %+v", fast.Reject)
	}
	close(release)
	wg.Wait()
	// 慢调用的前置基于旧链接代际通过，但提交时链接状态已变。
	if res.Status != StatusRejected || res.Reject.Category != RejectConcurrentInvalidation {
		t.Fatalf("expected concurrent invalidation on link conflict, got %+v", res)
	}
}

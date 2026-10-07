package lifecycle

import (
	"sync"
	"testing"
)

// 1. 单实例迁移前置条件：满足放行、不满足拒绝且无副作用。
func TestPreconditionDecision(t *testing.T) {
	st := seedStore(inst("o1", "order", "created", nil))
	eng := NewEngine(testSchema(), st, DiscardLogger{})

	res, _ := eng.Batch([]Op{Fire("o1", "pay")})
	if res.Committed || res.Outcomes[0].Err.Code != CodePrecondition {
		t.Fatalf("want precondition reject, got %+v", res.Outcomes[0].Err)
	}
	if got := st.GetInstance("o1").State; got != "created" {
		t.Fatalf("被拒后状态被改动: %s", got)
	}

	res, _ = eng.Batch([]Op{SetAttr("o1", "paid", true)})
	if !res.Committed {
		t.Fatal("setattr failed")
	}
	res, _ = eng.Batch([]Op{Fire("o1", "pay")})
	if !res.Committed {
		t.Fatalf("pay 应成功: %+v", res.Outcomes[0].Err)
	}
	if got := st.GetInstance("o1").State; got != "paid" {
		t.Fatalf("state=%s", got)
	}
}

// 2. 未声明规则与源状态不匹配 -> CodeUnknown。
func TestUnknownTransition(t *testing.T) {
	st := seedStore(inst("o1", "order", "created", nil))
	eng := NewEngine(testSchema(), st, DiscardLogger{})
	res, _ := eng.Batch([]Op{Fire("o1", "nope")})
	if res.Committed || res.Outcomes[0].Err.Code != CodeUnknown {
		t.Fatalf("want unknown, got %+v", res.Outcomes[0].Err)
	}
	res, _ = eng.Batch([]Op{Fire("o1", "close")})
	if res.Committed || res.Outcomes[0].Err.Code != CodeUnknown {
		t.Fatalf("want unknown for wrong source state, got %+v", res.Outcomes[0].Err)
	}
}

// 3. 互斥迁移：同一处理单元中按调用方声明顺序放行靠前的一条。
func TestMutexPriority(t *testing.T) {
	st := seedStore(inst("o1", "order", "created", map[string]AttrValue{"paid": true}))
	eng := NewEngine(testSchema(), st, DiscardLogger{})

	res, _ := eng.Batch([]Op{Fire("o1", "pay"), Fire("o1", "ship2")})
	if res.Committed {
		t.Fatal("互斥批次不应提交")
	}
	if res.Outcomes[1].Err == nil || res.Outcomes[1].Err.Code != CodeMutex {
		t.Fatalf("want mutex reject for ship2, got %+v", res.Outcomes[1].Err)
	}
	if got := st.GetInstance("o1").State; got != "created" {
		t.Fatal("拒绝批次不得改状态")
	}

	res, _ = eng.Batch([]Op{Fire("o1", "ship2"), Fire("o1", "pay")})
	if res.Committed {
		t.Fatal("互斥批次不应提交")
	}
	if res.Outcomes[0].Err != nil {
		t.Fatalf("ship2 应被放行: %+v", res.Outcomes[0].Err)
	}
	if res.Outcomes[1].Err == nil || res.Outcomes[1].Err.Code != CodeMutex {
		t.Fatalf("want mutex reject for pay, got %+v", res.Outcomes[1].Err)
	}
}

// 4. 迁移后基数校验使用迁移生效后的真实链接状态（同批新增链接计入）。
func TestPostCardinality(t *testing.T) {
	st := seedStore(
		inst("o1", "order", "created", map[string]AttrValue{"paid": true}),
		inst("t1", "order", "created", nil),
		inst("t2", "order", "created", nil),
		inst("t3", "order", "created", nil),
	)
	eng := NewEngine(testSchema(), st, DiscardLogger{})

	res, _ := eng.Batch([]Op{
		AddLink("tag", "o1", "t1"),
		AddLink("tag", "o1", "t2"),
		AddLink("tag", "o1", "t3"),
		Fire("o1", "pay"),
	})
	if res.Committed || res.Outcomes[3].Err == nil ||
		res.Outcomes[3].Err.Code != CodeCardinality {
		t.Fatalf("want cardinality reject, got committed=%v err=%+v",
			res.Committed, res.Outcomes[3].Err)
	}
	for _, to := range []string{"t1", "t2", "t3"} {
		if st.HasLink(Link{Type: "tag", FromID: "o1", ToID: to}) {
			t.Fatalf("被拒批次的链接改动未回滚: %s", to)
		}
	}

	res, _ = eng.Batch([]Op{
		AddLink("tag", "o1", "t1"),
		AddLink("tag", "o1", "t2"),
		Fire("o1", "pay"),
	})
	if !res.Committed {
		t.Fatalf("基数满足时应成功: %+v", res.Outcomes[2].Err)
	}
}

// 5. 跨实例钩子拒绝：发起方与链接新增都不得生效。
func TestHookReject(t *testing.T) {
	st := seedStore(
		inst("o1", "order", "created", map[string]AttrValue{"paid": true}),
		inst("i1", "invoice", "pending", nil),
	)
	eng := NewEngine(testSchema(), st, DiscardLogger{})
	res, _ := eng.Batch([]Op{
		AddLink("has_invoice", "o1", "i1"),
		Fire("o1", "pay_audit"),
	})
	if res.Committed {
		t.Fatal("钩子不满足时整批必须拒绝")
	}
	err := res.Outcomes[1].Err
	if err == nil || err.Code != CodeHook {
		t.Fatalf("want hook reject, got %+v", err)
	}
	if got := st.GetInstance("o1").State; got != "created" {
		t.Fatal("钩子拒绝后发起方状态不得改变")
	}
	if st.HasLink(Link{Type: "has_invoice", FromID: "o1", ToID: "i1"}) {
		t.Fatal("钩子拒绝后链接新增必须回滚")
	}
}

// 6. 链式触发整体生效。
func TestCascadeCommit(t *testing.T) {
	st := seedStore(
		inst("o1", "order", "created", map[string]AttrValue{"paid": true}),
		inst("i1", "invoice", "pending", map[string]AttrValue{"ok": true}),
	)
	eng := NewEngine(testSchema(), st, DiscardLogger{})
	res, _ := eng.Batch([]Op{
		AddLink("has_invoice", "o1", "i1"),
		Fire("o1", "pay"),
	})
	if !res.Committed {
		t.Fatalf("链式应整体成功: %+v", res.Outcomes[1].Err)
	}
	if got := st.GetInstance("o1").State; got != "paid" {
		t.Fatalf("order state=%s", got)
	}
	if got := st.GetInstance("i1").State; got != "issued" {
		t.Fatalf("invoice state=%s", got)
	}
	fired := res.Outcomes[1].Fired
	if len(fired) != 2 || fired[0].InstanceID != "o1" || fired[1].InstanceID != "i1" {
		t.Fatalf("级联环节异常: %+v", fired)
	}
}

// 7. 链式触发中一环前置失败 -> 此前已生效环节一并撤销。
func TestCascadeRollback(t *testing.T) {
	st := seedStore(
		inst("o1", "order", "created", map[string]AttrValue{"paid": true}),
		inst("i1", "invoice", "pending", nil),
	)
	eng := NewEngine(testSchema(), st, DiscardLogger{})
	res, _ := eng.Batch([]Op{
		AddLink("has_invoice", "o1", "i1"),
		Fire("o1", "pay"),
	})
	if res.Committed {
		t.Fatal("级联前置失败时整批不得提交")
	}
	if res.Outcomes[1].Err == nil || res.Outcomes[1].Err.Code != CodePrecondition {
		t.Fatalf("want precondition on cascade, got %+v", res.Outcomes[1].Err)
	}
	if got := st.GetInstance("o1").State; got != "created" {
		t.Fatal("发起方必须回滚")
	}
	if got := st.GetInstance("i1").State; got != "pending" {
		t.Fatal("被级联方必须保持 pending")
	}
}

// 8. 循环触发必须在任何一环生效前检测并拒绝。
func TestCascadeCycle(t *testing.T) {
	st := seedStore(
		inst("a", "node", "s0", nil),
		inst("b", "node", "s0", nil),
	)
	eng := NewEngine(cycleSchema(), st, DiscardLogger{})
	res, _ := eng.Batch([]Op{
		AddLink("next", "a", "b"),
		AddLink("next", "b", "a"),
		Fire("a", "go"),
	})
	if res.Committed || res.Outcomes[2].Err == nil ||
		res.Outcomes[2].Err.Code != CodeCycle {
		t.Fatalf("want cycle reject, got committed=%v err=%+v",
			res.Committed, res.Outcomes[2].Err)
	}
	if got := st.GetInstance("a").State; got != "s0" || st.GetInstance("b").State != "s0" {
		t.Fatal("循环拒绝后任何环节不得迁移")
	}
}

// 9. 终态全面保护。
func TestTerminalProtection(t *testing.T) {
	st := seedStore(
		inst("o1", "order", "closed", nil),
		inst("o2", "order", "paid", nil),
		inst("t1", "order", "created", nil),
	)
	eng := NewEngine(testSchema(), st, DiscardLogger{})
	// 终态上允许删除“已存在”的链接（删除本身是允许操作）。
	st.lockIDs([]string{"o1"})
	o1 := st.inst["o1"].load()
	if o1.out["tag"] == nil {
		o1.out["tag"] = map[string]bool{}
	}
	o1.out["tag"]["t1"] = true
	st.unlockIDs([]string{"o1"})

	res, _ := eng.Batch([]Op{SetAttr("o1", "x", 1)})
	if res.Committed || res.Outcomes[0].Err.Code != CodeTerminal {
		t.Fatalf("终态改属性必须拒绝, got %+v", res.Outcomes[0].Err)
	}

	res, _ = eng.Batch([]Op{AddLink("tag", "o2", "o1")})
	if res.Committed || res.Outcomes[0].Err.Code != CodeTerminal {
		t.Fatalf("终态参与新增链接必须拒绝, got %+v", res.Outcomes[0].Err)
	}

	res, _ = eng.Batch([]Op{DelLink("tag", "o1", "t1")})
	if !res.Committed {
		t.Fatal("终态应允许删除已有链接")
	}
	if st.HasLink(Link{Type: "tag", FromID: "o1", ToID: "t1"}) {
		t.Fatal("终态链接删除应当生效")
	}

	before := st.GetInstance("o1").Clock
	eng.Batch([]Op{SetAttr("o1", "x", 1)})
	if after := st.GetInstance("o1").Clock; after != before {
		t.Fatalf("被拒操作改动了时钟戳: %d -> %d", before, after)
	}
}

// 10. failpoint 验证提交中途失败时的反向整体回滚。
func TestCommitRollbackOrder(t *testing.T) {
	st := seedStore(
		inst("o1", "order", "created", map[string]AttrValue{"paid": true}),
		inst("i1", "invoice", "pending", map[string]AttrValue{"ok": true}),
	)
	eng := NewEngine(testSchema(), st, DiscardLogger{})
	eng.setFailAfterSteps(1)

	res, _ := eng.Batch([]Op{
		AddLink("has_invoice", "o1", "i1"),
		Fire("o1", "pay"),
	})
	if res.Committed {
		t.Fatal("failpoint 触发后不得提交")
	}
	if got := st.GetInstance("o1").State; got != "created" {
		t.Fatal("failpoint 回滚后 o1 必须复原")
	}
	if st.HasLink(Link{Type: "has_invoice", FromID: "o1", ToID: "i1"}) {
		t.Fatal("failpoint 回滚后链接必须复原")
	}
}

var _ = sync.Mutex{}

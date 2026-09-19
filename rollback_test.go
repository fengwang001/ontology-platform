package ontology

import (
	"errors"
	"strings"
	"testing"
)

// 业务逻辑中途失败：已创建对象、已加关系必须全部逆序回滚，
// 存储与 Action 开始前完全一致。
func TestMidFailureRollsBackEverything(t *testing.T) {
	st := NewStore()
	e := NewEngine(st)

	e.Register(NewActionType("createTwo", Schema{Params: []ParamSpec{
		{Name: "a", Type: TypeString, Required: true},
		{Name: "b", Type: TypeString, Required: true},
	}}, []string{testType}, nil, func(tx *Txn, args map[string]any) error {
		if err := tx.CreateObject(args["a"].(string), testType, nil); err != nil {
			return err
		}
		if err := tx.CreateObject(args["b"].(string), testType, nil); err != nil {
			return err
		}
		if err := tx.AddRelation(Relation{Type: "link",
			FromID: args["a"].(string), ToID: args["b"].(string)}); err != nil {
			return err
		}
		return errors.New("业务逻辑主动失败")
	}))

	beforeO, beforeR := st.ObjectCount(), st.RelationCount()
	_, err := e.Execute("createTwo", map[string]any{"a": "x", "b": "y"})
	if err == nil {
		t.Fatal("期望执行失败")
	}
	afterO, afterR := st.ObjectCount(), st.RelationCount()
	if beforeO != afterO || beforeR != afterR {
		t.Fatalf("回滚不彻底: 对象 %d->%d, 关系 %d->%d", beforeO, afterO, beforeR, afterR)
	}
	if _, ok := st.GetObject("x"); ok {
		t.Fatal("半成品对象残留")
	}
	if e.Log().LastSeq() != 0 {
		t.Fatal("失败 Action 不得占用序号")
	}
}

// 约束冲突（重复创建）同样整体回滚。
func TestConstraintConflictRollback(t *testing.T) {
	st := NewStore()
	e := NewEngine(st)
	e.Register(NewActionType("dup", Schema{Params: []ParamSpec{
		{Name: "id", Type: TypeString, Required: true},
	}}, []string{testType}, nil, func(tx *Txn, args map[string]any) error {
		// 先创建一个会随失败一起回滚的临时对象，再撞唯一约束。
		if err := tx.CreateObject("tmp", testType, nil); err != nil {
			return err
		}
		return tx.CreateObject(args["id"].(string), testType, nil)
	}))
	if _, err := e.Execute("dup", map[string]any{"id": "dup"}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Execute("dup", map[string]any{"id": "dup"}); err == nil {
		t.Fatal("重复创建应失败")
	}
	if st.ObjectCount() != 2 { // 第一次成功提交的 tmp 与 dup
		t.Fatalf("第二次冲突调用回滚后对象数应仍为 2，实际 %d", st.ObjectCount())
	}
	if got := e.Log().LastSeq(); got != 1 {
		t.Fatalf("失败调用不得占用序号，最后序号=%d", got)
	}
}

// 撤销失败：存储被标记不一致，记录无法撤销的步骤，之后写入一律拒绝。
func TestUndoFailureMarksInconsistent(t *testing.T) {
	st := NewStore()
	e := NewEngine(st)
	e.failUndoDesc = "create object tmp(item)"

	e.Register(NewActionType("risky", Schema{Params: []ParamSpec{
		{Name: "id", Type: TypeString, Required: true},
	}}, []string{testType}, nil, func(tx *Txn, args map[string]any) error {
		if err := tx.CreateObject("tmp", testType, nil); err != nil {
			return err
		}
		if err := tx.CreateObject(args["id"].(string), testType, nil); err != nil {
			return err
		}
		return errors.New("触发回滚")
	}))

	_, err := e.Execute("risky", map[string]any{"id": "ok"})
	var rb *RollbackError
	if !errors.As(err, &rb) {
		t.Fatalf("期望 RollbackError，得到 %T: %v", err, err)
	}
	if st.IsConsistent() {
		t.Fatal("存储应被标记为不一致")
	}
	step := st.InconsistentStep()
	if step == "" || !strings.Contains(step, "create object tmp") {
		t.Fatalf("不一致点报告错误: %q", step)
	}

	// 后续写入一律拒绝。
	_, err = e.Execute("risky", map[string]any{"id": "again"})
	var ic *InconsistentError
	if !errors.As(err, &ic) {
		t.Fatalf("不一致后写入应被拒绝，得到 %T: %v", err, err)
	}
}

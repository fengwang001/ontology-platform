package ontology

import (
	"errors"
	"testing"
)

func TestRelationRemoveAndRestoreOnRollback(t *testing.T) {
	st := NewStore()
	e := NewEngine(st)

	e.Register(NewActionType("link", Schema{Params: []ParamSpec{
		{Name: "a", Type: TypeString, Required: true},
		{Name: "b", Type: TypeString, Required: true},
	}}, []string{testType}, nil, func(tx *Txn, args map[string]any) error {
		if err := tx.CreateObject(args["a"].(string), testType, nil); err != nil {
			return err
		}
		return tx.CreateObject(args["b"].(string), testType, nil)
	}))
	if _, err := e.Execute("link", map[string]any{"a": "a1", "b": "b1"}); err != nil {
		t.Fatal(err)
	}

	rel := Relation{Type: "knows", FromID: "a1", ToID: "b1"}
	e.Register(NewActionType("bind", Schema{}, []string{testType}, nil, func(tx *Txn, args map[string]any) error {
		if err := tx.AddRelation(rel); err != nil {
			return err
		}
		return tx.RemoveRelation(Relation{Type: "ghost", FromID: "x", ToID: "y"})
	}))
	if _, err := e.Execute("bind", nil); err == nil {
		t.Fatal("删除不存在的关系应失败")
	}
	if st.RelationCount() != 0 {
		t.Fatalf("失败 Action 中建立的关系必须回滚，关系数=%d", st.RelationCount())
	}

	// 成功建立，再由一个失败 Action 删除——删除也要随回滚恢复。
	e.Register(NewActionType("bindok", Schema{}, []string{testType}, nil, func(tx *Txn, args map[string]any) error {
		return tx.AddRelation(rel)
	}))
	if _, err := e.Execute("bindok", nil); err != nil {
		t.Fatal(err)
	}
	e.Register(NewActionType("unlinkfail", Schema{}, []string{testType}, nil, func(tx *Txn, args map[string]any) error {
		if err := tx.RemoveRelation(rel); err != nil {
			return err
		}
		return errBoom
	}))
	if _, err := e.Execute("unlinkfail", nil); err == nil {
		t.Fatal("应失败")
	}
	if !st.relations0(rel) {
		t.Fatal("被失败事务删除的关系必须恢复")
	}
}

var errBoom = errors.New("boom")

// relations0 通过引擎再建一个只读 Action 太重，直接读存储（同包测试）。
func (s *Store) relations0(rel Relation) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.relations[rel]
	return ok
}

func TestUndeclaredObjectTypeRejected(t *testing.T) {
	st := NewStore()
	e := NewEngine(st)
	e.Register(NewActionType("onlyItem", Schema{}, []string{testType}, nil, func(tx *Txn, args map[string]any) error {
		return tx.CreateObject("z", "other-type", nil)
	}))
	if _, err := e.Execute("onlyItem", nil); err == nil {
		t.Fatal("创建未声明触碰的对象类型必须失败")
	}
	if st.ObjectCount() != 0 {
		t.Fatal("越类型创建必须回滚")
	}
}

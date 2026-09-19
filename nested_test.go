package ontology

import (
	"errors"
	"strconv"
	"strings"
	"testing"
)

func newNestedEngine() (*Engine, *Store) {
	st := NewStore()
	e := NewEngine(st)

	// ok: 创建一个对象，成功。
	e.Register(NewActionType("ok", Schema{Params: []ParamSpec{
		{Name: "id", Type: TypeString, Required: true},
	}}, []string{testType}, nil, func(tx *Txn, args map[string]any) error {
		return tx.CreateObject(args["id"].(string), testType, nil)
	}))

	// bad: 先创建对象再返回错误，回滚到自身保存点。
	e.Register(NewActionType("bad", Schema{Params: []ParamSpec{
		{Name: "id", Type: TypeString, Required: true},
	}}, []string{testType}, nil, func(tx *Txn, args map[string]any) error {
		if err := tx.CreateObject(args["id"].(string), testType, nil); err != nil {
			return err
		}
		return errors.New("内层必然失败")
	}))

	// tolerant: 调 bad 但吞掉错误后继续，再创建一个对象。
	e.Register(NewActionType("tolerant", Schema{Params: []ParamSpec{
		{Name: "id", Type: TypeString, Required: true},
	}}, []string{testType}, nil, func(tx *Txn, args map[string]any) error {
		if err := tx.CreateObject(args["id"].(string), testType, nil); err != nil {
			return err
		}
		if err := tx.Call("bad", map[string]any{"id": args["id"].(string) + "-inner"}); err == nil {
			return errors.New("内层本应失败")
		}
		return tx.CreateObject(args["id"].(string)+"-kept", testType, nil)
	}))

	// outerFail: 内层成功，但外层最后失败——内层成果必须一起回滚。
	e.Register(NewActionType("outerFail", Schema{}, []string{testType}, nil, func(tx *Txn, args map[string]any) error {
		if err := tx.Call("ok", map[string]any{"id": "inner-ok"}); err != nil {
			return err
		}
		return errors.New("外层最后失败")
	}))

	return e, st
}

func TestInnerFailureSavepointOuterContinues(t *testing.T) {
	e, st := newNestedEngine()
	r, err := e.Execute("tolerant", map[string]any{"id": "root"})
	if err != nil {
		t.Fatalf("外层吞掉内层错误后应成功，得到 %v", err)
	}
	if r.Seq != 1 {
		t.Fatalf("成功提交序号应为 1，得到 %d", r.Seq)
	}
	// inner 失败对象被回滚；root 与 root-kept 保留。
	if st.ObjectCount() != 2 {
		t.Fatalf("内层半成品必须回滚，对象数=%d", st.ObjectCount())
	}
	if _, ok := st.GetObject("root-inner"); ok {
		t.Fatal("内层失败对象不得残留")
	}
	if _, ok := st.GetObject("root-kept"); !ok {
		t.Fatal("外层在保存点之后继续创建的对象应保留")
	}
}

func TestOuterFailureRollsBackSuccessfulInner(t *testing.T) {
	e, st := newNestedEngine()
	_, err := e.Execute("outerFail", nil)
	if err == nil {
		t.Fatal("外层应失败")
	}
	if st.ObjectCount() != 0 {
		t.Fatalf("外层失败必须连带回滚已成功的内层，对象数=%d", st.ObjectCount())
	}
	if e.Log().LastSeq() != 0 {
		t.Fatal("失败链路上不得产生任何执行记录")
	}
}

func TestRecursionDetectedWithChain(t *testing.T) {
	st := NewStore()
	e := NewEngine(st)
	var self *ActionType
	self = NewActionType("rec", Schema{}, []string{testType}, nil, func(tx *Txn, args map[string]any) error {
		return tx.Call("rec", nil)
	})
	e.Register(self)

	// 间接递归：a -> b -> a。
	var a, b *ActionType
	a = NewActionType("a", Schema{}, []string{testType}, nil, func(tx *Txn, args map[string]any) error {
		return tx.Call("b", nil)
	})
	b = NewActionType("b", Schema{}, []string{testType}, nil, func(tx *Txn, args map[string]any) error {
		return tx.Call("a", nil)
	})
	e.Register(a)
	e.Register(b)

	_, err := e.Execute("rec", nil)
	var rc *RecursionError
	if !errors.As(err, &rc) {
		t.Fatalf("期望 RecursionError，得到 %T: %v", err, err)
	}
	if strings.Join(rc.Chain, "->") != "rec->rec" {
		t.Fatalf("直接递归调用链错误: %v", rc.Chain)
	}

	_, err = e.Execute("a", nil)
	if !errors.As(err, &rc) {
		t.Fatalf("期望 RecursionError，得到 %T: %v", err, err)
	}
	want := "a->b->a"
	if strings.Join(rc.Chain, "->") != want {
		t.Fatalf("间接递归调用链应为 %s，得到 %v", want, rc.Chain)
	}
}

func TestDepthLimitReportsChain(t *testing.T) {
	st := NewStore()
	e := NewEngine(st)
	// down 每次再调 down，靠深度上限（而非递归检测）拒绝——
	// 但同名会先触发递归检测，因此用编号链式的不同 Action 名。
	const n = MaxDepth + 3
	for i := 0; i < n; i++ {
		i := i
		name := depthName(i)
		var h HandlerFunc
		if i+1 < n {
			h = func(tx *Txn, args map[string]any) error {
				return tx.Call(depthName(i+1), nil)
			}
		} else {
			h = noopHandler
		}
		e.Register(NewActionType(name, Schema{}, []string{testType}, nil, h))
	}

	_, err := e.Execute(depthName(0), nil)
	var de *DepthError
	if !errors.As(err, &de) {
		t.Fatalf("期望 DepthError，得到 %T: %v", err, err)
	}
	if de.Limit != MaxDepth || len(de.Chain) != MaxDepth+1 {
		t.Fatalf("深度链长度应为 %d（含超限目标），得到 %d", MaxDepth+1, len(de.Chain))
	}
	if de.Chain[0] != depthName(0) {
		t.Fatalf("调用链起点错误: %v", de.Chain)
	}
	if de.Chain[MaxDepth] != depthName(MaxDepth) {
		t.Fatalf("调用链终点错误: %v", de.Chain)
	}
}

func depthName(i int) string {
	return "d" + strconv.Itoa(i)
}

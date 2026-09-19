package ontology

import (
	"errors"
	"testing"
)

// 钩子按声明顺序执行；第二个钩子拒绝，错误必须指出“第 2 个”及理由。
func TestHookRejectReportsIndexAndReason(t *testing.T) {
	e := NewEngine(NewStore())
	var order []int
	e.Register(NewActionType("a", Schema{}, []string{testType}, []HookFunc{
		func(tx *Txn, args map[string]any) string { order = append(order, 1); return "" },
		func(tx *Txn, args map[string]any) string { order = append(order, 2); return "余额不足" },
	}, func(tx *Txn, args map[string]any) error {
		t.Fatal("被拒绝后 handler 不应执行")
		return nil
	}))

	_, err := e.Execute("a", map[string]any{})
	var hr *HookRejectError
	if !errors.As(err, &hr) {
		t.Fatalf("期望 HookRejectError，得到 %T: %v", err, err)
	}
	if hr.Index != 2 || hr.Reason != "余额不足" {
		t.Fatalf("钩子序号/理由错误: %+v", hr)
	}
	if len(order) != 2 || order[0] != 1 || order[1] != 2 {
		t.Fatalf("钩子未按声明顺序执行: %v", order)
	}
	if e.Log().LastSeq() != 0 {
		t.Fatal("被拒绝的 Action 不应占用序号")
	}
}

// 钩子可以读到事务内已发生的修改（这里读外层事务的状态），
// 同时钩子自身的写入不生效，且在提交时被检测、整体失败。
func TestHookCanReadButWriteIsDetected(t *testing.T) {
	st := NewStore()
	e := NewEngine(st)

	var sawBalance any
	e.Register(NewActionType("a", Schema{Params: []ParamSpec{
		{Name: "id", Type: TypeString, Required: true},
	}}, []string{testType}, []HookFunc{
		// 第 1 个钩子只读。
		func(tx *Txn, args map[string]any) string {
			if o, ok := tx.GetObject(args["id"].(string)); ok {
				sawBalance = o.Attributes["balance"]
			}
			return ""
		},
		// 第 2 个钩子试图越权写入。
		func(tx *Txn, args map[string]any) string {
			_ = tx.SetAttribute(args["id"].(string), "balance", 999)
			return ""
		},
	}, func(tx *Txn, args map[string]any) error {
		return tx.SetAttribute(args["id"].(string), "balance", 42)
	}))

	// 预置一个对象（通过另一个干净 Action）。
	e.Register(NewActionType("seed", Schema{Params: []ParamSpec{
		{Name: "id", Type: TypeString, Required: true},
	}}, []string{testType}, nil, func(tx *Txn, args map[string]any) error {
		return tx.CreateObject(args["id"].(string), testType, map[string]any{"balance": 0})
	}))
	if _, err := e.Execute("seed", map[string]any{"id": "o1"}); err != nil {
		t.Fatal(err)
	}

	// 让 handler 先制造“事务内已发生的修改”给钩子读：
	// 钩子在 handler 之前运行，因此这里读到的是同事务外层数据。
	// 为验证“读到事务内修改”，使用嵌套：外层先改，内层钩子读。
	e.Register(NewActionType("inner", Schema{Params: []ParamSpec{
		{Name: "id", Type: TypeString, Required: true},
	}}, []string{testType}, []HookFunc{
		func(tx *Txn, args map[string]any) string {
			if o, ok := tx.GetObject(args["id"].(string)); ok {
				sawBalance = o.Attributes["balance"]
			}
			_ = tx.CreateObject("rogue", testType, nil)
			return ""
		},
	}, func(tx *Txn, args map[string]any) error { return nil }))

	e.Register(NewActionType("outer", Schema{Params: []ParamSpec{
		{Name: "id", Type: TypeString, Required: true},
	}}, []string{testType}, nil, func(tx *Txn, args map[string]any) error {
		if err := tx.SetAttribute(args["id"].(string), "balance", 77); err != nil {
			return err
		}
		return tx.Call("inner", map[string]any{"id": args["id"].(string)})
	}))

	_, err := e.Execute("outer", map[string]any{"id": "o1"})
	var hw *HookWriteError
	if !errors.As(err, &hw) {
		t.Fatalf("期望 HookWriteError，得到 %T: %v", err, err)
	}
	if hw.Index != 1 || hw.Action != "inner" {
		t.Fatalf("越权钩子定位错误: %+v", hw)
	}
	if sawBalance != 77 {
		t.Fatalf("钩子应读到事务内未提交修改 balance=77，读到 %v", sawBalance)
	}
	if st.ObjectCount() != 1 {
		t.Fatalf("越权写入的对象不得残留，对象数=%d", st.ObjectCount())
	}
	if o, _ := st.GetObject("o1"); o.Attributes["balance"] != 0 {
		t.Fatalf("外层修改也必须随失败回滚，balance=%v", o.Attributes["balance"])
	}
}

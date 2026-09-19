package ontology

import (
	"errors"
	"testing"
)

const testType = "item"

func noopHandler(*Txn, map[string]any) error { return nil }

func TestParamErrorsReportedAllAtOnce(t *testing.T) {
	e := NewEngine(NewStore())
	e.Register(NewActionType("ship", Schema{Params: []ParamSpec{
		{Name: "id", Type: TypeString, Required: true},
		{Name: "qty", Type: TypeInt, Required: true},
		{Name: "note", Type: TypeString, Required: false, Default: "n/a"},
	}}, nil, nil, noopHandler))

	// 缺 id（必填）、qty 类型错、传入未声明参数 extra，必须一次报全。
	_, err := e.Execute("ship", map[string]any{"qty": "many", "extra": 1})
	var pe ParamErrors
	if !errors.As(err, &pe) {
		t.Fatalf("期望 ParamErrors，得到 %T: %v", err, err)
	}
	kinds := map[ParamKind]bool{}
	for _, p := range pe {
		kinds[p.Kind] = true
	}
	for _, k := range []ParamKind{ParamMissingRequired, ParamTypeMismatch, ParamUnknown} {
		if !kinds[k] {
			t.Fatalf("缺少错误类别 %s，全部问题: %v", k, pe)
		}
	}
	if len(pe) != 3 {
		t.Fatalf("期望恰好 3 条参数问题，得到 %d: %v", len(pe), pe)
	}

	// 三类错误必须能被调用方分别判定。
	var asTarget ParamErrors
	errors.As(err, &asTarget)
	byName := map[string]ParamError{}
	for _, p := range asTarget {
		byName[p.Name] = p
	}
	if byName["id"].Kind != ParamMissingRequired {
		t.Fatal("id 应为 missing_required")
	}
	if byName["qty"].Kind != ParamTypeMismatch {
		t.Fatal("qty 应为 type_mismatch")
	}
	if byName["extra"].Kind != ParamUnknown {
		t.Fatal("extra 应为 unknown_param")
	}
}

func TestDefaultValuesFilledAndNotPolluted(t *testing.T) {
	e := NewEngine(NewStore())

	// handler 内修改本次规范化参数不得污染 ActionType 声明的默认值。
	a := NewActionType("m", Schema{Params: []ParamSpec{
		{Name: "n", Type: TypeInt, Required: false, Default: int(5)},
	}}, nil, nil, func(tx *Txn, args map[string]any) error {
		args["n"] = int(999) // 改本次的规范化参数，不应影响声明默认值
		return nil
	})
	e.Register(a)

	_, err := e.Execute("m", map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	if a.defaultSnapshot["n"] != 5 {
		t.Fatalf("首次调用后默认快照被污染: %v", a.defaultSnapshot["n"])
	}
	_, err = e.Execute("m", map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	if a.defaultSnapshot["n"] != 5 {
		t.Fatalf("默认值被上次调用污染，快照=%v", a.defaultSnapshot["n"])
	}

	// 直接验证声明 map 默认值被深拷贝快照。
	orig := map[string]any{"k": "v"}
	b := NewActionType("b", Schema{Params: []ParamSpec{
		{Name: "x", Type: TypeInt, Required: false, Default: orig},
	}}, nil, nil, noopHandler)
	orig["k"] = "changed-after-declare"
	if b.defaultSnapshot["x"].(map[string]any)["k"] != "v" {
		t.Fatal("声明后修改原始默认值污染了 ActionType 快照")
	}
}

func TestDeepCopyIsolatesNestedMapsAndSlices(t *testing.T) {
	orig := map[string]any{
		"m": map[string]any{"a": []any{int(1), int(2)}},
	}
	cp := deepCopy(orig).(map[string]any)
	cp["m"].(map[string]any)["a"].([]any)[0] = int(9)
	if orig["m"].(map[string]any)["a"].([]any)[0] != 1 {
		t.Fatal("深层 deepCopy 未隔离")
	}
}

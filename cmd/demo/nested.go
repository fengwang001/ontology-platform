package main

import "ontology"

// registerNested 注册嵌套与递归演示用的 Action。
func registerNested(e *ontology.Engine) {
	// audit：内层 Action，故意创建对象后失败 -> 只回滚到它的保存点。
	e.Register(ontology.NewActionType("audit", ontology.Schema{Params: []ontology.ParamSpec{
		{Name: "id", Type: ontology.TypeString, Required: true},
	}}, []string{accountType}, nil, func(tx *ontology.Txn, args map[string]any) error {
		if err := tx.CreateObject(args["id"].(string), accountType, nil); err != nil {
			return err
		}
		return errBoom
	}))

	// settle：外层先建对象，再调用会失败的 audit，吞掉错误后继续。
	e.Register(ontology.NewActionType("settle", ontology.Schema{Params: []ontology.ParamSpec{
		{Name: "id", Type: ontology.TypeString, Required: true},
	}}, []string{accountType}, nil, func(tx *ontology.Txn, args map[string]any) error {
		id := args["id"].(string)
		if err := tx.CreateObject(id, accountType, map[string]any{"balance": 100}); err != nil {
			return err
		}
		if err := tx.Call("audit", map[string]any{"id": id + "-audit"}); err == nil {
			return errBoom
		}
		return tx.SetAttribute(id, "settled", true)
	}))

	// recurse：自己调自己，演示递归检测。
	e.Register(ontology.NewActionType("recurse", ontology.Schema{}, []string{accountType}, nil,
		func(tx *ontology.Txn, args map[string]any) error {
			return tx.Call("recurse", nil)
		}))
}

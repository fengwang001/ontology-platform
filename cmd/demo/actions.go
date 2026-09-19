package main

import (
	"errors"

	"ontology"
)

const accountType = "account"

// registerActions 构建演示用的 ActionType 集合。
func registerActions(e *ontology.Engine) {
	// openAccount：开户并设置初始余额。
	e.Register(ontology.NewActionType("openAccount", ontology.Schema{Params: []ontology.ParamSpec{
		{Name: "id", Type: ontology.TypeString, Required: true},
		{Name: "balance", Type: ontology.TypeInt, Required: false, Default: int(0)},
	}}, []string{accountType}, nil, func(tx *ontology.Txn, args map[string]any) error {
		return tx.CreateObject(args["id"].(string), accountType,
			map[string]any{"balance": args["balance"]})
	}))

	// transfer：带两个前置钩子的转账（第二个钩子演示拒绝）。
	e.Register(ontology.NewActionType("transfer", ontology.Schema{Params: []ontology.ParamSpec{
		{Name: "from", Type: ontology.TypeString, Required: true},
		{Name: "to", Type: ontology.TypeString, Required: true},
		{Name: "amount", Type: ontology.TypeInt, Required: true},
	}}, []string{accountType}, []ontology.HookFunc{
		// 钩子 1：账户必须存在。
		func(tx *ontology.Txn, args map[string]any) string {
			for _, k := range []string{"from", "to"} {
				if _, ok := tx.GetObject(args[k].(string)); !ok {
					return "账户不存在: " + args[k].(string)
				}
			}
			return ""
		},
		// 钩子 2：余额必须充足。
		func(tx *ontology.Txn, args map[string]any) string {
			src, _ := tx.GetObject(args["from"].(string))
			if src.Attributes["balance"].(int) < args["amount"].(int) {
				return "余额不足"
			}
			return ""
		},
	}, func(tx *ontology.Txn, args map[string]any) error {
		src, _ := tx.GetObject(args["from"].(string))
		dst, _ := tx.GetObject(args["to"].(string))
		amt := args["amount"].(int)
		if err := tx.SetAttribute(args["from"].(string), "balance",
			src.Attributes["balance"].(int)-amt); err != nil {
			return err
		}
		return tx.SetAttribute(args["to"].(string), "balance",
			dst.Attributes["balance"].(int)+amt)
	}))

	registerNested(e)
}

var errBoom = errors.New("业务中途失败")

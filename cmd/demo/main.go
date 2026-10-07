// Command demo 演示对象生命周期状态机子系统的核心语义：
// 前置条件、互斥优先级、迁移后基数、跨实例钩子、链式随迁与整体回滚。
package main

import (
	"fmt"
	"os"

	"ontology/lifecycle"
)

func main() {
	schema := &lifecycle.Schema{Types: map[string]*lifecycle.ObjectType{
		"order": {
			Name:        "order",
			States:      []string{"created", "paid", "closed"},
			FinalStates: map[string]bool{"closed": true},
			Transitions: map[string]*lifecycle.Transition{
				"pay": {
					Name: "pay", From: "created", To: "paid",
					Preconds: []lifecycle.Precondition{
						lifecycle.Attr("approved", true),
					},
					MaxCard: []lifecycle.CardinalityRule{{LinkType: "tag", Max: 2}},
					Hooks: []lifecycle.HookRule{
						{LinkType: "invoice", States: []string{"issued"}},
					},
					Cascades: []lifecycle.CascadeRule{{
						LinkType: "invoice", WhenStates: []string{"pending"},
						Transition: "issue",
					}},
				},
				"close": {Name: "close", From: "paid", To: "closed"},
			},
		},
		"invoice": {
			Name:   "invoice",
			States: []string{"pending", "issued"},
			Transitions: map[string]*lifecycle.Transition{
				"issue": {
					Name: "issue", From: "pending", To: "issued",
					Preconds: []lifecycle.Precondition{lifecycle.Attr("ok", true)},
				},
			},
		},
	}}

	store := lifecycle.NewStore()
	store.AddInstance(&lifecycle.Instance{
		ID: "order-1", Type: "order", State: "created",
		Attrs: map[string]lifecycle.AttrValue{"approved": true},
	})
	store.AddInstance(&lifecycle.Instance{
		ID: "inv-1", Type: "invoice", State: "pending",
		Attrs: map[string]lifecycle.AttrValue{"ok": true},
	})

	logger := lifecycle.NewTextLogger(os.Stdout)
	eng := lifecycle.NewEngine(schema, store, logger)

	run := func(title string, ops ...lifecycle.Op) {
		fmt.Printf("\n==== %s ====\n", title)
		res, err := eng.Batch(ops)
		if err != nil {
			fmt.Println("引擎错误:", err)
			return
		}
		if !res.Committed {
			fmt.Println("处理单元整体不生效（已回滚）")
			return
		}
		fmt.Println("处理单元整体生效")
	}

	run("链式随迁：order.pay 强制 inv.issue",
		lifecycle.AddLink("invoice", "order-1", "inv-1"),
		lifecycle.Fire("order-1", "pay"),
	)
	fmt.Printf("最终: order=%s invoice=%s\n",
		store.GetInstance("order-1").State, store.GetInstance("inv-1").State)

	run("终态保护：closed 订单拒绝改属性",
		lifecycle.Fire("order-1", "close"),
		lifecycle.SetAttr("order-1", "note", "x"),
	)
	fmt.Printf("最终: order=%s\n", store.GetInstance("order-1").State)
}

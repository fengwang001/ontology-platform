package lifecycle_test

import "ontology/lifecycle"

// diffSchema 是对拍专用模式，同时包含：
//   - 前置条件（attr）
//   - 迁移后基数（tag 上限 2）
//   - 跨实例钩子（has_invoice 对端须 issued）
//   - 级联随迁（pending 发票强制 issue）
//   - 终态（closed / void）
//   - 循环结构链接 next（配合 node 类型，这里仅 order/invoice，故用 next 作为普通链接）
func diffSchema() *lifecycle.Schema {
	return &lifecycle.Schema{Types: map[string]*lifecycle.ObjectType{
		"order": {
			Name:        "order",
			States:      []string{"created", "paid", "closed"},
			FinalStates: map[string]bool{"closed": true},
			Transitions: map[string]*lifecycle.Transition{
				"pay": {
					Name: "pay", From: "created", To: "paid",
					Preconds: []lifecycle.Precondition{
						lifecycle.Attr("paid", true),
					},
					MaxCard: []lifecycle.CardinalityRule{
						{LinkType: "tag", Max: 2},
					},
					Hooks: []lifecycle.HookRule{
						{LinkType: "has_invoice", States: []string{"issued"}},
					},
					Cascades: []lifecycle.CascadeRule{{
						LinkType: "has_invoice", WhenStates: []string{"pending"},
						Transition: "issue",
					}},
				},
				"close": {Name: "close", From: "paid", To: "closed"},
			},
		},
		"invoice": {
			Name:        "invoice",
			States:      []string{"pending", "issued", "void"},
			FinalStates: map[string]bool{"void": true},
			Transitions: map[string]*lifecycle.Transition{
				"issue": {
					Name: "issue", From: "pending", To: "issued",
					Preconds: []lifecycle.Precondition{lifecycle.Attr("ok", true)},
				},
				"void": {Name: "void", From: "issued", To: "void"},
			},
		},
	}}
}

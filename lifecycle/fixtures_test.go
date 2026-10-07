package lifecycle

// testSchema 构造一个包含两个对象类型、终态、互斥、基数、钩子与级联的模式。
//
// order: created -> paid -> shipped -> closed(终态)
//
//	pay:   前置 attr paid=true；迁移后 tag 基数 <= 2；
//	       钩子：has_invoice 对端必须处于 issued；
//	       级联：对处于 pending 的发票强制 issue。
//	ship:  与 pay 同属互斥组 g1（示例互斥：两者只能放行批内顺序靠前的一条）。
//
// invoice: pending -> issued -> void(终态)
//
//	issue: 前置 attr ok=true。
//	void:  无额外约束。
//
// 循环模式单独由 cycleSchema 提供：
//
//	a.go -> 级联 b.go；b.go -> 级联 a.go。
func testSchema() *Schema {
	return &Schema{Types: map[string]*ObjectType{
		"order": {
			Name:        "order",
			States:      []string{"created", "paid", "shipped", "closed"},
			FinalStates: map[string]bool{"closed": true},
			Transitions: map[string]*Transition{
				"pay": {
					Name: "pay", From: "created", To: "paid",
					Preconds: []Precondition{Attr("paid", true)},
					MaxCard:  []CardinalityRule{{LinkType: "tag", Max: 2}},
					Hooks:    []HookRule{{LinkType: "has_invoice", States: []string{"issued"}}},
					Cascades: []CascadeRule{{
						LinkType: "has_invoice", WhenStates: []string{"pending"},
						Transition: "issue",
					}},
					MutexGroupID: "g1",
				},
				"ship": {
					Name: "ship", From: "created", To: "shipped",
					Preconds:     []Precondition{},
					MutexGroupID: "g1",
				},
				// ship2 与 pay 同源态、无前置条件，用于纯粹验证互斥优先级。
				"ship2": {
					Name: "ship2", From: "created", To: "shipped",
					MutexGroupID: "g1",
				},
				"close": {Name: "close", From: "paid", To: "closed"},
				// pay_audit 仅带跨实例钩子、不级联，用于单独验证钩子联动拒绝。
				"pay_audit": {
					Name: "pay_audit", From: "created", To: "paid",
					Preconds: []Precondition{Attr("paid", true)},
					Hooks:    []HookRule{{LinkType: "has_invoice", States: []string{"issued"}}},
				},
			},
		},
		"invoice": {
			Name:        "invoice",
			States:      []string{"pending", "issued", "void"},
			FinalStates: map[string]bool{"void": true},
			Transitions: map[string]*Transition{
				"issue": {
					Name: "issue", From: "pending", To: "issued",
					Preconds: []Precondition{Attr("ok", true)},
				},
				"void": {Name: "void", From: "issued", To: "void"},
			},
		},
	}}
}

func cycleSchema() *Schema {
	return &Schema{Types: map[string]*ObjectType{
		"node": {
			Name:        "node",
			States:      []string{"s0", "s1"},
			FinalStates: map[string]bool{},
			Transitions: map[string]*Transition{
				"go": {
					Name: "go", From: "s0", To: "s1",
					Cascades: []CascadeRule{{
						LinkType: "next", WhenStates: []string{"s0"},
						Transition: "go",
					}},
				},
			},
		},
	}}
}

func seedStore(insts ...*Instance) *Store {
	st := NewStore()
	for _, in := range insts {
		st.AddInstance(in)
	}
	return st
}

func inst(id, typ, state string, attrs map[string]AttrValue) *Instance {
	if attrs == nil {
		attrs = map[string]AttrValue{}
	}
	return &Instance{ID: id, Type: typ, State: state, Attrs: attrs}
}

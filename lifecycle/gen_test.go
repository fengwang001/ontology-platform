package lifecycle_test

import (
	"math/rand"

	"ontology/internal/naive"
	"ontology/lifecycle"
)

func diffNaiveSpec() *naive.Spec {
	return &naive.Spec{Types: map[string]*naive.ObjectType{
		"order": {
			Final: map[string]bool{"closed": true},
			Transitions: map[string]*naive.Transition{
				"pay": {
					Name: "pay", From: "created", To: "paid",
					Preconds: []naive.Precondition{
						{Kind: naive.PrecondAttr, Attr: "paid", Equals: true},
					},
					MaxCard: []naive.Cardinality{{LinkType: "tag", Max: 2}},
					Hooks:   []naive.Hook{{LinkType: "has_invoice", States: []string{"issued"}}},
					Cascades: []naive.Cascade{{
						LinkType: "has_invoice", WhenStates: []string{"pending"},
						Transition: "issue",
					}},
				},
				"close": {Name: "close", From: "paid", To: "closed"},
			},
		},
		"invoice": {
			Final: map[string]bool{"void": true},
			Transitions: map[string]*naive.Transition{
				"issue": {
					Name: "issue", From: "pending", To: "issued",
					Preconds: []naive.Precondition{
						{Kind: naive.PrecondAttr, Attr: "ok", Equals: true},
					},
				},
				"void": {Name: "void", From: "issued", To: "void"},
			},
		},
	}}
}

func randomWorld(rng *rand.Rand) *world {
	no := 1 + rng.Intn(3)
	ni := 1 + rng.Intn(3)
	w := &world{
		attrs:  map[string]map[string]lifecycle.AttrValue{},
		states: map[string]string{},
		types:  map[string]string{},
	}
	orderStates := []string{"created", "paid", "closed"}
	invStates := []string{"pending", "issued", "void"}
	for i := 0; i < no; i++ {
		id := "o" + itoa(i)
		w.orders = append(w.orders, id)
		w.types[id] = "order"
		w.states[id] = orderStates[rng.Intn(len(orderStates))]
		a := map[string]lifecycle.AttrValue{}
		if rng.Intn(2) == 0 {
			a["paid"] = true
		}
		w.attrs[id] = a
	}
	for i := 0; i < ni; i++ {
		id := "i" + itoa(i)
		w.invoices = append(w.invoices, id)
		w.types[id] = "invoice"
		w.states[id] = invStates[rng.Intn(len(invStates))]
		a := map[string]lifecycle.AttrValue{}
		if rng.Intn(2) == 0 {
			a["ok"] = true
		}
		w.attrs[id] = a
	}
	// 随机链接。
	for _, o := range w.orders {
		for _, i := range w.invoices {
			if rng.Intn(2) == 0 {
				w.links = append(w.links, [3]string{"has_invoice", o, i})
			}
		}
		for k := 0; k < no; k++ {
			tag := w.orders[k]
			if o != tag && rng.Intn(3) == 0 {
				w.links = append(w.links, [3]string{"tag", o, tag})
			}
		}
	}
	return w
}

// randomOps 为两套实现并行生成语义相同的操作序列。
func randomOps(rng *rand.Rand, w *world, n int) ([]lifecycle.Op, []naive.Op) {
	var ps []lifecycle.Op
	var ns []naive.Op
	all := append(append([]string{}, w.orders...), w.invoices...)
	for step := 0; step < n; step++ {
		id := all[rng.Intn(len(all))]
		typ := w.types[id]
		switch rng.Intn(5) {
		case 0:
			tr := "pay"
			if typ == "invoice" {
				tr = "issue"
			}
			ps = append(ps, lifecycle.Fire(id, tr))
			ns = append(ns, naive.Op{Kind: naive.OpFire, InstanceID: id, Transition: tr})
		case 1:
			if typ == "order" {
				ps = append(ps, lifecycle.Fire(id, "close"))
				ns = append(ns, naive.Op{Kind: naive.OpFire, InstanceID: id, Transition: "close"})
			} else {
				ps = append(ps, lifecycle.Fire(id, "void"))
				ns = append(ns, naive.Op{Kind: naive.OpFire, InstanceID: id, Transition: "void"})
			}
		case 2:
			key := "paid"
			if typ == "invoice" {
				key = "ok"
			}
			val := rng.Intn(2) == 0
			ps = append(ps, lifecycle.SetAttr(id, key, val))
			ns = append(ns, naive.Op{
				Kind: naive.OpSetAttr, InstanceID: id, Attr: key, Value: val,
			})
		case 3:
			target := w.orders[rng.Intn(len(w.orders))]
			ps = append(ps, lifecycle.AddLink("tag", id, target))
			ns = append(ns, naive.Op{Kind: naive.OpAddLink,
				Link: naive.Link{Type: "tag", From: id, To: target}})
		case 4:
			target := w.invoices[rng.Intn(len(w.invoices))]
			ps = append(ps, lifecycle.AddLink("has_invoice", id, target))
			ns = append(ns, naive.Op{Kind: naive.OpAddLink,
				Link: naive.Link{Type: "has_invoice", From: id, To: target}})
		}
	}
	return ps, ns
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

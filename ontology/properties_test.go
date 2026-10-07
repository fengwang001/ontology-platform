package ontology

import (
	"bytes"
	"encoding/json"
	"testing"
)

func jsonMarshalView(v *InstanceView) ([]byte, error) {
	return json.Marshal(v)
}

// TestRegistrationOrderIndependence builds the same policy set in different
// registration orders and asserts the read view is byte-identical.
func TestRegistrationOrderIndependence(t *testing.T) {
	build := func(order []string) *InstanceView {
		al, de := allowDeny()
		h := newHarness(t, Config{
			RowMode: DenyOverrides, PropertyMode: DenyOverrides,
			DefaultRow: EffectAllow, DefaultRead: EffectAllow, DefaultWrite: EffectDeny,
		})
		h.store.Put(Instance{
			Type:    empType,
			ID:      "o1",
			Values:  map[string]Value{"name": {Str: "A"}, "clearance": {Str: "secret"}, "salary": {Int: 5}},
			Present: map[string]bool{"name": true, "clearance": true, "salary": true},
		})
		policies := map[string]PropertyPolicy{
			"01-deny-salary":  {ID: "01-deny-salary", ObjectType: empType, Property: "salary", Read: de},
			"02-allow-name":   {ID: "02-allow-name", ObjectType: empType, Property: "name", Read: al},
			"03-deny-clear-a": {ID: "03-deny-clear-a", ObjectType: empType, Property: "clearance", Read: de},
			"04-allow-clear":  {ID: "04-allow-clear", ObjectType: empType, Property: "clearance", Read: al},
			"05-mask-clear": {ID: "05-mask-clear", ObjectType: empType, Property: "clearance",
				Read: al, Mask: func(Value) Value { return Value{Str: "MASKED"} }},
		}
		for _, key := range order {
			_ = h.catalog.RegisterPropertyPolicy(policies[key])
		}
		view, err := h.a.Read("u", empType, "o1")
		if err != nil {
			t.Fatal(err)
		}
		return view
	}
	orders := [][]string{
		{"01-deny-salary", "02-allow-name", "03-deny-clear-a", "04-allow-clear", "05-mask-clear"},
		{"05-mask-clear", "04-allow-clear", "03-deny-clear-a", "02-allow-name", "01-deny-salary"},
		{"03-deny-clear-a", "05-mask-clear", "01-deny-salary", "04-allow-clear", "02-allow-name"},
	}
	var first []byte
	for i, order := range orders {
		view := build(order)
		data, _ := jsonMarshalView(view)
		if i == 0 {
			first = data
			continue
		}
		if !bytes.Equal(data, first) {
			t.Fatalf("order %d produced a different verdict:\n%s\nvs\n%s", i, data, first)
		}
	}
}

// TestDeterministicMaskSelection: with deny-overrides the read verdict for
// clearance is allow (one deny is absent here); both masks are legal plain
// strings, and the smallest policy ID must win regardless of order.
func TestDeterministicMaskSelection(t *testing.T) {
	al, _ := allowDeny()
	mask := func(out string) MaskFunc { return func(Value) Value { return Value{Str: out} } }
	makeView := func(ids [2]string) string {
		h := newHarness(t, Config{
			RowMode: AllowOverrides, PropertyMode: AllowOverrides,
			DefaultRow: EffectAllow, DefaultRead: EffectAllow, DefaultWrite: EffectDeny,
		})
		h.store.Put(Instance{
			Type:    empType,
			ID:      "m1",
			Values:  map[string]Value{"clearance": {Str: "x"}},
			Present: map[string]bool{"clearance": true},
		})
		for _, id := range ids {
			out := "FIRST"
			if id == "m-b" {
				out = "SECOND"
			}
			h.catalog.RegisterPropertyPolicy(PropertyPolicy{
				ID: id, ObjectType: empType, Property: "clearance", Read: al, Mask: mask(out),
			})
		}
		view, err := h.a.Read("u", empType, "m1")
		if err != nil {
			t.Fatal(err)
		}
		return view.Fields["clearance"].Value.Str
	}
	gotA := makeView([2]string{"m-a", "m-b"})
	gotB := makeView([2]string{"m-b", "m-a"})
	if gotA != "FIRST" || gotB != "FIRST" {
		t.Fatalf("mask selection not order-independent: %q %q", gotA, gotB)
	}
}

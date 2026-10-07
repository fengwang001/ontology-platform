package ontology_test

import (
	"fmt"
	"testing"

	ont "ontology/ontology"
)

func openEngine(t *testing.T, mode ont.MergeMode) (*ont.Engine, *ont.ObjectType) {
	t.Helper()
	ot := mustType(t)
	inst, _ := ot.NewInstance("p", map[string]ont.RawValue{"id": int64(1), "ssn": "x", "score": float64(1)})
	rows := ont.RowPolicySet{Mode: mode, Rules: []ont.RowRule{
		{ID: "ra", Selector: ont.SubjectSelector{MatchAll: true}, Effect: ont.EffectAllow},
		{ID: "rd", Selector: ont.SubjectSelector{MatchAll: true}, Effect: ont.EffectDeny},
	}}
	props := ont.PropPolicySet{Mode: mode, Rules: []ont.PropRule{
		{ID: "pa", Property: "id", Selector: ont.SubjectSelector{MatchAll: true}, HasRead: true, Readable: true},
		{ID: "pd", Property: "id", Selector: ont.SubjectSelector{MatchAll: true}, HasRead: true, Readable: false},
	}}
	eng, err := ont.NewEngine(ont.Config{Type: ot, Rows: rows, Props: props})
	if err != nil {
		t.Fatal(err)
	}
	_ = eng.AddInstance(inst)
	return eng, ot
}

// Contradictory allow+deny under both merge modes must be deterministic.
func TestContradictoryMergeModes(t *testing.T) {
	// Allow-overrides: visible, id raw.
	engA, _ := openEngine(t, ont.AllowOverrides)
	res, derr := engA.Read(ont.Subject{ID: "u"}, "p", nil)
	if derr != nil {
		t.Fatalf("allow-overrides should be visible: %v", derr)
	}
	if _, ok := res.View["id"]; !ok {
		t.Fatalf("allow-overrides: id should be raw readable, redacted=%v", res.Redacted)
	}
	// Deny-overrides: row deny hides the instance entirely.
	engD, _ := openEngine(t, ont.DenyOverrides)
	if _, derr := engD.Read(ont.Subject{ID: "u"}, "p", nil); derr == nil || derr.Kind != ont.ErrInstanceNotFound {
		t.Fatalf("deny-overrides should hide instance, got %v", derr)
	}
}

// Masked value violating the declared type contract is reported and
// does not corrupt uninvolved reads.
func TestMaskedTypeConflict(t *testing.T) {
	ot := mustType(t)
	inst, _ := ot.NewInstance("p", map[string]ont.RawValue{"id": int64(1), "ssn": "x"})
	rows := ont.RowPolicySet{Mode: ont.AllowOverrides, Rules: []ont.RowRule{
		{ID: "ra", Selector: ont.SubjectSelector{MatchAll: true}, Effect: ont.EffectAllow},
	}}
	props := ont.PropPolicySet{Mode: ont.AllowOverrides, Rules: []ont.PropRule{
		{ID: "p-id", Property: "id", Selector: ont.SubjectSelector{MatchAll: true}, HasRead: true, Readable: true},
		{ID: "p-ssn", Property: "ssn", Selector: ont.SubjectSelector{MatchAll: true},
			Mask: func(_ ont.Subject, _ ont.RawValue) (ont.RawValue, error) { return int64(123), nil }},
	}}
	eng, _ := ont.NewEngine(ont.Config{Type: ot, Rows: rows, Props: props})
	_ = eng.AddInstance(inst)

	okRes, derr := eng.Read(ont.Subject{ID: "u"}, "p", []string{"id"})
	if derr != nil || okRes.View["id"] != int64(1) {
		t.Fatalf("uninvolved read must succeed: res=%v err=%v", okRes, derr)
	}
	res, derr := eng.Read(ont.Subject{ID: "u"}, "p", []string{"ssn"})
	if derr == nil || derr.Kind != ont.ErrMaskedTypeViolation {
		t.Fatalf("expected ErrMaskedTypeViolation, got %v", derr)
	}
	if res.View != nil || res.Conflict == nil {
		t.Fatalf("conflicting view must be withheld, got %+v", res)
	}
}

func writeEngine(t *testing.T, wm ont.WriteMode) *ont.Engine {
	t.Helper()
	ot := mustType(t)
	inst, _ := ot.NewInstance("p", map[string]ont.RawValue{"id": int64(1), "score": float64(1)})
	rows := ont.RowPolicySet{Mode: ont.AllowOverrides, Rules: []ont.RowRule{
		{ID: "ra", Selector: ont.SubjectSelector{Users: []string{"w"}}, Effect: ont.EffectAllow},
	}}
	props := ont.PropPolicySet{Mode: ont.AllowOverrides, Rules: []ont.PropRule{
		{ID: "p-id", Property: "id", Selector: ont.SubjectSelector{MatchAll: true}, HasWrite: true, Writable: true, HasRead: true, Readable: true},
		{ID: "p-score", Property: "score", Selector: ont.SubjectSelector{MatchAll: true}, HasWrite: true, Writable: false, HasRead: true, Readable: true},
	}}
	eng, err := ont.NewEngine(ont.Config{Type: ot, Rows: rows, Props: props, WriteMode: wm})
	if err != nil {
		t.Fatal(err)
	}
	_ = eng.AddInstance(inst)
	return eng
}

// Write error priority + rejection atomicity.
func TestWriteErrorPriorityAndAtomicity(t *testing.T) {
	eng := writeEngine(t, ont.WriteRejectAll)
	_, derr := eng.Write(ont.Subject{ID: "x"}, "p", map[string]ont.RawValue{"id": "nope"})
	if derr == nil || derr.Kind != ont.ErrInstanceNotFound {
		t.Fatalf("invisible write must be opaque not-found, got %v", derr)
	}
	_, derr = eng.Write(ont.Subject{ID: "w"}, "p", map[string]ont.RawValue{"id": int64(2), "score": float64(2)})
	if derr == nil || derr.Kind != ont.ErrPropertyNotWritable {
		t.Fatalf("expected ErrPropertyNotWritable, got %v", derr)
	}
	res, _ := eng.Read(ont.Subject{ID: "w"}, "p", []string{"id"})
	if res.View["id"] != int64(1) {
		t.Fatalf("rejected write mutated id, got %v", res.View["id"])
	}
	if res.Trace.RowOutcome != "allow" {
		t.Fatalf("trace should show allow, got %+v", res.Trace)
	}
}

// Drop mode skips unwritable fields and applies the rest.
func TestWriteDropMode(t *testing.T) {
	eng := writeEngine(t, ont.WriteDrop)
	out, derr := eng.Write(ont.Subject{ID: "w"}, "p", map[string]ont.RawValue{
		"score": float64(5), "id": int64(7),
	})
	if derr != nil {
		t.Fatal(derr)
	}
	if !contains(out.Applied, "id") || !contains(out.Dropped, "score") {
		t.Fatalf("unexpected apply/drop: %+v", out)
	}
	res, _ := eng.Read(ont.Subject{ID: "w"}, "p", nil)
	if res.View["id"] != int64(7) || res.View["score"] != float64(1) {
		t.Fatalf("score must be untouched, got %+v", res.View)
	}
}

// Registration order and request order independence.
func TestOrderIndependence(t *testing.T) {
	mk := func(order []string) (*ont.Engine, string) {
		ot := mustType(t)
		inst, _ := ot.NewInstance("p", map[string]ont.RawValue{"id": int64(3), "score": float64(2), "ssn": "z"})
		ruleOf := func(id string) ont.PropRule {
			switch id {
			case "m1":
				return ont.PropRule{ID: "m1", Property: "ssn", Selector: ont.SubjectSelector{MatchAll: true},
					Mask: func(_ ont.Subject, _ ont.RawValue) (ont.RawValue, error) { return "A", nil }}
			case "m2":
				return ont.PropRule{ID: "m2", Property: "ssn", Selector: ont.SubjectSelector{MatchAll: true},
					Mask: func(_ ont.Subject, _ ont.RawValue) (ont.RawValue, error) { return "B", nil }}
			case "r-id":
				return ont.PropRule{ID: "r-id", Property: "id", Selector: ont.SubjectSelector{MatchAll: true}, HasRead: true, Readable: true}
			default:
				return ont.PropRule{ID: "r-score", Property: "score", Selector: ont.SubjectSelector{MatchAll: true}, HasRead: true, Readable: true}
			}
		}
		rules := make([]ont.PropRule, 0, len(order))
		for _, id := range order {
			rules = append(rules, ruleOf(id))
		}
		rows := ont.RowPolicySet{Mode: ont.AllowOverrides, Rules: []ont.RowRule{
			{ID: "ra", Selector: ont.SubjectSelector{MatchAll: true}, Effect: ont.EffectAllow},
		}}
		props := ont.PropPolicySet{Mode: ont.AllowOverrides, Rules: rules}
		eng, err := ont.NewEngine(ont.Config{Type: ot, Rows: rows, Props: props})
		if err != nil {
			t.Fatal(err)
		}
		_ = eng.AddInstance(inst)
		res, derr := eng.Read(ont.Subject{ID: "u"}, "p", []string{"score", "ssn", "id"})
		if derr != nil {
			t.Fatal(err)
		}
		return eng, fmt.Sprintf("%v|%v|%v", res.View["ssn"], res.View["id"], res.View["score"])
	}
	_, v1 := mk([]string{"m2", "m1", "r-id", "r-score"})
	_, v2 := mk([]string{"r-score", "m1", "r-id", "m2"})
	if v1 != "A|3|2" || v1 != v2 {
		t.Fatalf("mask winner/order unstable: %q vs %q", v1, v2)
	}
}

package ontology_test

import (
	"testing"

	ont "ontology/ontology"
)

func mustType(t *testing.T) *ont.ObjectType {
	t.Helper()
	ot, err := ont.NewObjectType("Person", []ont.PropertyDecl{
		{Name: "id", Type: ont.TypeInt},
		{Name: "ssn", Type: ont.TypeString},
		{Name: "score", Type: ont.TypeFloat},
		{Name: "flag", Type: ont.TypeBool},
	})
	if err != nil {
		t.Fatalf("type: %v", err)
	}
	return ot
}

func contains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}

// Three-way distinguishability: absent vs present zero vs undeclared.
func TestThreeWayDistinction(t *testing.T) {
	ot := mustType(t)
	inst, err := ot.NewInstance("p1", map[string]ont.RawValue{"id": int64(0), "ssn": ""})
	if err != nil {
		t.Fatal(err)
	}
	rows := ont.RowPolicySet{Mode: ont.AllowOverrides, Rules: []ont.RowRule{
		{ID: "ra", Selector: ont.SubjectSelector{MatchAll: true}, Effect: ont.EffectAllow},
	}}
	props := ont.PropPolicySet{Mode: ont.AllowOverrides, Rules: []ont.PropRule{
		{ID: "pa", Property: "id", Selector: ont.SubjectSelector{MatchAll: true}, HasRead: true, Readable: true, HasWrite: true, Writable: true},
		{ID: "pb", Property: "ssn", Selector: ont.SubjectSelector{MatchAll: true}, HasRead: true, Readable: true, HasWrite: true, Writable: true},
		{ID: "pc", Property: "score", Selector: ont.SubjectSelector{MatchAll: true}, HasRead: true, Readable: true, HasWrite: true, Writable: true},
	}}
	eng, err := ont.NewEngine(ont.Config{Type: ot, Rows: rows, Props: props})
	if err != nil {
		t.Fatal(err)
	}
	if err := eng.AddInstance(inst); err != nil {
		t.Fatal(err)
	}
	res, derr := eng.Read(ont.Subject{ID: "u"}, "p1", nil)
	if derr != nil {
		t.Fatalf("read: %v", derr)
	}
	if v, ok := res.View["id"]; !ok || v != int64(0) {
		t.Fatalf("id zero value must be present, got %v ok=%v", v, ok)
	}
	if v, ok := res.View["ssn"]; !ok || v != "" {
		t.Fatalf("ssn empty string must be present, got %v ok=%v", v, ok)
	}
	if _, ok := res.View["score"]; ok {
		t.Fatal("absent score must not appear in View")
	}
	if !contains(res.Absent, "score") {
		t.Fatalf("score must be listed Absent, got %+v", res.Absent)
	}
	if _, derr := eng.Read(ont.Subject{ID: "u"}, "p1", []string{"nope"}); derr == nil || derr.Kind != ont.ErrUnknownProperty {
		t.Fatalf("expected ErrUnknownProperty, got %v", derr)
	}
}

// Row predicates run on RAW values; masked/hidden values never leak.
func TestRowPredicateUsesRawValue(t *testing.T) {
	ot := mustType(t)
	inst, _ := ot.NewInstance("p1", map[string]ont.RawValue{
		"id": int64(42), "ssn": "secret", "score": float64(9),
	})
	rows := ont.RowPolicySet{Mode: ont.AllowOverrides, Rules: []ont.RowRule{
		{ID: "r-secret", Selector: ont.SubjectSelector{Users: []string{"clerk"}}, Effect: ont.EffectAllow,
			Predicates: []ont.Predicate{{Property: "ssn", Op: ont.CmpEq, Value: "secret"}}},
	}}
	props := ont.PropPolicySet{Mode: ont.AllowOverrides, Rules: []ont.PropRule{
		{ID: "p-id", Property: "id", Selector: ont.SubjectSelector{MatchAll: true}, HasRead: true, Readable: true},
		{ID: "p-ssn", Property: "ssn", Selector: ont.SubjectSelector{MatchAll: true},
			Mask: func(_ ont.Subject, _ ont.RawValue) (ont.RawValue, error) { return "***", nil }},
		{ID: "p-score", Property: "score", Selector: ont.SubjectSelector{MatchAll: true}, HasRead: true, Readable: false},
	}}
	eng, _ := ont.NewEngine(ont.Config{Type: ot, Rows: rows, Props: props})
	_ = eng.AddInstance(inst)

	res, derr := eng.Read(ont.Subject{ID: "clerk"}, "p1", nil)
	if derr != nil {
		t.Fatalf("clerk visible via raw ssn predicate, got %v", derr)
	}
	if res.View["ssn"] != "***" {
		t.Fatalf("ssn must be masked in view, got %v", res.View["ssn"])
	}
	if _, ok := res.View["score"]; ok {
		t.Fatal("score raw value leaked")
	}
	if !contains(res.Redacted, "score") {
		t.Fatalf("score must be Redacted, got %v", res.Redacted)
	}
	if _, derr := eng.Read(ont.Subject{ID: "other"}, "p1", nil); derr == nil || derr.Kind != ont.ErrInstanceNotFound {
		t.Fatalf("hidden instance must look like not found, got %v", derr)
	}
}

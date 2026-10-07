package ontology_test

import (
	"testing"

	ont "ontology/ontology"
)

func TestTraceRecordsRuleBasis(t *testing.T) {
	ot := mustType(t)
	inst, _ := ot.NewInstance("p", map[string]ont.RawValue{"id": int64(1), "ssn": "z"})
	rows := ont.RowPolicySet{Mode: ont.AllowOverrides, Rules: []ont.RowRule{
		{ID: "r1", Selector: ont.SubjectSelector{Users: []string{"u"}}, Effect: ont.EffectAllow,
			Predicates: []ont.Predicate{{Property: "id", Op: ont.CmpGe, Value: int64(0)}}},
	}}
	props := ont.PropPolicySet{Mode: ont.AllowOverrides, Rules: []ont.PropRule{
		{ID: "allow-id", Property: "id", Selector: ont.SubjectSelector{MatchAll: true}, HasRead: true, Readable: true},
		{ID: "mask-a", Property: "ssn", Selector: ont.SubjectSelector{MatchAll: true},
			Mask: func(_ ont.Subject, _ ont.RawValue) (ont.RawValue, error) { return "A", nil }},
		{ID: "mask-b", Property: "ssn", Selector: ont.SubjectSelector{MatchAll: true},
			Mask: func(_ ont.Subject, _ ont.RawValue) (ont.RawValue, error) { return "B", nil }},
	}}
	eng, _ := ont.NewEngine(ont.Config{Type: ot, Rows: rows, Props: props})
	_ = eng.AddInstance(inst)
	res, derr := eng.Read(ont.Subject{ID: "u"}, "p", nil)
	if derr != nil {
		t.Fatal(derr)
	}
	basis := map[string]ont.RuleBasis{}
	for _, b := range res.Trace.MatchedPropRules {
		basis[b.Property] = b
	}
	if id, ok := basis["id"]; !ok || id.RuleID != "allow-id" || id.Presented != "raw" {
		t.Fatalf("id basis wrong: %+v", basis)
	}
	if ssn, ok := basis["ssn"]; !ok || ssn.RuleID != "mask-a" || ssn.Presented != "masked" {
		t.Fatalf("ssn basis must name smallest-id mask winner, got %+v", basis)
	}
	if res.View["ssn"] != "A" {
		t.Fatalf("smallest-id mask must present A, got %v", res.View["ssn"])
	}
	if len(res.Trace.MatchedRowRules) != 1 || res.Trace.MatchedRowRules[0].RuleID != "r1" {
		t.Fatalf("row basis wrong: %+v", res.Trace.MatchedRowRules)
	}
	if res.Trace.RowRulesEvaluated != 1 || res.Trace.PropRulesEvaluated != 3 {
		t.Fatalf("evaluation counts wrong: %+v", res.Trace)
	}
}

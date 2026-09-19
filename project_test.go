package projection

import (
	"reflect"
	"testing"
)

func sampleObject() map[string]any {
	return map[string]any{
		"name":  "Alice",
		"email": "a@example.com",
		"ssn":   "000",
		"addr": map[string]any{
			"city": "NYC",
			"zip":  "10001",
			"geo": map[string]any{
				"lat": 40.7,
				"lng": -74.0,
			},
		},
		"tags": []any{"x", "y"},
	}
}

func TestProjectBasicPruning(t *testing.T) {
	rs, _ := Compile(Config{DefaultAllow: true, Deny: []string{"ssn", "email"}})
	out, err := rs.Project(sampleObject(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := out["ssn"]; ok {
		t.Fatal("ssn must be pruned")
	}
	if _, ok := out["email"]; ok {
		t.Fatal("email must be pruned")
	}
	if out["name"] != "Alice" {
		t.Fatal("name must survive")
	}
}

func TestRequiredFieldHiddenReportsFieldAndRule(t *testing.T) {
	rs, _ := Compile(Config{DefaultAllow: true, Deny: []string{"ssn"}})
	schema := &Schema{Fields: map[string]FieldSpec{"ssn": {Required: true}}}
	_, err := rs.Project(sampleObject(), schema)
	pe, ok := AsProjectionError(err)
	if !ok || pe.Kind != ErrorRequiredHidden {
		t.Fatalf("expected required-hidden error, got %v", err)
	}
	if pe.Field != "ssn" || pe.RuleRaw != "ssn" {
		t.Fatalf("error must name field ssn and rule ssn, got %+v", pe)
	}
}

func TestRequiredNestedHiddenByAncestor(t *testing.T) {
	rs, _ := Compile(Config{Allow: []string{"name"}, Deny: []string{"addr"}})
	schema := &Schema{Fields: map[string]FieldSpec{"addr.city": {Required: true}}}
	_, err := rs.Project(sampleObject(), schema)
	pe, ok := AsProjectionError(err)
	if !ok || pe.Kind != ErrorRequiredHidden || pe.Field != "addr.city" {
		t.Fatalf("nested required field must be reported, got %v", err)
	}
	if pe.RuleRaw != "addr" {
		t.Fatalf("must cite ancestor rule addr, got %q", pe.RuleRaw)
	}
}

func TestRequiredAbsentInSourceIsNotAnError(t *testing.T) {
	rs, _ := Compile(Config{DefaultAllow: true})
	schema := &Schema{Fields: map[string]FieldSpec{"missing": {Required: true}}}
	out, err := rs.Project(sampleObject(), schema)
	if err != nil {
		t.Fatalf("required field absent in source must not error: %v", err)
	}
	if _, ok := out["missing"]; ok {
		t.Fatal("absent field must not appear")
	}
}

func TestDependencyHideResultPolicy(t *testing.T) {
	rs, _ := Compile(Config{
		DefaultAllow: true,
		Deny:         []string{"ssn"},
	})
	schema := &Schema{
		DependencyPolicy: DependencyHideResult,
		Fields: map[string]FieldSpec{
			"ssnHash": {ComputedFrom: "ssn"},
		},
	}
	obj := sampleObject()
	obj["ssnHash"] = "hash"
	out, err := rs.Project(obj, schema)
	if err != nil {
		t.Fatalf("hide policy must not error: %v", err)
	}
	if _, ok := out["ssnHash"]; ok {
		t.Fatal("computed result must be hidden with its source")
	}
	if _, ok := out["name"]; !ok {
		t.Fatal("unrelated fields must survive")
	}
}

func TestDependencyErrorPolicy(t *testing.T) {
	rs, _ := Compile(Config{
		DefaultAllow: true,
		Deny:         []string{"ssn"},
	})
	schema := &Schema{
		DependencyPolicy: DependencyError,
		Fields: map[string]FieldSpec{
			"ssnHash": {ComputedFrom: "ssn"},
		},
	}
	obj := sampleObject()
	obj["ssnHash"] = "hash"
	_, err := rs.Project(obj, schema)
	pe, ok := AsProjectionError(err)
	if !ok || pe.Kind != ErrorDependencyBroken {
		t.Fatalf("expected dependency error, got %v", err)
	}
	if pe.Field != "ssnHash" || pe.SourceField != "ssn" || pe.SourceRuleRaw != "ssn" {
		t.Fatalf("dependency error must name result and source rule, got %+v", pe)
	}
}

func TestDependencyVisibleSourceKeepsResult(t *testing.T) {
	rs, _ := Compile(Config{DefaultAllow: true, Deny: []string{"email"}})
	schema := &Schema{
		DependencyPolicy: DependencyError,
		Fields: map[string]FieldSpec{
			"ssnHash": {ComputedFrom: "ssn"},
		},
	}
	obj := sampleObject()
	obj["ssnHash"] = "hash"
	out, err := rs.Project(obj, schema)
	if err != nil {
		t.Fatal(err)
	}
	if out["ssnHash"] != "hash" {
		t.Fatal("computed result stays when source is visible")
	}
}

func TestSliceDeepCopy(t *testing.T) {
	rs, _ := Compile(Config{DefaultAllow: true})
	out, err := rs.Project(sampleObject(), nil)
	if err != nil {
		t.Fatal(err)
	}
	out["tags"].([]any)[0] = "mutated"
	if sampleObject()["tags"].([]any)[0] != "x" {
		t.Fatal("mutating result slice must not affect source")
	}
	if !reflect.DeepEqual(out["name"], "Alice") {
		t.Fatal("sanity")
	}
}

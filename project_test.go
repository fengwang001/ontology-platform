package ontology

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

func sampleObject() map[string]any {
	return map[string]any{
		"id":     "o-1",
		"name":   "alice",
		"secret": "s3cr3t",
		"addr": map[string]any{
			"city": "SH",
			"zip":  "200000",
			"geo":  map[string]any{"lat": 31.2, "lng": 121.5},
		},
		"tags": []any{"a", "b"},
	}
}

func mustProject(t *testing.T, obj map[string]any, rs *RuleSet, sch *Schema) map[string]any {
	t.Helper()
	out, err := Project(obj, rs, sch)
	if err != nil {
		t.Fatalf("Project: %v", err)
	}
	return out
}

func TestProjectBasicAllowList(t *testing.T) {
	rs := mustCompile(t, []string{"id", "name", "addr.city", "tags"}, nil)
	out := mustProject(t, sampleObject(), rs, nil)
	want := map[string]any{
		"id":   "o-1",
		"name": "alice",
		"addr": map[string]any{"city": "SH"},
		"tags": []any{"a", "b"},
	}
	if !reflect.DeepEqual(out, want) {
		t.Errorf("got %v, want %v", out, want)
	}
}

func TestRequiredFieldCutByDenyRule(t *testing.T) {
	rs := mustCompile(t, nil, []string{"name"})
	_, err := Project(sampleObject(), rs, &Schema{Required: []string{"id", "name"}})
	var reqErr *RequiredFieldError
	if !errors.As(err, &reqErr) {
		t.Fatalf("expected RequiredFieldError, got %v", err)
	}
	if reqErr.Path != "name" || reqErr.Rule != "name" {
		t.Errorf("error should locate field and rule, got %+v", reqErr)
	}
	if !strings.Contains(err.Error(), `"name"`) {
		t.Errorf("error message should name the field and rule: %v", err)
	}
}

func TestRequiredFieldCutByDefaultDeny(t *testing.T) {
	rs := mustCompile(t, []string{"id"}, nil)
	_, err := Project(sampleObject(), rs, &Schema{Required: []string{"name"}})
	var reqErr *RequiredFieldError
	if !errors.As(err, &reqErr) {
		t.Fatalf("expected RequiredFieldError, got %v", err)
	}
	if reqErr.Rule != "" || reqErr.Reason != ReasonDefaultDeny {
		t.Errorf("expected default-deny explanation, got %+v", reqErr)
	}
}

func TestDependencyHideDependentPolicy(t *testing.T) {
	obj := map[string]any{"raw": 1, "computed": 2}
	rs := mustCompile(t, nil, []string{"raw"})
	sch := &Schema{
		Dependencies: []Dependency{{Result: "computed", Source: "raw"}},
		Policy:       HideDependent,
	}
	out := mustProject(t, obj, rs, sch)
	if _, ok := out["computed"]; ok {
		t.Error("dependent field should be hidden with its source")
	}
}

func TestDependencyFailPolicy(t *testing.T) {
	obj := map[string]any{"raw": 1, "computed": 2}
	rs := mustCompile(t, nil, []string{"raw"})
	sch := &Schema{
		Dependencies: []Dependency{{Result: "computed", Source: "raw"}},
		Policy:       FailOnHiddenSource,
	}
	_, err := Project(obj, rs, sch)
	var depErr *DependencyError
	if !errors.As(err, &depErr) {
		t.Fatalf("expected DependencyError, got %v", err)
	}
	if depErr.Result != "computed" || depErr.Source != "raw" {
		t.Errorf("unexpected DependencyError: %+v", depErr)
	}
}

func TestDependencyBothVisibleIsFine(t *testing.T) {
	obj := map[string]any{"raw": 1, "computed": 2}
	rs := mustCompile(t, nil, nil)
	sch := &Schema{
		Dependencies: []Dependency{{Result: "computed", Source: "raw"}},
		Policy:       FailOnHiddenSource,
	}
	out := mustProject(t, obj, rs, sch)
	if len(out) != 2 {
		t.Errorf("both fields should survive, got %v", out)
	}
}

func TestWildcardDoesNotCrossLayers(t *testing.T) {
	rs := mustCompile(t, []string{"addr.*"}, nil)
	out := mustProject(t, sampleObject(), rs, nil)
	addr, ok := out["addr"].(map[string]any)
	if !ok {
		t.Fatalf("addr should survive with direct children, got %v", out)
	}
	if addr["city"] != "SH" {
		t.Error("addr.city should be allowed by addr.*")
	}
	if _, ok := addr["geo"]; ok {
		t.Error("addr.geo should vanish: addr.* must not allow addr.geo.lat")
	}
}

func TestAncestorDenyHidesExplicitlyAllowedDescendant(t *testing.T) {
	rs := mustCompile(t, []string{"addr.geo.lat"}, []string{"addr"})
	out := mustProject(t, sampleObject(), rs, nil)
	if _, ok := out["addr"]; ok {
		t.Errorf("denied parent subtree must disappear, got %v", out["addr"])
	}
}

func TestEmptyNestedObjectDisappears(t *testing.T) {
	rs := mustCompile(t, nil, []string{"addr.geo.lat", "addr.geo.lng"})
	out := mustProject(t, sampleObject(), rs, nil)
	addr, ok := out["addr"].(map[string]any)
	if !ok {
		t.Fatalf("addr should survive, got %v", out)
	}
	if addr["city"] != "SH" {
		t.Error("addr.city should remain")
	}
	if _, ok := addr["geo"]; ok {
		t.Error("empty addr.geo should be pruned, not left as empty object")
	}
}

func TestResultIsolationFromOriginal(t *testing.T) {
	obj := sampleObject()
	snapshot := sampleObject()
	rs := mustCompile(t, nil, []string{"secret"})
	out := mustProject(t, obj, rs, nil)
	out["addr"].(map[string]any)["city"] = "HACKED"
	out["addr"].(map[string]any)["geo"].(map[string]any)["lat"] = 0.0
	out["tags"].([]any)[0] = "HACKED"
	delete(out, "id")
	if !reflect.DeepEqual(obj, snapshot) {
		t.Errorf("original mutated via result: %v", obj)
	}
}

func TestOriginalIsolationFromProjection(t *testing.T) {
	obj := sampleObject()
	rs := mustCompile(t, nil, nil)
	out := mustProject(t, obj, rs, nil)
	obj["addr"].(map[string]any)["city"] = "HACKED"
	obj["tags"].([]any)[0] = "HACKED"
	if out["addr"].(map[string]any)["city"] != "SH" {
		t.Error("result should be isolated from later mutations of original")
	}
	if out["tags"].([]any)[0] != "a" {
		t.Error("result slice should be an independent copy")
	}
}

func TestCompiledRuleSetReusableAcrossObjects(t *testing.T) {
	rs := mustCompile(t, []string{"id"}, nil)
	for _, obj := range []map[string]any{
		{"id": "a", "x": 1},
		{"id": "b", "y": 2},
	} {
		out := mustProject(t, obj, rs, nil)
		if len(out) != 1 {
			t.Errorf("expected only id, got %v", out)
		}
	}
}

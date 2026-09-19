package ontology

import (
	"errors"
	"testing"
)

func registerEcho(t *testing.T, e *Engine, run ActionFunc) {
	t.Helper()
	err := e.Register(&ActionType{
		Name: "Echo",
		Params: []ParamSpec{
			{Name: "name", Type: TypeString, Required: true},
			{Name: "age", Type: TypeInt, Default: 30},
			{Name: "tags", Type: TypeList, Default: []any{"new"}},
		},
		ObjectTypes: []string{"User"},
		Run:         run,
	})
	if err != nil {
		t.Fatalf("register: %v", err)
	}
}

func TestValidateReportsAllIssuesAtOnce(t *testing.T) {
	e := NewEngine()
	registerEcho(t, e, func(*Tx, map[string]any) error { return nil })

	_, err := e.Execute("Echo", map[string]any{
		"age":   "thirty", // wrong type
		"extra": 1,        // undeclared
		// "name" missing
	})
	var perr *ParamErrors
	if !errors.As(err, &perr) {
		t.Fatalf("want *ParamErrors, got %T: %v", err, err)
	}
	if len(perr.Issues) != 3 {
		t.Fatalf("want 3 issues reported at once, got %d: %v", len(perr.Issues), perr)
	}
	kinds := map[ParamIssueKind]bool{}
	byName := map[string]ParamIssue{}
	for _, is := range perr.Issues {
		kinds[is.Kind] = true
		byName[is.Name] = is
	}
	for _, k := range []ParamIssueKind{IssueMissing, IssueType, IssueUnknown} {
		if !kinds[k] {
			t.Errorf("missing issue kind %v in %v", k, perr.Issues)
		}
	}
	if byName["name"].Kind != IssueMissing {
		t.Errorf("name: want missing, got %v", byName["name"])
	}
	if byName["age"].Kind != IssueType || byName["age"].Want != TypeInt {
		t.Errorf("age: want type issue, got %v", byName["age"])
	}
	if byName["extra"].Kind != IssueUnknown {
		t.Errorf("extra: want unknown, got %v", byName["extra"])
	}
}

func TestDefaultFilledAndNeverPolluted(t *testing.T) {
	e := NewEngine()
	var seen [][]any
	registerEcho(t, e, func(_ *Tx, p map[string]any) error {
		tags := p["tags"].([]any)
		seen = append(seen, append([]any(nil), tags...))
		tags[0] = "POLLUTED" // caller mutates the normalized default
		if p["age"] != 30 {
			t.Errorf("default age: want 30, got %v", p["age"])
		}
		return nil
	})

	for i := 0; i < 3; i++ {
		if _, err := e.Execute("Echo", map[string]any{"name": "a"}); err != nil {
			t.Fatalf("execute %d: %v", i, err)
		}
	}
	for i, tags := range seen {
		if len(tags) != 1 || tags[0] != "new" {
			t.Fatalf("call %d saw polluted default: %v", i, tags)
		}
	}
	recs := e.Records(1, 100)
	if len(recs) != 3 {
		t.Fatalf("want 3 records, got %d", len(recs))
	}
	for _, r := range recs {
		if r.Params["tags"].([]any)[0] != "new" {
			t.Fatalf("record %d holds polluted default: %v", r.Seq, r.Params)
		}
	}
}

func TestUnknownActionRejected(t *testing.T) {
	e := NewEngine()
	if _, err := e.Execute("Nope", nil); err == nil {
		t.Fatal("want error for unknown action")
	}
}

func TestRegisterValidation(t *testing.T) {
	e := NewEngine()
	if err := e.Register(&ActionType{Run: func(*Tx, map[string]any) error { return nil }}); err == nil {
		t.Fatal("want error for empty name")
	}
	if err := e.Register(&ActionType{Name: "X"}); err == nil {
		t.Fatal("want error for missing Run")
	}
	a := &ActionType{Name: "X", Run: func(*Tx, map[string]any) error { return nil }}
	if err := e.Register(a); err != nil {
		t.Fatalf("register: %v", err)
	}
	if err := e.Register(a); err == nil {
		t.Fatal("want error for duplicate registration")
	}
}

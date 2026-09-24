package policy

import (
	"errors"
	"testing"
)

func TestGrantAndVisible(t *testing.T) {
	p := New()
	p.Grant("analyst", "id", "name", "dept")
	got, err := p.Visible("analyst")
	if err != nil || len(got) != 3 || !got["id"] || !got["name"] || !got["dept"] {
		t.Fatalf("Visible(analyst) = %v, %v", got, err)
	}
	got["injected"] = true
	again, _ := p.Visible("analyst")
	if again["injected"] {
		t.Error("Visible must return a copy, not the live set")
	}
	p.Grant("analyst", "id")
	replaced, _ := p.Visible("analyst")
	if len(replaced) != 1 || !replaced["id"] {
		t.Errorf("Grant must replace the set, got %v", replaced)
	}
}

func TestUnknownRole(t *testing.T) {
	p := New()
	if _, err := p.Visible("ghost"); !errors.Is(err, ErrUnknownRole) {
		t.Errorf("got %v, want ErrUnknownRole", err)
	}
	p.Grant("auditor")
	empty, err := p.Visible("auditor")
	if err != nil || len(empty) != 0 {
		t.Errorf("explicit empty grant = %v, %v; want empty set, nil error", empty, err)
	}
}

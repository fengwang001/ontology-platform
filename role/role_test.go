package role

import (
	"errors"
	"strings"
	"testing"
)

func TestMatchPattern(t *testing.T) {
	cases := []struct {
		pattern string
		name    string
		want    bool
	}{
		{"*", "anything", true},
		{"*", "", true},
		{"logs-*", "logs-1", true},
		{"logs-*", "logs-", true},
		{"logs-*", "logx-1", false},
		{"logs-1", "logs-1", true},
		{"logs-1", "logs-2", false},
		{"ab*", "ab", true},
	}
	for _, c := range cases {
		if got := MatchPattern(c.pattern, c.name); got != c.want {
			t.Errorf("MatchPattern(%q,%q)=%v want %v", c.pattern, c.name, got, c.want)
		}
	}
}

func TestValidPattern(t *testing.T) {
	bad := []string{"", "a*b", "**", "*x", strings.Repeat("a", 65)}
	for _, p := range bad {
		if validPattern(p) {
			t.Errorf("validPattern(%q) = true, want false", p)
		}
	}
	good := []string{"a", "*", "a*", "logs-*", strings.Repeat("a", 64)}
	for _, p := range good {
		if !validPattern(p) {
			t.Errorf("validPattern(%q) = false, want true", p)
		}
	}
}

func TestEvalMissingAndMismatch(t *testing.T) {
	fields := map[string]Value{"team": "a", "level": int64(3)}
	term, _ := Term("secret", "k1")
	rng, _ := Range("level", 5, 9)
	termBadType, _ := Term("level", "3")
	rngOnString, _ := Range("team", 1, 2)
	if Eval(term, fields) {
		t.Fatal("missing field Term must be false")
	}
	if Eval(rng, fields) {
		t.Fatal("out-of-range Range must be false")
	}
	if got := Eval(mustNot(t, term), fields); !got {
		t.Fatal("Not over missing field must be true")
	}
	if Eval(termBadType, fields) {
		t.Fatal("Term with mismatched value type must be false")
	}
	if Eval(rngOnString, fields) {
		t.Fatal("Range over string must be false")
	}
}

func mustNot(t *testing.T, e Expr) Expr {
	t.Helper()
	n, err := Not(e)
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func TestInvalidExprs(t *testing.T) {
	bad := []Expr{
		{Op: OpTerm, Field: "", Value: "x"},
		{Op: OpTerm, Field: "f", Value: 3},
		{Op: OpRange, Field: "f", Lo: 9, Hi: 1},
		{Op: OpAnd},
		{Op: OpOr, Children: make([]Expr, 9)},
		{Op: OpNot},
	}
	for i, e := range bad {
		if err := ValidateExpr(e); !errors.Is(err, ErrInvalid) {
			t.Fatalf("bad expr #%d: err=%v want ErrInvalid", i, err)
		}
	}
	deep := Expr{Op: OpTerm, Field: "f", Value: int64(1)}
	for i := 0; i < maxDepth; i++ {
		deep = Expr{Op: OpAnd, Children: []Expr{deep}}
	}
	if err := ValidateExpr(deep); !errors.Is(err, ErrInvalid) {
		t.Fatalf("depth 9 err=%v want ErrInvalid", err)
	}
	ok := Expr{Op: OpTerm, Field: "f", Value: int64(1)}
	for i := 0; i < maxDepth-1; i++ {
		ok = Expr{Op: OpAnd, Children: []Expr{ok}}
	}
	if err := ValidateExpr(ok); err != nil {
		t.Fatalf("depth 8 must be valid: %v", err)
	}
}

func entry(pattern string, filter *Expr, fa FieldAuth) Entry {
	return Entry{Pattern: pattern, Filter: filter, Fields: fa}
}

func TestRoleLifecycle(t *testing.T) {
	r := NewRegistry()
	e1 := entry("logs-*", nil, FieldAuth{Grant: []string{"team"}})
	if err := r.PutRole("R1", []Entry{e1}); err != nil {
		t.Fatal(err)
	}
	if err := r.PutRole("", []Entry{e1}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("bad name err=%v", err)
	}
	if err := r.PutRole("R2", nil); !errors.Is(err, ErrInvalid) {
		t.Fatalf("no entries err=%v", err)
	}
	if err := r.DeleteRole("ghost"); !errors.Is(err, ErrRoleNotFound) {
		t.Fatalf("delete missing err=%v", err)
	}
	if err := r.BindUser("u", []string{"R1"}); err != nil {
		t.Fatal(err)
	}
	if err := r.DeleteRole("R1"); !errors.Is(err, ErrRoleInUse) {
		t.Fatalf("delete bound err=%v", err)
	}
	if err := r.BindUser("u", []string{"R1", "R1"}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("dup role err=%v", err)
	}
	if err := r.BindUser("u2", []string{"nope"}); !errors.Is(err, ErrRoleNotFound) {
		t.Fatalf("bind missing role err=%v", err)
	}
	if err := r.BindUser("u", []string{}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("empty bind err=%v", err)
	}
	if err := r.BindUser("u", nil); !errors.Is(err, ErrInvalid) {
		t.Fatalf("nil bind err=%v", err)
	}
}

func TestPutRoleReplaces(t *testing.T) {
	r := NewRegistry()
	term, _ := Term("a", "b")
	if err := r.PutRole("R", []Entry{entry("*", &term, FieldAuth{Unrestricted: true})}); err != nil {
		t.Fatal(err)
	}
	if err := r.PutRole("R", []Entry{entry("x-*", nil, FieldAuth{Grant: []string{"*"}})}); err != nil {
		t.Fatal(err)
	}
	got, ok := r.roleEntries("R")
	if !ok || len(got) != 1 || got[0].Pattern != "x-*" {
		t.Fatalf("replace failed: %+v", got)
	}
}

func TestErrorOrderRoleDelete(t *testing.T) {
	r := NewRegistry()
	if err := r.DeleteRole(""); !errors.Is(err, ErrInvalid) {
		t.Fatalf("invalid must precede not-found: %v", err)
	}
}

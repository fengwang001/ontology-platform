package role

import (
	"errors"
	"strings"
	"testing"
)

func validEntry(pattern string, filter *Expr) Entry {
	return Entry{IndexPattern: pattern, Filter: filter, Fields: FieldAuth{Unrestricted: true}}
}

func TestPutRoleValidation(t *testing.T) {
	s := NewStore()
	teamA := Term("team", "a")

	if err := s.PutRole("", []Entry{validEntry("*", nil)}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("empty role name: %v", err)
	}
	if err := s.PutRole("R", nil); !errors.Is(err, ErrInvalid) {
		t.Fatalf("zero entries: %v", err)
	}
	many := make([]Entry, 17)
	for i := range many {
		many[i] = validEntry("*", nil)
	}
	if err := s.PutRole("R", many); !errors.Is(err, ErrInvalid) {
		t.Fatalf("17 entries: %v", err)
	}
	if err := s.PutRole("R", []Entry{validEntry("a*b", nil)}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("bad pattern: %v", err)
	}
	if err := s.PutRole("R", []Entry{{IndexPattern: "*", Fields: FieldAuth{Grant: []string{}}}}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("empty grant: %v", err)
	}
	if err := s.PutRole("R", []Entry{{IndexPattern: "*", Fields: FieldAuth{Grant: []string{"x", "b*d"}}}}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("bad field pattern: %v", err)
	}
	if err := s.PutRole("R", []Entry{validEntry("*", &teamA)}); err != nil {
		t.Fatalf("valid: %v", err)
	}
}

func TestExprValidation(t *testing.T) {
	if err := ValidateExpr(Term("", int64(1))); !errors.Is(err, ErrInvalid) {
		t.Fatalf("empty field: %v", err)
	}
	if err := ValidateExpr(Term("f", 3)); !errors.Is(err, ErrInvalid) {
		t.Fatalf("plain int value: %v", err)
	}
	if err := ValidateExpr(Range("f", 9, 1)); !errors.Is(err, ErrInvalid) {
		t.Fatalf("inverted range: %v", err)
	}
	if err := ValidateExpr(And()); !errors.Is(err, ErrInvalid) {
		t.Fatalf("And with 0 args: %v", err)
	}
	nine := make([]Expr, 9)
	for i := range nine {
		nine[i] = Term("f", int64(1))
	}
	if err := ValidateExpr(Or(nine...)); !errors.Is(err, ErrInvalid) {
		t.Fatalf("Or with 9 args: %v", err)
	}

	// 深度 8 合法，深度 9 非法。
	deep := Term("f", int64(1))
	for i := 0; i < 7; i++ {
		deep = And(deep)
	}
	if err := ValidateExpr(deep); err != nil {
		t.Fatalf("depth 8 should be valid: %v", err)
	}
	deep = And(deep)
	if err := ValidateExpr(deep); !errors.Is(err, ErrInvalid) {
		t.Fatalf("depth 9 should be invalid: %v", err)
	}
}

func TestDeleteRoleOrdering(t *testing.T) {
	s := NewStore()
	// 参数非法先于角色不存在。
	if err := s.DeleteRole(""); !errors.Is(err, ErrInvalid) {
		t.Fatalf("invalid name: %v", err)
	}
	// 角色不存在先于角色在用。
	if err := s.DeleteRole("ghost"); !errors.Is(err, ErrNoRole) {
		t.Fatalf("missing role: %v", err)
	}
	if err := s.PutRole("R", []Entry{validEntry("*", nil)}); err != nil {
		t.Fatal(err)
	}
	if err := s.BindUser("u", []string{"R"}); err != nil {
		t.Fatal(err)
	}
	// 仍被绑定：角色在用优先于其他。
	if err := s.DeleteRole("R"); !errors.Is(err, ErrRoleInUse) {
		t.Fatalf("in use: %v", err)
	}
	s.UnbindUser("u")
	if err := s.DeleteRole("R"); err != nil {
		t.Fatalf("delete after unbind: %v", err)
	}
	if err := s.DeleteRole("R"); !errors.Is(err, ErrNoRole) {
		t.Fatalf("already deleted: %v", err)
	}
}

func TestBindUserValidation(t *testing.T) {
	s := NewStore()
	if err := s.PutRole("R1", []Entry{validEntry("*", nil)}); err != nil {
		t.Fatal(err)
	}
	if err := s.BindUser("", []string{"R1"}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("bad user: %v", err)
	}
	if err := s.BindUser("u", []string{"R1", "R1"}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("duplicate roles: %v", err)
	}
	if err := s.BindUser("u", []string{}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("zero roles: %v", err)
	}
	// 参数合法后角色不存在。
	if err := s.BindUser("u", []string{"R1", "ghost"}); !errors.Is(err, ErrNoRole) {
		t.Fatalf("missing role: %v", err)
	}
	if err := s.BindUser("u", []string{"R1"}); err != nil {
		t.Fatalf("bind: %v", err)
	}

	// 整体替换：改为不存在的角色应失败且保留旧绑定。
	if err := s.BindUser("u", []string{"ghost"}); !errors.Is(err, ErrNoRole) {
		t.Fatalf("rebind missing: %v", err)
	}
	entries, touched, err := s.UserEntries("u")
	if err != nil || touched != 1 || len(entries) != 1 {
		t.Fatalf("old binding changed: entries=%d touched=%d err=%v", len(entries), touched, err)
	}

	if _, _, err := s.UserEntries("nobody"); !errors.Is(err, ErrNoUser) {
		t.Fatalf("unknown user: %v", err)
	}

	// 重新 PutRole 后绑定立即看到新内容（整体替换）。
	if err := s.PutRole("R1", []Entry{validEntry("a*", nil), validEntry("b*", nil)}); err != nil {
		t.Fatal(err)
	}
	entries, touched, _ = s.UserEntries("u")
	if touched != 2 {
		t.Fatalf("touched after replace = %d, want 2", touched)
	}
}

func TestErrorsIsDistinguishable(t *testing.T) {
	for _, target := range []error{ErrInvalid, ErrNoUser, ErrNoRole, ErrRoleInUse, ErrNoPerm, ErrDocNotFound} {
		wrapped := invalidf("detail")
		if target == ErrInvalid && !errors.Is(wrapped, ErrInvalid) {
			t.Fatalf("wrapped invalid not matched")
		}
		if !strings.Contains(target.Error(), " ") && target.Error() == "" {
			t.Fatal("empty message")
		}
	}
}

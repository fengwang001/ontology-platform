package initorder

import (
	"errors"
	"reflect"
	"testing"
)

func TestNewSessionRejectsInvalidPredeclared(t *testing.T) {
	for _, bad := range [][]string{
		{""}, {"1abc"}, {"a-b"}, {"_"}, {"ok", "bad name"},
	} {
		if _, err := NewSession(bad); !errors.Is(err, ErrInvalidArgument) {
			t.Fatalf("NewSession(%v): expected ErrInvalidArgument, got %v", bad, err)
		}
	}
	if _, err := NewSession([]string{"a", "b", "a"}); !errors.Is(err, ErrDuplicateDeclaration) {
		t.Fatalf("expected ErrDuplicateDeclaration for duplicate predeclared, got %v", err)
	}
}

func TestRegisterVarsInvalidArgument(t *testing.T) {
	s := mustSession(t)
	cases := []struct {
		name string
		vars []string
		refs []string
	}{
		{"empty vars", nil, nil},
		{"invalid var digit", []string{"1x"}, nil},
		{"invalid var char", []string{"a-b"}, nil},
		{"invalid var empty", []string{"a", ""}, nil},
		{"blank in refs", []string{"a"}, []string{"_"}},
		{"invalid ref", []string{"a"}, []string{"2b"}},
	}
	for _, c := range cases {
		if err := s.RegisterVars(c.vars, c.refs); !errors.Is(err, ErrInvalidArgument) {
			t.Fatalf("%s: expected ErrInvalidArgument, got %v", c.name, err)
		}
	}
	// 全部被拒绝，会话保持无任何声明，求解成功且次序为空。
	if got := solveUnits(t, s); len(got) != 0 {
		t.Fatalf("expected empty order after rejections, got %v", got)
	}
}

func TestRegisterFuncInvalidArgument(t *testing.T) {
	s := mustSession(t)
	if err := s.RegisterFunc("_", nil); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("blank func name: expected ErrInvalidArgument, got %v", err)
	}
	if err := s.RegisterFunc("1f", nil); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("invalid func name: expected ErrInvalidArgument, got %v", err)
	}
	if err := s.RegisterFunc("f", []string{"_"}); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("blank ref: expected ErrInvalidArgument, got %v", err)
	}
}

func TestDuplicateDeclaration(t *testing.T) {
	s := mustSession(t, "runtime")
	mustRegisterVars(t, s, []string{"x"}, nil)
	mustRegisterFunc(t, s, "f", nil)
	cases := []struct {
		name string
		reg  func() error
	}{
		{"var dup var", func() error { return s.RegisterVars([]string{"x"}, nil) }},
		{"var dup func", func() error { return s.RegisterVars([]string{"f"}, nil) }},
		{"var dup predeclared", func() error { return s.RegisterVars([]string{"runtime"}, nil) }},
		{"func dup var", func() error { return s.RegisterFunc("x", nil) }},
		{"func dup func", func() error { return s.RegisterFunc("f", nil) }},
		{"func dup predeclared", func() error { return s.RegisterFunc("runtime", nil) }},
		{"same unit dup", func() error { return s.RegisterVars([]string{"y", "y"}, nil) }},
	}
	for _, c := range cases {
		if err := c.reg(); !errors.Is(err, ErrDuplicateDeclaration) {
			t.Fatalf("%s: expected ErrDuplicateDeclaration, got %v", c.name, err)
		}
	}
	// 同一单元内两个空白标识符不算重名。
	mustRegisterVars(t, s, []string{"_", "_"}, nil)
}

// 参数非法优先于重复声明。
func TestInvalidPriorityOverDuplicate(t *testing.T) {
	s := mustSession(t)
	mustRegisterVars(t, s, []string{"x"}, nil)
	// vars 同时含已声明的 x 与非法标识符：报非法而非重名。
	if err := s.RegisterVars([]string{"x", "1bad"}, nil); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("expected ErrInvalidArgument, got %v", err)
	}
	// 函数名与已声明变量重名且引用集合含空白标识符：报非法。
	if err := s.RegisterFunc("x", []string{"_"}); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("expected ErrInvalidArgument, got %v", err)
	}
}

// 被拒绝的登记不得占用源码位置、不得改变会话状态。
func TestRejectedRegistrationLeavesNoTrace(t *testing.T) {
	s := mustSession(t)
	mustRegisterVars(t, s, []string{"a"}, nil) // 单元 0
	// 一系列被拒绝的登记。
	if err := s.RegisterVars(nil, nil); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("expected rejection, got %v", err)
	}
	if err := s.RegisterVars([]string{"a"}, nil); !errors.Is(err, ErrDuplicateDeclaration) {
		t.Fatalf("expected rejection, got %v", err)
	}
	if err := s.RegisterFunc("a", nil); !errors.Is(err, ErrDuplicateDeclaration) {
		t.Fatalf("expected rejection, got %v", err)
	}
	// 被拒绝的名字不占用命名空间，可以随后正常登记。
	mustRegisterVars(t, s, []string{"b"}, []string{"a"}) // 单元 1
	mustRegisterFunc(t, s, "f", []string{"b"})
	mustRegisterVars(t, s, []string{"c"}, []string{"f"}) // 单元 2
	if got, want := solveUnits(t, s), []int{0, 1, 2}; !reflect.DeepEqual(got, want) {
		t.Fatalf("order = %v, want %v", got, want)
	}
	// 未声明引用报告中的登记次序不应包含被拒绝的登记：
	// 再登记一个含未声明引用的单元，它应是第 5 条已接受声明（下标 4）。
	mustRegisterVars(t, s, []string{"d"}, []string{"ghost"}) // 单元 3
	_, err := s.Solve()
	var undecl *UndeclaredReferenceError
	if !errors.As(err, &undecl) {
		t.Fatalf("expected UndeclaredReferenceError, got %v", err)
	}
	if undecl.DeclIndex != 4 {
		t.Fatalf("DeclIndex = %d, want 4 (rejected registrations must not occupy positions)", undecl.DeclIndex)
	}
}

// 预声明标识符视为已就绪，且不出现在依赖集合中。
func TestPredeclaredReadyAndNotInDeps(t *testing.T) {
	s := mustSession(t, "runtime", "unsafe")
	mustRegisterFunc(t, s, "f", []string{"runtime"})
	mustRegisterVars(t, s, []string{"a"}, []string{"f", "unsafe"})
	if got, want := solveUnits(t, s), []int{0}; !reflect.DeepEqual(got, want) {
		t.Fatalf("order = %v, want %v", got, want)
	}
	if got := depsOf(t, s, 0); len(got) != 0 {
		t.Fatalf("deps = %v, want empty (predeclared are not variables)", got)
	}
}

// 求解不改变会话：失败后仍可继续登记修复，再次求解成功。
func TestSolveDoesNotMutateSession(t *testing.T) {
	s := mustSession(t)
	mustRegisterVars(t, s, []string{"a"}, []string{"ghost"})
	if _, err := s.Solve(); !errors.Is(err, ErrUndeclaredReference) {
		t.Fatalf("expected ErrUndeclaredReference, got %v", err)
	}
	// 求解失败后登记不受影响，补上声明即可成功。
	mustRegisterFunc(t, s, "ghost", nil)
	sol1, err := s.Solve()
	if err != nil {
		t.Fatalf("Solve failed: %v", err)
	}
	sol2, err := s.Solve()
	if err != nil {
		t.Fatalf("Solve failed: %v", err)
	}
	if !reflect.DeepEqual(sol1, sol2) {
		t.Fatalf("repeated solves differ: %v vs %v", sol1, sol2)
	}
}

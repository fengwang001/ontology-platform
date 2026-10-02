package hygiene

import (
	"errors"
	"strconv"
	"strings"
	"sync"
	"testing"
)

func TestHygieneRules(t *testing.T) {
	s := NewSession()
	mustDef(t, s, "q", []string{"x"}, "(quote (x add2 lam t))")
	mustDef(t, s, "capture", []string{"u"}, "(lam (t u) (t u))")
	mustDef(t, s, "nested", []string{"x"}, "(lam (t) (lam (t) t))")
	mustDef(t, s, "binderarg", []string{"v", "body"}, "(lam (v) body)")

	cases := []struct {
		input string
		want  string
	}{
		{"(q t)", "(quote (t add2 lam t))"},
		{"(capture 3)", "(lam (t#1 3#2) (t#1 3#2))"},
		{"(nested 1)", "(lam (t#3) (lam (t#4) t#4))"},
		{"(binderarg x (f x))", "(lam (x#5) (f x#5))"},
	}
	for _, tc := range cases {
		got, err := s.Expand(tc.input)
		if err != nil {
			t.Fatalf("Expand(%s): %v", tc.input, err)
		}
		if got != tc.want {
			t.Fatalf("Expand(%s) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

func TestHygieneMacrosAndQuoting(t *testing.T) {
	s := NewSession()
	mustDef(t, s, "use-f", []string{"x"}, "(f x)")
	mustDef(t, s, "identity", []string{"x"}, "x")
	mustDef(t, s, "qmacro", nil, "(quote (identity use-f lam quote))")

	cases := []struct {
		input string
		want  string
	}{
		{"(lam (f) (use-f 1))", "(lam (f#1) (f 1))"},
		{"(identity (lam (f) (use-f 2)))", "(lam (f#2) (f 2))"},
		{"(qmacro)", "(quote (identity use-f lam quote))"},
		{"(identity (quote (identity x)))", "(quote (identity x))"},
	}
	for _, tc := range cases {
		got, err := s.Expand(tc.input)
		if err != nil {
			t.Fatalf("Expand(%s): %v", tc.input, err)
		}
		if got != tc.want {
			t.Fatalf("Expand(%s) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

func TestMultipleMacroInstances(t *testing.T) {
	s := NewSession()
	mustDef(t, s, "fresh", []string{"x"}, "(lam (t) (t x))")
	got, err := s.Expand("((fresh a) (fresh b))")
	if err != nil {
		t.Fatal(err)
	}
	want := "((lam (t#1) (t#1 a)) (lam (t#2) (t#2 b)))"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestUserBinderDoesNotReuseOuterBinding(t *testing.T) {
	s := NewSession()
	mustDef(t, s, "makelam", []string{"v", "body"}, "(lam (v) body)")
	got, err := s.Expand("(lam (x) (makelam x x))")
	if err != nil {
		t.Fatal(err)
	}
	want := "(lam (x#1) (lam (x#2) x#2))"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestRejectionsAndBoundaries(t *testing.T) {
	t.Run("definition", func(t *testing.T) {
		s := NewSession()
		if err := s.DefMacro("lam", nil, "x"); !errors.Is(err, ErrInvalidDefinition) {
			t.Fatalf("reserved name: %v", err)
		}
		if err := s.DefMacro("bad", []string{"a", "a"}, "x"); !errors.Is(err, ErrInvalidDefinition) {
			t.Fatalf("duplicate param: %v", err)
		}
		if err := s.DefMacro("bad", []string{"lam"}, "x"); !errors.Is(err, ErrInvalidDefinition) {
			t.Fatalf("reserved param: %v", err)
		}
		if err := s.DefMacro("bad", nil, "()"); err != nil {
			t.Fatalf("empty list is a valid term template: %v", err)
		}
		limitTemplate := strings.Repeat("(x ", 99) + "x x" + strings.Repeat(")", 99)
		if err := s.DefMacro("limitok", nil, limitTemplate); err != nil {
			t.Fatalf("200-node template: %v", err)
		}
		tooLargeTemplate := strings.Repeat("(x ", 100) + "x" + strings.Repeat(")", 100)
		if err := s.DefMacro("limitbad", nil, tooLargeTemplate); !errors.Is(err, ErrInvalidDefinition) {
			t.Fatalf("201-node template: %v", err)
		}
		if err := s.DefMacro(strings.Repeat("a", 33), nil, "x"); !errors.Is(err, ErrInvalidDefinition) {
			t.Fatalf("33-byte symbol should be invalid")
		}
		mustDef(t, s, "once", nil, "x")
		if err := s.DefMacro("once", nil, "y"); !errors.Is(err, ErrDuplicateMacro) {
			t.Fatalf("duplicate macro: %v", err)
		}
	})

	t.Run("arity and invalid argument binder", func(t *testing.T) {
		s := NewSession()
		mustDef(t, s, "one", []string{"x"}, "x")
		mustDef(t, s, "makelam", []string{"v"}, "(lam (v) v)")
		assertExpandError(t, s, "(one)", ErrArgumentCount)
		assertExpandError(t, s, "(one 1 2)", ErrArgumentCount)
		assertExpandError(t, s, "(makelam (1))", ErrInvalidForm)
		assertExpandError(t, s, "(makelam (f x))", ErrInvalidForm)
		assertCounters(t, s, 0, 0)
	})

	t.Run("depth boundary", func(t *testing.T) {
		s := NewSession()
		mustDef(t, s, "wrap", []string{"x"}, "x")
		depth20 := strings.Repeat("(wrap ", 20) + "x" + strings.Repeat(")", 20)
		depth21 := strings.Repeat("(wrap ", 21) + "x" + strings.Repeat(")", 21)
		if _, err := s.Expand(depth20); err != nil {
			t.Fatalf("depth 20: %v", err)
		}
		assertExpandError(t, s, depth21, ErrDepthLimit)
		assertCounters(t, s, 0, 20)

		chain20 := NewSession()
		mustDef(t, chain20, "c19", nil, "z")
		for i := 18; i >= 0; i-- {
			mustDef(t, chain20, "c"+strconv.Itoa(i), nil, "(c"+strconv.Itoa(i+1)+")")
		}
		if _, err := chain20.Expand("(c0)"); err != nil {
			t.Fatalf("macro chain depth 20: %v", err)
		}
		chain21 := NewSession()
		mustDef(t, chain21, "d20", nil, "z")
		for i := 19; i >= 0; i-- {
			mustDef(t, chain21, "d"+strconv.Itoa(i), nil, "(d"+strconv.Itoa(i+1)+")")
		}
		assertExpandError(t, chain21, "(d0)", ErrDepthLimit)

	})

	t.Run("macro table boundary", func(t *testing.T) {
		s := NewSession()
		for i := 0; i < 100; i++ {
			name := "d" + strconv.Itoa(i)
			if err := s.DefMacro(name, nil, "x"); err != nil {
				t.Fatalf("DefMacro(%d): %v", i, err)
			}
		}
		if err := s.DefMacro("overflow", nil, "x"); !errors.Is(err, ErrMacroTableFull) {
			t.Fatalf("error = %v, want ErrMacroTableFull", err)
		}
	})

	t.Run("concurrent operations", func(t *testing.T) {
		s := NewSession()
		mustDef(t, s, "id", []string{"x"}, "x")
		var wait sync.WaitGroup
		for i := 0; i < 64; i++ {
			wait.Add(1)
			go func() {
				defer wait.Done()
				_, _ = s.Expand("(id z)")
				_, _ = s.N(), s.X()
			}()
		}
		wait.Wait()
		if s.X() != 64 {
			t.Fatalf("X = %d, want 64", s.X())
		}
	})

}

func assertExpandError(t *testing.T, s *Session, input string, want error) {
	t.Helper()
	_, err := s.Expand(input)
	if !errors.Is(err, want) {
		t.Fatalf("Expand(%s) error = %v, want %v", input, err, want)
	}
}

func assertCounters(t *testing.T, s *Session, n, x int) {
	t.Helper()
	if s.N() != n || s.X() != x {
		t.Fatalf("counters N=%d X=%d, want N=%d X=%d", s.N(), s.X(), n, x)
	}
}

func TestSizeBoundary(t *testing.T) {
	s := NewSession()
	mustDef(t, s, "many", nil, "(a b c d e f g h)")
	outer := sizeTerm(0)
	if _, err := s.Expand(outer); err != nil {
		t.Fatalf("under boundary setup: %v", err)
	}
	beforeN, beforeX := s.N(), s.X()
	_, err := s.Expand(sizeTerm(1))
	if !errors.Is(err, ErrSizeLimit) {
		t.Fatalf("error = %v, want ErrSizeLimit", err)
	}
	if s.N() != beforeN || s.X() != beforeX {
		t.Fatalf("rejection changed counters: N=%d X=%d, before N=%d X=%d", s.N(), s.X(), beforeN, beforeX)
	}
}

func sizeTerm(extra int) string {
	parts := make([]string, 0, 223+extra)
	for i := 0; i < 218; i++ {
		parts = append(parts, "(many)")
	}
	for i := 0; i < 5+extra; i++ {
		parts = append(parts, "z")
	}
	return tree(parts)
}

func tree(leaves []string) string {
	current := leaves
	for len(current) > 1 {
		var next []string
		for i := 0; i < len(current); i += 8 {
			end := i + 8
			if end > len(current) {
				end = len(current)
			}
			next = append(next, "("+strings.Join(current[i:end], " ")+")")
		}
		current = next
	}
	return current[0]
}

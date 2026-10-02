package hygiene

import "testing"

func TestSpecExamples(t *testing.T) {
	s := NewSession()
	mustDef(t, s, "my-or", []string{"a", "b"}, "((lam (t) (if t t b)) a)")
	mustDef(t, s, "add2", []string{"x"}, "(+ x 2)")
	mustDef(t, s, "inc", []string{"x"}, "(add2 x)")
	mustDef(t, s, "my-lam", []string{"v", "b"}, "(lam (v) b)")

	cases := []struct {
		input string
		want  string
		n     int
		x     int
	}{
		{"(my-or (f) t)", "((lam (t#1) (if t#1 t#1 t)) (f))", 1, 1},
		{"(lam (+) (add2 5))", "(lam (+#2) (+ 5 2))", 2, 2},
		{"(lam (add2) (inc 1))", "(lam (add2#3) (+ 1 2))", 3, 4},
		{"(lam (add2) (add2 5))", "(lam (add2#4) (add2#4 5))", 4, 4},
		{"(my-lam x (f x))", "(lam (x#5) (f x#5))", 5, 5},
		{
			"(my-or (g) (my-or (h) t))",
			"((lam (t#6) (if t#6 t#6 ((lam (t#7) (if t#7 t#7 t)) (h)))) (g))",
			7,
			7,
		},
	}
	for _, tc := range cases {
		got, err := s.Expand(tc.input)
		if err != nil {
			t.Fatalf("Expand(%s): %v", tc.input, err)
		}
		if got != tc.want {
			t.Fatalf("Expand(%s) = %q, want %q", tc.input, got, tc.want)
		}
		if gotN := s.N(); gotN != tc.n {
			t.Fatalf("N = %d, want %d", gotN, tc.n)
		}
		if gotX := s.X(); gotX != tc.x {
			t.Fatalf("X = %d, want %d", gotX, tc.x)
		}
	}
}

func TestRejection(t *testing.T) {
	s := NewSession()
	if _, err := s.Expand("(lam (lam) 1)"); err != ErrInvalidForm {
		t.Fatalf("error = %v, want ErrInvalidForm", err)
	}
	if s.N() != 0 || s.X() != 0 {
		t.Fatalf("rejected expand changed counters: N=%d X=%d", s.N(), s.X())
	}
}

func mustDef(t *testing.T, s *Session, name string, params []string, template string) {
	t.Helper()
	if err := s.DefMacro(name, params, template); err != nil {
		t.Fatalf("DefMacro(%s): %v", name, err)
	}
}

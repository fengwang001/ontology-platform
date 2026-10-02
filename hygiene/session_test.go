package hygiene

import "testing"

func mustParse(t *testing.T, text string) *Term {
	t.Helper()
	parsed, err := Parse(text)
	if err != nil {
		t.Fatalf("Parse(%q): %v", text, err)
	}
	return parsed
}

func mustRender(t *testing.T, term *Term) string {
	t.Helper()
	text, err := Render(term)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	return text
}

func TestSpecExamples(t *testing.T) {
	session := NewSession()
	mustDef(t, session, "my-or", []string{"a", "b"}, mustParse(t, "((lam (t) (if t t b)) a)"))
	mustDef(t, session, "add2", []string{"x"}, mustParse(t, "(+ x 2)"))
	mustDef(t, session, "inc", []string{"x"}, mustParse(t, "(add2 x)"))
	mustDef(t, session, "my-lam", []string{"v", "b"}, mustParse(t, "(lam (v) b)"))

	tests := []struct {
		input    string
		output   string
		bindings int
		macros   int
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

	for _, test := range tests {
		result, err := session.Expand(mustParse(t, test.input))
		if err != nil {
			t.Fatalf("Expand(%q): %v", test.input, err)
		}
		if got := mustRender(t, result); got != test.output {
			t.Fatalf("Expand(%q) = %q, want %q", test.input, got, test.output)
		}
		if got := session.BindingCount(); got != test.bindings {
			t.Fatalf("N after %q = %d, want %d", test.input, got, test.bindings)
		}
		if got := session.ExpansionCount(); got != test.macros {
			t.Fatalf("X after %q = %d, want %d", test.input, got, test.macros)
		}
	}

	_, err := session.Expand(mustParse(t, "(lam (lam) 1)"))
	if err == nil || err.(ExpandError).Kind != FormInvalid {
		t.Fatalf("reserved binder error = %v, want FormInvalid", err)
	}
	if session.BindingCount() != 7 || session.ExpansionCount() != 7 {
		t.Fatalf("rejected expansion changed state: N=%d X=%d", session.BindingCount(), session.ExpansionCount())
	}
}

func mustDef(t *testing.T, session *Session, name string, params []string, template *Term) {
	t.Helper()
	if err := session.DefMacro(name, params, template); err != nil {
		t.Fatalf("DefMacro(%q): %v", name, err)
	}
}

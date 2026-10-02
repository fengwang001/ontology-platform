package hygiene

import "testing"

func TestNestedIntroducedBinderShadowsOuter(t *testing.T) {
	session := NewSession()
	mustDef(t, session, "nested", []string{"a"}, mustParse(t, "(lam (t) (lam (t) t))"))
	result, err := session.Expand(mustParse(t, "(nested x)"))
	if err != nil {
		t.Fatal(err)
	}
	want := "(lam (t#1) (lam (t#2) t#2))"
	if got := mustRender(t, result); got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestQuoteIsPreserved(t *testing.T) {
	session := NewSession()
	mustDef(t, session, "q", []string{"x"}, mustParse(t, "(quote (m x lam))"))
	mustDef(t, session, "m", nil, mustParse(t, "(quote m)"))
	result, err := session.Expand(mustParse(t, "(q user)"))
	if err != nil {
		t.Fatal(err)
	}
	want := "(quote (m x lam))"
	if got := mustRender(t, result); got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	if session.BindingCount() != 0 || session.ExpansionCount() != 1 {
		t.Fatalf("state = N:%d X:%d", session.BindingCount(), session.ExpansionCount())
	}
}

func TestArgumentInBinderPositionIsUserBinding(t *testing.T) {
	session := NewSession()
	mustDef(t, session, "bind", []string{"x", "body"}, mustParse(t, "(lam (x) body)"))
	result, err := session.Expand(mustParse(t, "(bind z (z y))"))
	if err != nil {
		t.Fatal(err)
	}
	want := "(lam (z#1) (z#1 y))"
	if got := mustRender(t, result); got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestNonSymbolArgumentInBinderIsFormInvalid(t *testing.T) {
	session := NewSession()
	mustDef(t, session, "bad", []string{"x"}, mustParse(t, "(lam (x) x)"))
	_, err := session.Expand(mustParse(t, "(bad (a))"))
	if err == nil || err.(ExpandError).Kind != FormInvalid {
		t.Fatalf("err = %v, want FormInvalid", err)
	}
	if session.BindingCount() != 0 || session.ExpansionCount() != 0 {
		t.Fatalf("rejected operation changed state: N:%d X:%d", session.BindingCount(), session.ExpansionCount())
	}
}

func TestArityMismatch(t *testing.T) {
	session := NewSession()
	mustDef(t, session, "two", []string{"a", "b"}, mustParse(t, "(+ a b)"))
	_, err := session.Expand(mustParse(t, "(two 1)"))
	if err == nil || err.(ExpandError).Kind != ArityMismatch {
		t.Fatalf("err = %v, want ArityMismatch", err)
	}
}

func TestMalformedQuoteIsFormInvalid(t *testing.T) {
	session := NewSession()
	for _, text := range []string{"(quote)", "(quote x y)"} {
		_, err := session.Expand(mustParse(t, text))
		if err == nil || err.(ExpandError).Kind != FormInvalid {
			t.Fatalf("%q err = %v, want FormInvalid", text, err)
		}
	}
}

func TestInvalidDirectTermIsRejected(t *testing.T) {
	session := NewSession()
	if _, err := session.Expand(&Term{Kind: 42}); err == nil || err.(ExpandError).Kind != FormInvalid {
		t.Fatalf("invalid kind err = %v, want FormInvalid", err)
	}
}

func TestIntroducedGlobalNotCapturedByUserBinder(t *testing.T) {
	session := NewSession()
	mustDef(t, session, "use", nil, mustParse(t, "(lam (f) (f 1))"))
	result, err := session.Expand(mustParse(t, "(lam (f) (use))"))
	if err != nil {
		t.Fatal(err)
	}
	want := "(lam (f#1) (lam (f#2) (f#2 1)))"
	if got := mustRender(t, result); got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

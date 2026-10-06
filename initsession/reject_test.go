package initsession

import "testing"

func TestRegistrationRejection(t *testing.T) {
	lg := newTestLogger(t)
	s := mustSession(t, "pre")

	reject := func(desc string, kind ErrorKind, fn func() error) {
		t.Helper()
		err := fn()
		if err == nil {
			t.Fatalf("%s: want error", desc)
		}
		e := err.(*Error)
		if e.Kind != kind {
			t.Fatalf("%s: want kind %d, got %v", desc, kind, err)
		}
		lg.outputf("rejected (%s): %v", desc, err)
	}

	reject("empty lhs", KindInvalidArgument, func() error {
		_, err := s.AddVariableUnit(nil, nil)
		return err
	})
	reject("illegal var name", KindInvalidArgument, func() error {
		_, err := s.AddVariableUnit([]string{"1x"}, nil)
		return err
	})
	reject("blank in refs", KindInvalidArgument, func() error {
		_, err := s.AddVariableUnit([]string{"z"}, []string{"_"})
		return err
	})
	reject("illegal ref identifier", KindInvalidArgument, func() error {
		_, err := s.AddVariableUnit([]string{"z"}, []string{"a-b"})
		return err
	})
	reject("blank function name", KindInvalidArgument, func() error {
		return s.AddFunction("_", nil)
	})
	reject("illegal function name", KindInvalidArgument, func() error {
		return s.AddFunction("9f", nil)
	})

	// Accept one of each so redeclaration paths exist.
	addUnit(t, lg, s, []string{"v", "w"}, nil)
	addFunc(t, lg, s, "g", nil)

	reject("var redeclares var", KindRedeclared, func() error {
		_, err := s.AddVariableUnit([]string{"v"}, nil)
		return err
	})
	reject("function redeclares var", KindRedeclared, func() error {
		return s.AddFunction("w", nil)
	})
	reject("var redeclares function", KindRedeclared, func() error {
		_, err := s.AddVariableUnit([]string{"g"}, nil)
		return err
	})
	reject("function redeclares function", KindRedeclared, func() error {
		return s.AddFunction("g", nil)
	})
	reject("redeclares predeclared", KindRedeclared, func() error {
		return s.AddFunction("pre", nil)
	})
	reject("duplicate within unit", KindRedeclared, func() error {
		_, err := s.AddVariableUnit([]string{"k", "k"}, nil)
		return err
	})

	// Invalid argument wins even when the name also redeclares.
	reject("invalid beats redeclare", KindInvalidArgument, func() error {
		_, err := s.AddVariableUnit([]string{"v", "1bad"}, nil)
		return err
	})
	reject("bad ref beats redeclared fn", KindInvalidArgument, func() error {
		return s.AddFunction("g", []string{"_"})
	})

	// Rejected registrations occupy no source position: the next unit is
	// still index 1.
	idx, err := s.AddVariableUnit([]string{"fresh"}, nil)
	if err != nil || idx != 1 {
		t.Fatalf("after rejects, next unit index = %d (%v), want 1", idx, err)
	}
	lg.outputf("rejected registrations did not consume source positions; next index %d", idx)

	// Session state unchanged: solving the accepted declarations
	// succeeds with the two original units only.
	res, _, err := s.Solve()
	if err != nil {
		t.Fatalf("solve after rejects: %v", err)
	}
	if len(res.Dependencies) != 2 {
		t.Fatalf("accepted units = %d, want 2", len(res.Dependencies))
	}
}

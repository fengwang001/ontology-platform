package eval

import (
	"errors"
	"testing"
)

func assertPosError(t *testing.T, expr string, wantErr error, wantPos int) {
	t.Helper()
	_, _, err := Eval(expr)
	if err == nil {
		t.Fatalf("Eval(%q): want error, got nil", expr)
	}
	if !errors.Is(err, wantErr) {
		t.Fatalf("Eval(%q): err = %v, want class %v", expr, err, wantErr)
	}
	var pe *PosError
	if !errors.As(err, &pe) {
		t.Fatalf("Eval(%q): %v is not *PosError", expr, err)
	}
	if pe.Pos != wantPos {
		t.Fatalf("Eval(%q): pos = %d, want %d", expr, pe.Pos, wantPos)
	}
}

func TestEmptyExpression(t *testing.T) {
	for _, expr := range []string{"", " ", "\t\n \r", "　"} {
		_, _, err := Eval(expr)
		if !errors.Is(err, ErrEmptyExpression) {
			t.Errorf("Eval(%q): err = %v", expr, err)
		}
	}
}

func TestIllegalCharPositions(t *testing.T) {
	assertPosError(t, "1+", ErrIllegalChar, 2)
	assertPosError(t, "*3", ErrIllegalChar, 0)
	assertPosError(t, "1$2", ErrIllegalChar, 1)
	assertPosError(t, "  1+a", ErrIllegalChar, 4)
	assertPosError(t, "()", ErrIllegalChar, 1)
	assertPosError(t, "(1+)", ErrIllegalChar, 3)
}

func TestImplicitMultiplicationRejected(t *testing.T) {
	// "2(3)": the '(' at byte 1 is reported as an illegal character.
	_, _, err := Eval("2(3)")
	if !errors.Is(err, ErrIllegalChar) {
		t.Fatalf("err = %v", err)
	}
	var pe *PosError
	if !errors.As(err, &pe) || pe.Pos != 1 {
		t.Fatalf("err = %v, want pos 1", err)
	}
	// Adjacent numbers are likewise illegal; second number starts at pos 2.
	assertPosError(t, "1 2", ErrIllegalChar, 2)
	// ")(" adjacency.
	assertPosError(t, "(1)(2)", ErrIllegalChar, 3)
}

func TestBadLiteralPositions(t *testing.T) {
	assertPosError(t, "1.2.3", ErrBadLiteral, 0)
	assertPosError(t, "..5", ErrBadLiteral, 0)
	assertPosError(t, "1.", ErrBadLiteral, 0)
	assertPosError(t, ".5", ErrBadLiteral, 0)
	assertPosError(t, "2+1.2.3", ErrBadLiteral, 2)
	assertPosError(t, "1..", ErrBadLiteral, 0)
}

func TestUnmatchedParentheses(t *testing.T) {
	t.Run("missing close", func(t *testing.T) {
		_, _, err := Eval("(1+2")
		if !errors.Is(err, ErrUnmatchedParen) {
			t.Fatalf("err = %v", err)
		}
		var pe *PosError
		if !errors.As(err, &pe) {
			t.Fatalf("err = %v", err)
		}
		if pe.Pos != 0 || pe.Ordinal != 1 {
			t.Fatalf("pos=%d ordinal=%d, want 0,1", pe.Pos, pe.Ordinal)
		}
	})
	t.Run("outermost remains open", func(t *testing.T) {
		// "((1)": the inner '(' is closed; the first '(' is unmatched.
		_, _, err := Eval("((1)")
		var pe *PosError
		if !errors.As(err, &pe) || !errors.Is(err, ErrUnmatchedParen) {
			t.Fatalf("err = %v", err)
		}
		if pe.Pos != 0 || pe.Ordinal != 1 {
			t.Fatalf("pos=%d ordinal=%d, want 0,1", pe.Pos, pe.Ordinal)
		}
	})
	t.Run("extra close", func(t *testing.T) {
		assertPosError(t, "1)", ErrUnmatchedParen, 1)
		assertPosError(t, "(1))", ErrUnmatchedParen, 3)
	})
}

func TestErrorClassesAreDistinct(t *testing.T) {
	classes := []error{
		ErrEmptyExpression,
		ErrIllegalChar,
		ErrUnmatchedParen,
		ErrBadLiteral,
		ErrOverflow,
		ErrDivideByZero,
		ErrNotRepresentable,
	}
	exprs := []string{
		"",
		"@",
		"(1",
		"1.2.3",
		"9223372036854775807+1",
		"1/0",
		"0.1",
	}
	if len(classes) != len(exprs) {
		t.Fatal("classes and exprs mismatch")
	}
	for i := range classes {
		_, _, err := Eval(exprs[i])
		if err == nil {
			t.Fatalf("Eval(%q): want error", exprs[i])
		}
		for j, other := range classes {
			got := errors.Is(err, other)
			want := i == j
			if got != want {
				t.Errorf("Eval(%q) errors.Is(%v) = %v, want %v",
					exprs[i], other, got, want)
			}
		}
	}
}

func TestDivideByZeroPosition(t *testing.T) {
	_, _, err := Eval("2/0+1")
	if !errors.Is(err, ErrDivideByZero) {
		t.Fatalf("err = %v", err)
	}
	var pe *PosError
	if !errors.As(err, &pe) || pe.Pos != 1 {
		t.Fatalf("err = %v, want slash pos 1", err)
	}
}

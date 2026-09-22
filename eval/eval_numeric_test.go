package eval

import (
	"errors"
	"testing"
)

func TestReturnTypeBoundaries(t *testing.T) {
	const maxInt64 = "9223372036854775807"
	const minInt64 = "-9223372036854775808"

	t.Run("max int64 literal", func(t *testing.T) {
		v, _, err := Eval(maxInt64)
		if err != nil || v != int64(9223372036854775807) {
			t.Fatalf("got %v, %v", v, err)
		}
	})
	t.Run("min int64 literal", func(t *testing.T) {
		v, _, err := Eval(minInt64)
		if err != nil || v != int64(-9223372036854775808) {
			t.Fatalf("got %v, %v", v, err)
		}
	})
	t.Run("one above max int64", func(t *testing.T) {
		_, _, err := Eval("9223372036854775808")
		if !errors.Is(err, ErrOverflow) {
			t.Fatalf("err = %v, want overflow", err)
		}
	})
	t.Run("one below min int64", func(t *testing.T) {
		_, _, err := Eval("-9223372036854775809")
		if !errors.Is(err, ErrOverflow) {
			t.Fatalf("err = %v, want overflow", err)
		}
	})
	t.Run("half is float64", func(t *testing.T) {
		v, _, err := Eval("1/2")
		if err != nil {
			t.Fatal(err)
		}
		if f, ok := v.(float64); !ok || f != 0.5 {
			t.Fatalf("got %v (%T)", v, v)
		}
	})
}

func TestArithmeticOverflow(t *testing.T) {
	overflowExprs := []string{
		"9223372036854775807+1",
		"-9223372036854775808-1",
		"9223372036854775807*2",
	}
	for _, expr := range overflowExprs {
		_, _, err := Eval(expr)
		if !errors.Is(err, ErrOverflow) {
			t.Errorf("Eval(%q): err = %v, want overflow", expr, err)
		}
	}
}

func TestDecimalNotExactlyRepresentable(t *testing.T) {
	for _, expr := range []string{"0.1", "0.2", "1.1", "0.1+0.2"} {
		_, _, err := Eval(expr)
		if !errors.Is(err, ErrNotRepresentable) {
			t.Errorf("Eval(%q): err = %v, want not representable", expr, err)
		}
	}
}

func TestExactDecimalsAndCancellation(t *testing.T) {
	cases := map[string]any{
		"0.5":       float64(0.5),
		"0.25":      float64(0.25),
		"0.5+0.5":   int64(1),
		"1.5+1.5":   int64(3),
		"0.1+0.9":   int64(1),
		"10.0":      int64(10),
		"2.5*4":     int64(10),
		"0.1*10":    int64(1),
		"0.3-0.3":   int64(0),
		"1.25*4":    int64(5),
		"3.75-0.75": int64(3),
	}
	for expr, want := range cases {
		v, _, err := Eval(expr)
		if err != nil {
			t.Errorf("Eval(%q): %v", expr, err)
			continue
		}
		if !valueEqual(v, want) {
			t.Errorf("Eval(%q) = %v (%T), want %v (%T)",
				expr, v, v, want, want)
		}
	}
}

func TestDivisionSemantics(t *testing.T) {
	t.Run("exact divide returns int64", func(t *testing.T) {
		v, _, err := Eval("6/3")
		if err != nil {
			t.Fatal(err)
		}
		if g, ok := v.(int64); !ok || g != 2 {
			t.Fatalf("got %v (%T)", v, v)
		}
	})
	t.Run("non-exact but binary representable returns float64", func(t *testing.T) {
		v, _, err := Eval("5/4")
		if err != nil {
			t.Fatal(err)
		}
		if f, ok := v.(float64); !ok || f != 1.25 {
			t.Fatalf("got %v (%T)", v, v)
		}
	})
	t.Run("non-binary fraction is not representable", func(t *testing.T) {
		_, _, err := Eval("1/3")
		if !errors.Is(err, ErrNotRepresentable) {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("divide by zero", func(t *testing.T) {
		_, _, err := Eval("1/0")
		if !errors.Is(err, ErrDivideByZero) {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("divide by zero after exact reduction", func(t *testing.T) {
		_, _, err := Eval("(2-2)/(1-1)")
		if !errors.Is(err, ErrDivideByZero) {
			t.Fatalf("err = %v", err)
		}
	})
}

func TestIntermediateValuesStayExact(t *testing.T) {
	// 1/3 + 1/3 + 1/3 == 1 exactly, even though 1/3 alone cannot be a
	// float64: intermediate values are kept as rationals.
	v, _, err := Eval("1/3+1/3+1/3")
	if err != nil {
		t.Fatal(err)
	}
	if g, ok := v.(int64); !ok || g != 1 {
		t.Fatalf("got %v (%T)", v, v)
	}
	// (0.1 + 0.2) * 10 == 3 exactly for the same reason.
	v, _, err = Eval("(0.1+0.2)*10")
	if err != nil {
		t.Fatal(err)
	}
	if g, ok := v.(int64); !ok || g != 3 {
		t.Fatalf("got %v (%T)", v, v)
	}
}

func TestInputNotMutated(t *testing.T) {
	expr := "  1 + 2 "
	orig := expr
	if _, _, err := Eval(expr); err != nil {
		t.Fatal(err)
	}
	if expr != orig {
		t.Fatalf("input changed: %q -> %q", orig, expr)
	}
}

package syntax_test

import (
	"errors"
	"testing"

	"ontology/class"
	"ontology/syntax"
)

func errOff(t *testing.T, err error) int {
	t.Helper()
	var oe *syntax.OffsetError
	if !errors.As(err, &oe) {
		t.Fatalf("want *OffsetError, got %T %v", err, err)
	}
	return oe.Offset
}

func TestCompileErrors(t *testing.T) {
	cases := []struct {
		pat  string
		kind class.Kind
		off  int
	}{
		{"abc[", class.KindUnclosed, 3},
		{"x[z-a]y", class.KindReversed, 3},
		{"a[]", class.KindEmpty, 1},
	}
	for _, c := range cases {
		_, err := syntax.Compile(c.pat, nil)
		if err == nil {
			t.Errorf("Compile(%q) = nil error", c.pat)
			continue
		}
		var ce *class.Error
		if !errors.As(err, &ce) || ce.Kind != c.kind {
			t.Errorf("Compile(%q) err=%v, want kind %d", c.pat, err, c.kind)
			continue
		}
		if got := errOff(t, err); got != c.off {
			t.Errorf("Compile(%q) offset=%d, want %d", c.pat, got, c.off)
		}
	}

	_, err := syntax.Compile(`abc\`, nil)
	if !errors.Is(err, syntax.ErrTrailingEscape) || errOff(t, err) != 3 {
		t.Fatalf("trailing escape: %v", err)
	}
	// 四类错误彼此可区分。
	kinds := []error{class.ErrUnclosed, class.ErrEmpty, class.ErrReversed, syntax.ErrTrailingEscape}
	for i, a := range kinds {
		for j, b := range kinds {
			if i != j && errors.Is(a, b) {
				t.Fatalf("error %v is %v", a, b)
			}
		}
	}
}

func TestLimits(t *testing.T) {
	if _, err := syntax.Compile("0123456789", &syntax.Limits{MaxBytes: 5}); !errors.Is(err, syntax.ErrTooLong) {
		t.Fatalf("MaxBytes: %v", err)
	}
	if _, err := syntax.Compile("**/**/**", &syntax.Limits{MaxDoubleStar: 2}); !errors.Is(err, syntax.ErrTooManyDoubleStar) {
		t.Fatalf("MaxDoubleStar: %v", err)
	}
}

// TestTruncateEveryByte：对若干合法模式在每个字节位置截断，
// 只允许编译成功或四类已知错误，且不 panic；'[' 之后必须是未闭合。
func TestTruncateEveryByte(t *testing.T) {
	pats := []string{`a/b`, `[!]a]`, `[a-]x`, `[\]]`, `a/**/b`, `x\y`, `[é-ë]`}
	for _, pat := range pats {
		for k := 0; k <= len(pat); k++ {
			func() {
				defer func() {
					if r := recover(); r != nil {
						t.Fatalf("panic on %q[:%d]: %v", pat, k, r)
					}
				}()
				_, err := syntax.Compile(pat[:k], nil)
				if err == nil {
					return
				}
				known := errors.Is(err, class.ErrUnclosed) || errors.Is(err, class.ErrEmpty) ||
					errors.Is(err, class.ErrReversed) || errors.Is(err, syntax.ErrTrailingEscape)
				if !known {
					t.Fatalf("%q[:%d] unknown error: %v", pat, k, err)
				}
				// 截断点恰好落在某个 '[' 之后且该类未闭合时，必须报未闭合。
				if k > 0 && pat[k-1] == '[' && !errors.Is(err, class.ErrUnclosed) {
					t.Fatalf("%q[:%d]: want unclosed, got %v", pat, k, err)
				}
			}()
		}
	}
}

func TestSegments(t *testing.T) {
	cases := []struct {
		pat string
		n   int
		ds  int
	}{
		{"", 1, 0}, {"a/**/b", 3, 1}, {"**/x", 2, 1}, {"x/**", 2, 1},
		{"a**b", 1, 0}, {"**.go", 1, 0}, {"x/", 2, 0}, {"/x", 2, 0},
	}
	for _, c := range cases {
		p, err := syntax.Compile(c.pat, nil)
		if err != nil {
			t.Fatalf("Compile(%q): %v", c.pat, err)
		}
		if len(p.Segs) != c.n {
			t.Errorf("Compile(%q) segs=%d, want %d", c.pat, len(p.Segs), c.n)
		}
		ds := 0
		for _, s := range p.Segs {
			if s.DoubleStar {
				ds++
			}
		}
		if ds != c.ds {
			t.Errorf("Compile(%q) doublestar=%d, want %d", c.pat, ds, c.ds)
		}
	}
}

package syntax_test

import (
	"errors"
	"testing"

	"ontology/engine"
	"ontology/runes"
	"ontology/syntax"
)

func TestRunes(t *testing.T) {
	cases := []struct {
		name string
		s    string
		n    int
	}{
		{"ascii", "abc", 3},
		{"utf8", "éë", 2},
		{"bad", "a\xffb", 3},
		{"allbad", "\xff\xfe", 2},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := len(runes.Items(c.s)); got != c.n {
				t.Fatalf("Items(%q)=%d want %d", c.s, got, c.n)
			}
		})
	}
	if !runes.Eq(runes.Items("\xff")[0], runes.Items("\xff")[0]) {
		t.Fatal("invalid bytes with same value must compare equal")
	}
	if runes.Eq(runes.Items("\xff")[0], runes.Items("\xfe")[0]) {
		t.Fatal("distinct invalid bytes must differ")
	}
}

func TestCompileErrors(t *testing.T) {
	cases := []struct {
		pat    string
		kind   error
		offset int
	}{
		{"[a", syntax.ErrUnterminatedClass, 2},
		{"[z-a]", syntax.ErrReversedRange, 3},
		{`abc\`, syntax.ErrTrailingSlashEsc, 3},
		{"[]", syntax.ErrEmptyClass, 0},
	}
	for _, c := range cases {
		_, err := syntax.Compile(c.pat, syntax.DefaultLimits)
		var se *syntax.Error
		if !errors.As(err, &se) || !errors.Is(err, c.kind) || se.Offset != c.offset {
			t.Fatalf("Compile(%q)=%v want %v@%d", c.pat, err, c.kind, c.offset)
		}
	}
}

func TestClassCases(t *testing.T) {
	cases := []struct {
		pat, path string
		want      bool
	}{
		{"[]a]", "]", true}, {"[]a]", "a", true}, {"[]a]", "b", false},
		{"[!]a]", "b", true}, {"[!]a]", "]", false}, {"[!]a]", "a", false},
		{"[!]a]", "/", false},
		{"[a-]", "a", true}, {"[a-]", "-", true}, {"[a-]", "b", false},
		{`[\]]`, "]", true}, {`[\]]`, "[", false},
		{"[é-ë]", "é", true}, {"[é-ë]", "ê", true}, {"[é-ë]", "ë", true},
		{"[é-ë]", "a", false}, {"[é-ë]", "\xff", false},
	}
	for _, c := range cases {
		m, err := engine.New(c.pat)
		if err != nil {
			t.Fatalf("Compile(%q): %v", c.pat, err)
		}
		if got := m.Match(c.path); got != c.want {
			t.Fatalf("%q.Match(%q)=%v want %v", c.pat, c.path, got, c.want)
		}
	}
}

func TestTruncation(t *testing.T) {
	pats := []string{"a/**/b", "[é-ë]/x", `[\]]`, "a*b?c", "[!]a]z", `x\\y`}
	for _, pat := range pats {
		for i := 0; i <= len(pat); i++ {
			func() {
				defer func() {
					if r := recover(); r != nil {
						t.Fatalf("panic on %q[:%d]: %v", pat, i, r)
					}
				}()
				m, err := engine.New(pat[:i])
				if err == nil {
					m.Match(pat[:i])
				}
			}()
		}
	}
	m, err := engine.New("[")
	if err == nil || !errors.Is(err, syntax.ErrUnterminatedClass) {
		t.Fatalf("truncate after '[' must be unterminated, got %v %v", m, err)
	}
}

func TestLimits(t *testing.T) {
	if _, err := syntax.Compile("abc", syntax.Limits{MaxBytes: 2}); !errors.Is(err, syntax.ErrPatternTooLong) {
		t.Fatalf("MaxBytes: %v", err)
	}
	if _, err := syntax.Compile("**/**/**", syntax.Limits{MaxDoubleStars: 2}); !errors.Is(err, syntax.ErrTooManyDoubleStar) {
		t.Fatalf("MaxDoubleStars: %v", err)
	}
}

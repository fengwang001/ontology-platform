package engine_test

import (
	"errors"
	"strings"
	"testing"

	"ontology/engine"
	"ontology/runes"
	"ontology/syntax"
)

func TestSyntaxErrors(t *testing.T) {
	cases := []struct {
		pat    string
		kind   error
		offset int
	}{
		{"[", syntax.ErrUnterminatedClass, 0},
		{"ab[", syntax.ErrUnterminatedClass, 2},
		{"a[b-c", syntax.ErrUnterminatedClass, 1},
		{"[]", syntax.ErrEmptyClass, 0},
		{"x[]", syntax.ErrEmptyClass, 1},
		{"[z-a]", syntax.ErrReversedRange, 3},
		{`a\`, syntax.ErrTrailingBackslash, 1},
		{`a/b\`, syntax.ErrTrailingBackslash, 3},
	}
	for _, c := range cases {
		p, err := syntax.Compile(c.pat, syntax.Limits{})
		if err == nil {
			t.Errorf("Compile(%q) succeeded, want %v", c.pat, c.kind)
			continue
		}
		if p != nil {
			t.Errorf("Compile(%q) returned a usable pattern on error", c.pat)
		}
		if !errors.Is(err, c.kind) {
			t.Errorf("Compile(%q) error = %v, want kind %v", c.pat, err, c.kind)
		}
		var se *syntax.Error
		if errors.As(err, &se) && se.Offset != c.offset {
			t.Errorf("Compile(%q) offset = %d, want %d", c.pat, se.Offset, c.offset)
		}
	}
}

func TestTruncation(t *testing.T) {
	kinds := []error{syntax.ErrUnterminatedClass, syntax.ErrEmptyClass,
		syntax.ErrReversedRange, syntax.ErrTrailingBackslash}
	pats := []string{"a[b-c]d", "x/**/y", `a\?b`, "[é-ë]z", "[]a]b"}
	for _, pat := range pats {
		for i := 0; i < len(pat); i++ {
			sub := pat[:i]
			_, err := syntax.Compile(sub, syntax.Limits{})
			if err == nil {
				continue
			}
			ok := false
			for _, k := range kinds {
				ok = ok || errors.Is(err, k)
			}
			if !ok {
				t.Errorf("Compile(%q[:%d]) = %v, not a decidable kind", pat, i, err)
			}
		}
	}
	// Cuts inside the class of "a[b-c]d" must be "unterminated".
	for i := 2; i <= 5; i++ {
		if _, err := syntax.Compile("a[b-c]d"[:i], syntax.Limits{}); !errors.Is(err, syntax.ErrUnterminatedClass) {
			t.Errorf("Compile(a[b-c]d[:%d]) = %v, want unterminated", i, err)
		}
	}
	if _, err := syntax.Compile("[]a]b"[:2], syntax.Limits{}); !errors.Is(err, syntax.ErrEmptyClass) {
		t.Errorf("Compile([]) = %v, want empty class", err)
	}
}

func TestLimits(t *testing.T) {
	if _, err := syntax.Compile("abcde", syntax.Limits{MaxBytes: 4}); !errors.Is(err, syntax.ErrTooLong) {
		t.Errorf("long pattern: %v, want ErrTooLong", err)
	}
	if _, err := syntax.Compile("**/**/x", syntax.Limits{MaxGlobstars: 1}); !errors.Is(err, syntax.ErrTooManyGlobstars) {
		t.Errorf("globstars: %v, want ErrTooManyGlobstars", err)
	}
}

func atomsOf(p *syntax.Pattern) int {
	n := 0
	for _, s := range p.Segs {
		if s.Glob {
			n++
		} else {
			n += len(s.Atoms)
		}
	}
	return n
}

func TestSteps(t *testing.T) {
	deep := func(n int) string { return strings.Repeat("a/", n-1) + "a" }
	cases := []struct {
		pat       string
		path100   string
		path10000 string
	}{
		{"a*a*a*a*a*a*a*a*b", strings.Repeat("a", 100), strings.Repeat("a", 10000)},
		{"**/**/**/**/**/x", deep(100), deep(10000)},
	}
	for _, c := range cases {
		p, err := syntax.Compile(c.pat, syntax.Limits{})
		if err != nil {
			t.Fatal(err)
		}
		atoms := atomsOf(p)
		var prev int64
		for i, path := range []string{c.path100, c.path10000} {
			if engine.Match(p, path) {
				t.Errorf("Match(%q, len %d) = true, want false", c.pat, len(path))
			}
			steps, n := engine.LastSteps(), int64(runes.Len([]byte(path)))
			if bound := 4 * int64(atoms) * n; steps > bound {
				t.Errorf("%q len %d: steps %d > bound %d", c.pat, n, steps, bound)
			}
			if i == 1 && steps > 200*prev {
				t.Errorf("%q: steps %d (len %d) > 200x %d", c.pat, steps, n, prev)
			}
			prev = steps
		}
	}
}

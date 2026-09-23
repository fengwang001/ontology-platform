package syntax_test

import (
	"errors"
	"strings"
	"testing"

	"ontology/class"
	"ontology/syntax"
)

func TestCompileErrors(t *testing.T) {
	cases := []struct {
		pat    string
		kind   class.Kind
		offset int
	}{
		{"[", class.KindUnclosed, 0},
		{"a[", class.KindUnclosed, 1},
		{"a[b", class.KindUnclosed, 1},
		{`[\`, class.KindUnclosed, 0},
		{"x/y[", class.KindUnclosed, 3},
		{"[z-a]", class.KindReversed, 2},
		{"x/[z-a]", class.KindReversed, 4},
		{`ab\`, class.KindBackslash, 2},
		{`\`, class.KindBackslash, 0},
		{"[]", class.KindEmpty, 0},
		{"[!]", class.KindEmpty, 0},
		{"[^]", class.KindEmpty, 0},
	}
	for _, c := range cases {
		p, err := syntax.Compile(c.pat, syntax.Limits{})
		if p != nil {
			t.Errorf("Compile(%q) returned a usable pattern on error", c.pat)
		}
		var ce *class.Error
		if !errors.As(err, &ce) {
			t.Errorf("Compile(%q) err=%v, want *class.Error", c.pat, err)
			continue
		}
		if ce.Kind != c.kind || ce.Offset != c.offset {
			t.Errorf("Compile(%q) = kind %d off %d, want kind %d off %d",
				c.pat, ce.Kind, ce.Offset, c.kind, c.offset)
		}
	}
}

func TestCompileValid(t *testing.T) {
	valid := []string{"", "*", "**", "?", "a/**/b", "[]a]", "[!]a]", "[^a]",
		"[a-]", "[é-ë]", `[\]]`, `a\*b`, "**.go", "x/**", "[]]", "\xff", "a\xffb"}
	for _, pat := range valid {
		if _, err := syntax.Compile(pat, syntax.Limits{}); err != nil {
			t.Errorf("Compile(%q) unexpected error: %v", pat, err)
		}
	}
}

func TestLimits(t *testing.T) {
	if _, err := syntax.Compile("abcde", syntax.Limits{MaxBytes: 3}); !errors.Is(err, syntax.ErrTooLong) {
		t.Errorf("long pattern: %v", err)
	}
	if _, err := syntax.Compile("a/**/**/b", syntax.Limits{MaxDouble: 1}); !errors.Is(err, syntax.ErrTooManyDouble) {
		t.Errorf("too many **: %v", err)
	}
}

func TestTruncation(t *testing.T) {
	pats := []string{"a[bc]d", "[é-ë]x", `a\y`, "[]a]", "a/**/b", `[\]]`, "[!^z]", "x\\"}
	for _, pat := range pats {
		for i := 0; i < len(pat); i++ {
			_, err := syntax.Compile(pat[:i], syntax.Limits{})
			if err == nil {
				continue
			}
			var ce *class.Error
			if !errors.As(err, &ce) {
				t.Errorf("Compile(%q) err=%v, want a syntax error kind", pat[:i], err)
			}
		}
	}
	// Cuts inside an unclosed class must report KindUnclosed.
	for _, pat := range []string{"[abc]", "x/[a-z]", "[é-ë]x", `[\]]`} {
		open := strings.Index(pat, "[")
		close := strings.Index(pat[open+1:], "]") + open + 1
		for i := open + 1; i <= close; i++ {
			_, err := syntax.Compile(pat[:i], syntax.Limits{})
			var ce *class.Error
			if !errors.As(err, &ce) || ce.Kind != class.KindUnclosed {
				t.Errorf("Compile(%q) = %v, want unclosed class", pat[:i], err)
			}
		}
	}
}

package pattern_test

import (
	"errors"
	"testing"

	"ontology/pattern"
)

func TestParseNormalizedLen(t *testing.T) {
	cases := []struct {
		pat  string
		want int
	}{
		{"", 0},
		{"abc", 3},
		{"%", 1},
		{"%%%", 1},
		{"%%%a", 2},
		{"%a%", 3},
		{"a%%b%c", 5},
		{"_", 1},
		{"caf_", 4},
		{`\%`, 1},
		{`\_`, 1},
		{`\\`, 1},
		{`\%\%`, 2},
		{"héllo", 5},
	}
	for _, c := range cases {
		p, err := pattern.Parse(c.pat)
		if err != nil {
			t.Fatalf("Parse(%q): %v", c.pat, err)
		}
		if p.Len() != c.want {
			t.Errorf("Parse(%q).Len() = %d, want %d", c.pat, p.Len(), c.want)
		}
	}
}

func TestFoldingShorterAndSameTokens(t *testing.T) {
	folded, err := pattern.Parse("%%%a")
	if err != nil {
		t.Fatal(err)
	}
	plain, err := pattern.Parse("%a")
	if err != nil {
		t.Fatal(err)
	}
	if folded.Len() >= len("%%%a") {
		t.Errorf("folded len %d not shorter than raw %d", folded.Len(), len("%%%a"))
	}
	if folded.Len() != plain.Len() {
		t.Fatalf("len mismatch: %d vs %d", folded.Len(), plain.Len())
	}
	for i, tok := range folded.Tokens() {
		if tok != plain.Tokens()[i] {
			t.Errorf("token %d differs: %+v vs %+v", i, tok, plain.Tokens()[i])
		}
	}
}

func TestEscapeLiterals(t *testing.T) {
	cases := []struct {
		pat  string
		want rune
	}{
		{`\%`, '%'},
		{`\_`, '_'},
		{`\\`, '\\'},
	}
	for _, c := range cases {
		p, err := pattern.Parse(c.pat)
		if err != nil {
			t.Fatalf("Parse(%q): %v", c.pat, err)
		}
		toks := p.Tokens()
		if len(toks) != 1 || toks[0].Kind != pattern.Lit || toks[0].Lit != c.want {
			t.Errorf("Parse(%q) = %+v, want literal %q", c.pat, toks, c.want)
		}
	}
}

func TestEscapeErrors(t *testing.T) {
	cases := []struct {
		pat    string
		kind   error
		offset int
	}{
		{`\x`, pattern.ErrInvalidEscape, 0},
		{`ab\x`, pattern.ErrInvalidEscape, 2},
		{`\é`, pattern.ErrInvalidEscape, 0},
		{`\`, pattern.ErrTrailingEscape, 0},
		{`abc\`, pattern.ErrTrailingEscape, 3},
	}
	for _, c := range cases {
		_, err := pattern.Parse(c.pat)
		if !errors.Is(err, c.kind) {
			t.Errorf("Parse(%q) err = %v, want errors.Is %v", c.pat, err, c.kind)
		}
		var ee *pattern.EscapeError
		if !errors.As(err, &ee) || ee.Offset != c.offset {
			t.Errorf("Parse(%q) offset = %+v, want %d", c.pat, err, c.offset)
		}
	}
	if _, err := pattern.Parse(`\%`); err != nil {
		t.Errorf(`Parse(\%%) unexpected err %v`, err)
	}
}

func TestInvalidUTF8Pattern(t *testing.T) {
	cases := []struct {
		pat    string
		offset int
	}{
		{"\xff", 0},
		{"ab\xc3", 2},
		{"a\xe4\xbd", 1},
		{"ok_\xff_", 3},
	}
	for _, c := range cases {
		_, err := pattern.Parse(c.pat)
		if !errors.Is(err, pattern.ErrInvalidUTF8) {
			t.Errorf("Parse(%q) err = %v, want ErrInvalidUTF8", c.pat, err)
		}
		var ue *pattern.UTF8Error
		if !errors.As(err, &ue) || ue.Side != "pattern" || ue.Offset != c.offset {
			t.Errorf("Parse(%q) = %+v, want side=pattern offset=%d", c.pat, err, c.offset)
		}
	}
}

func TestErrorClassesDistinct(t *testing.T) {
	if errors.Is(pattern.ErrInvalidEscape, pattern.ErrTrailingEscape) ||
		errors.Is(pattern.ErrInvalidEscape, pattern.ErrInvalidUTF8) ||
		errors.Is(pattern.ErrTrailingEscape, pattern.ErrInvalidUTF8) {
		t.Fatal("error classes must be distinct under errors.Is")
	}
}

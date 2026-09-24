package match_test

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"ontology/budget"
	"ontology/match"
	"ontology/pattern"
)

func mustParse(t *testing.T, pat string) pattern.Pattern {
	t.Helper()
	p, err := pattern.Parse(pat)
	if err != nil {
		t.Fatalf("Parse(%q): %v", pat, err)
	}
	return p
}

func TestMatchTable(t *testing.T) {
	cases := []struct {
		pat, text string
		want      bool
	}{
		{"", "", true},
		{"", "a", false},
		{"%", "", true},
		{"%", "anything %_ here", true},
		{"_", "", false},
		{"_", "a", true},
		{"_", "ab", false},
		{"___", "", false},
		{"____", "abc", false},
		{"abcdef", "abc", false},
		{"caf_", "café", true},
		{"caf_", "cafe", true},
		{"_", "😀", true},
		{"_", "😀x", false},
		{"__", "😀é", true},
		{`\%\_`, "%_", true},
		{`\%\_`, "ab", false},
		{`a\_b`, "a_b", true},
		{`a\_b`, "axb", false},
		{"a_b", "axb", true},
		{`100\%`, "100%", true},
		{"a%b", "ab", true},
		{"a%b", "axyzb", true},
		{"a%b", "axbyzb", true},
		{"%a%a", "aa", true},
		{"%a%a", "ba", false},
		{"h_llo w_rld", "hello world", true},
	}
	for _, c := range cases {
		got, err := match.Match(mustParse(t, c.pat), c.text, nil)
		if err != nil {
			t.Fatalf("Match(%q, %q): %v", c.pat, c.text, err)
		}
		if got != c.want {
			t.Errorf("Match(%q, %q) = %v, want %v", c.pat, c.text, got, c.want)
		}
	}
}

func TestCafUnderscoreRejectsFiveRunes(t *testing.T) {
	p := mustParse(t, "caf_")
	five := []string{"caféx", "xcafé", "cafeé", "caf😀e", "ccafe"}
	for _, s := range five {
		if len([]rune(s)) != 5 {
			t.Fatalf("test bug: %q is not 5 runes", s)
		}
		if got, _ := match.Match(p, s, nil); got {
			t.Errorf("Match(caf_, %q) = true, want false", s)
		}
	}
}

func TestInvalidUTF8Text(t *testing.T) {
	p := mustParse(t, "a_c")
	for _, c := range []struct {
		text   string
		offset int
	}{
		{"\xff", 0},
		{"ab\xffc", 2},
		{"ok\xe4\xbd", 2},
	} {
		_, err := match.Match(p, c.text, nil)
		if !errors.Is(err, pattern.ErrInvalidUTF8) {
			t.Errorf("Match text %q err = %v, want ErrInvalidUTF8", c.text, err)
		}
		var ue *pattern.UTF8Error
		if !errors.As(err, &ue) || ue.Side != "text" || ue.Offset != c.offset {
			t.Errorf("Match text %q = %+v, want side=text offset=%d", c.text, err, c.offset)
		}
	}
}

func adversarial(k int) (pattern.Pattern, string) {
	p, _ := pattern.Parse(strings.Repeat("%a", k-1) + "%b")
	return p, strings.Repeat("a", 20)
}

func TestAdversarialStepsBounded(t *testing.T) {
	for _, k := range []int{2, 4, 8} {
		p, text := adversarial(k)
		m := budget.New(0)
		got, err := match.Match(p, text, m)
		if err != nil || got {
			t.Fatalf("k=%d: got %v, %v; want false, nil", k, got, err)
		}
		bound := int64(4 * p.Len() * len([]rune(text)))
		if m.Steps() > bound {
			t.Errorf("k=%d: steps %d exceed bound %d", k, m.Steps(), bound)
		}
		t.Logf("k=%d m=%d n=20 steps=%d bound=%d", k, p.Len(), m.Steps(), bound)
	}
}

func TestBudgetExhausted(t *testing.T) {
	p, text := adversarial(8)
	_, err := match.Match(p, text, budget.New(1))
	if !errors.Is(err, budget.ErrExhausted) {
		t.Fatalf("err = %v, want ErrExhausted", err)
	}
}

func TestFoldingSameMatchSet(t *testing.T) {
	folded := mustParse(t, "%%%a")
	plain := mustParse(t, "%a")
	texts := []string{"", "a", "ba", "ab", "xaaay", "%%a", "😀a", "nan"}
	for _, s := range texts {
		g1, _ := match.Match(folded, s, nil)
		g2, _ := match.Match(plain, s, nil)
		if g1 != g2 {
			t.Errorf("text %q: folded=%v but plain=%v", s, g1, g2)
		}
	}
}

func TestDeterministic(t *testing.T) {
	p, text := adversarial(8)
	first, _ := match.Match(p, text, nil)
	m0 := budget.New(0)
	match.Match(p, text, m0)
	for i := 0; i < 1000; i++ {
		m := budget.New(0)
		got, err := match.Match(p, text, m)
		if err != nil || got != first || m.Steps() != m0.Steps() {
			t.Fatalf("iter %d: got=%v err=%v steps=%d, want %v nil %d",
				i, got, err, m.Steps(), first, m0.Steps())
		}
	}
}

func TestNoMutation(t *testing.T) {
	p := mustParse(t, "a%b_c")
	before := fmt.Sprintf("%v", p.Tokens())
	text := "axbyc"
	match.Match(p, text, nil)
	if after := fmt.Sprintf("%v", p.Tokens()); after != before {
		t.Errorf("pattern tokens mutated: %s -> %s", before, after)
	}
	if text != "axbyc" {
		t.Errorf("text mutated: %q", text)
	}
}

func TestConcurrent(t *testing.T) {
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			p, text := adversarial(2 + g)
			m0 := budget.New(0)
			match.Match(p, text, m0)
			for i := 0; i < 50; i++ {
				m := budget.New(0)
				got, err := match.Match(p, text, m)
				if err != nil || got {
					t.Errorf("g=%d: got %v, %v", g, got, err)
					return
				}
				if m.Steps() != m0.Steps() {
					t.Errorf("g=%d iter=%d: steps %d, want %d",
						g, i, m.Steps(), m0.Steps())
					return
				}
			}
		}(g)
	}
	wg.Wait()
}

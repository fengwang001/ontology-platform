package match

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"ontology/budget"
	"ontology/pattern"
)

func TestSemantics(t *testing.T) {
	cases := []struct {
		name string
		pat  string
		text string
		want bool
	}{
		{"empty both", "", "", true},
		{"empty pattern nonempty text", "", "a", false},
		{"percent matches empty", "%", "", true},
		{"percent matches anything", "%", "a%_z", true},
		{"underscore rejects empty", "_", "", false},
		{"caf underscore matches cafe", "caf_", "cafe", true},
		{"caf underscore matches accented", "caf_", "café", true},
		{"caf underscore rejects five codepoints", "caf_", "cafés", false},
		{"underscore matches four-byte emoji", "_", "😀", true},
		{"emoji between literals", "a_b", "a😀b", true},
		{"pattern longer than text underscores", "___", "ab", false},
		{"pattern longer than text literals", "abc", "ab", false},
		{"escaped percent literal match", `\%`, "%", true},
		{"escaped percent literal reject", `\%`, "a", false},
		{"escaped underscore literal match", `\_`, "_", true},
		{"escaped backslash literal match", `\\`, `\`, true},
		{"literal meta in text needs escape", `a\_b`, "a_b", true},
		{"unescaped underscore is wildcard", "a_b", "axb", true},
		{"unescaped underscore not literal", "a_b", "a_b", true},
		{"anchor hard case", "%a%b", "aaabbb", true},
		{"anchor hard case miss", "%a%b", "aaa", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Match(tc.pat, tc.text, budget.New(0))
			if err != nil || got != tc.want {
				t.Fatalf("Match(%q,%q)=%v,%v want %v", tc.pat, tc.text, got, err, tc.want)
			}
		})
	}
}

func TestFoldEquivalence(t *testing.T) {
	texts := []string{"", "a", "aaa", "ba", "ab", "baa", "xyz", "aaaaaa"}
	for _, text := range texts {
		c1 := budget.New(0)
		c2 := budget.New(0)
		r1, err1 := Match("%%%a", text, c1)
		r2, err2 := Match("%a", text, c2)
		if err1 != nil || err2 != nil || r1 != r2 {
			t.Fatalf("fold mismatch on %q: %v(%v) vs %v(%v)", text, r1, err1, r2, err2)
		}
	}
	p1, _ := pattern.Parse("%%%a")
	p2, _ := pattern.Parse("%a")
	if p1.Len() != p2.Len() {
		t.Fatalf("folded lengths differ: %d vs %d", p1.Len(), p2.Len())
	}
}

func worstPattern(k int) string {
	var b strings.Builder
	for i := 0; i < k; i++ {
		b.WriteString("%a")
	}
	b.WriteString("%b")
	return b.String()
}

func TestLinearStepBound(t *testing.T) {
	text := strings.Repeat("a", 20)
	for _, k := range []int{2, 4, 8} {
		pat := worstPattern(k)
		p, _ := pattern.Parse(pat)
		c := budget.New(0)
		ok, err := Match(pat, text, c)
		bound := 4 * p.Len() * len([]rune(text))
		t.Logf("k=%d patternLen=%d textLen=%d steps=%d bound=%d result=%v",
			k, p.Len(), 20, c.Steps(), bound, ok)
		if err != nil || ok {
			t.Fatalf("k=%d unexpected ok=%v err=%v", k, ok, err)
		}
		if c.Steps() > bound {
			t.Fatalf("k=%d steps=%d exceed linear bound %d", k, c.Steps(), bound)
		}
	}
}

func TestErrors(t *testing.T) {
	cases := []struct {
		name string
		pat  string
		text string
		kind error
		pos  int
	}{
		{"invalid text utf8", "%", "ab\xff", pattern.ErrText, 2},
		{"invalid pattern utf8", "a\xffb", "a", pattern.ErrPattern, 1},
		{"bad escape", `\q`, "a", pattern.ErrPattern, 0},
		{"dangling escape", `a\`, "a", pattern.ErrPattern, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Match(tc.pat, tc.text, nil)
			if !errors.Is(err, tc.kind) {
				t.Fatalf("err=%v want kind %v", err, tc.kind)
			}
			var pe *pattern.PosError
			if !errors.As(err, &pe) || pe.BytePos() != tc.pos {
				t.Fatalf("err=%v want pos %d", err, tc.pos)
			}
		})
	}
}

func TestBudgetExceeded(t *testing.T) {
	_, err := Match("%a%b", strings.Repeat("a", 40), budget.New(10))
	if !errors.Is(err, budget.ErrBudget) {
		t.Fatalf("err=%v want ErrBudget", err)
	}
}

func TestDeterministic(t *testing.T) {
	pat, text := worstPattern(8), strings.Repeat("a", 20)
	want, _ := Match(pat, text, budget.New(0))
	wantSteps := budget.New(0)
	if _, err := Match(pat, text, wantSteps); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 1000; i++ {
		c := budget.New(0)
		got, err := Match(pat, text, c)
		if err != nil || got != want || c.Steps() != wantSteps.Steps() {
			t.Fatalf("iter %d: %v,%v steps=%d want %v,%d", i, got, err, c.Steps(), want, wantSteps.Steps())
		}
		if pat != worstPattern(8) || text != strings.Repeat("a", 20) {
			t.Fatal("input was modified")
		}
	}
}

func TestConcurrentIsolation(t *testing.T) {
	inputs := []struct{ pat, text string }{
		{worstPattern(8), strings.Repeat("a", 20)},
		{"caf_", "café"},
		{"%", "😀😀"},
		{`\%`, "%"},
	}
	var wg sync.WaitGroup
	errs := make(chan error, len(inputs)*4)
	for round := 0; round < 4; round++ {
		for _, in := range inputs {
			wg.Add(1)
			go func(pat, text string) {
				defer wg.Done()
				c := budget.New(0)
				if _, err := Match(pat, text, c); err != nil {
					errs <- fmt.Errorf("%q: %w", pat, err)
					return
				}
				if c.Steps() == 0 {
					errs <- fmt.Errorf("%q: zero steps", pat)
				}
			}(in.pat, in.text)
		}
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
}

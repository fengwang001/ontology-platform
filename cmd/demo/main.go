package main

import (
	"errors"
	"fmt"
	"math/rand/v2"
	"strings"

	"ontology/audit"
	"ontology/budget"
	"ontology/match"
	"ontology/pattern"
)

var fails int

func check(name string, ok bool, detail string) {
	tag := "OK"
	if !ok {
		tag = "FAIL"
		fails++
	}
	fmt.Printf("%s %s %s\n", tag, name, detail)
}

func main() {
	_, err := pattern.Parse(`ab\`)
	check("trailing-backslash-rejected", errors.Is(err, pattern.ErrPattern), "")

	folded, _ := pattern.Parse("%%%a")
	raw, _ := pattern.Parse("%a")
	check("adjacent-percent-folded", folded.Len() == raw.Len() && folded.Len() == 2,
		fmt.Sprintf("len=%d", folded.Len()))

	bc := budget.New(50)
	budgetHit := false
	for i := 0; i < 51; i++ {
		if err := bc.Tick(); err != nil && errors.Is(err, budget.ErrBudget) {
			budgetHit = true
		}
	}
	check("budget-limit-detected", budgetHit && bc.Steps() == 51,
		fmt.Sprintf("steps=%d", bc.Steps()))

	worst := func(k int) string {
		var b strings.Builder
		for i := 0; i < k; i++ {
			b.WriteString("%a")
		}
		b.WriteString("%b")
		return b.String()
	}
	hardText := strings.Repeat("a", 20)
	sc := budget.New(0)
	hardOK, hardErr := match.Match(worst(8), hardText, sc)
	wp, _ := pattern.Parse(worst(8))
	sbound := 4 * wp.Len() * len([]rune(hardText))
	check("8-percent-case-linear", hardErr == nil && !hardOK && sc.Steps() <= sbound,
		fmt.Sprintf("steps=%d bound=%d", sc.Steps(), sbound))

	foldSame := true
	for _, s := range []string{"", "a", "aaa", "ba", "abba", "baa", "zzz"} {
		r1, _ := match.Match("%%%a", s, budget.New(0))
		r2, _ := match.Match("%a", s, budget.New(0))
		foldSame = foldSame && r1 == r2
	}
	check("fold-same-language", foldSame, "")

	cafe, _ := match.Match("caf_", "café", budget.New(0))
	check("caf_-matches-cafe-codepoint", cafe, "")

	emoji, _ := match.Match("_", "😀", budget.New(0))
	check("underscore-matches-four-byte-emoji", emoji, "")

	_, utf8err := match.Match("%", "ab\xff", budget.New(0))
	utf8ok := errors.Is(utf8err, pattern.ErrText)
	pos := -1
	var pe *pattern.PosError
	if errors.As(utf8err, &pe) {
		pos = pe.BytePos()
	}
	check("invalid-utf8-side-and-bytepos", utf8ok && pos == 2, fmt.Sprintf("side=text pos=%d", pos))

	escLit := true
	escapeCases := []struct{ pat, text string }{
		{`\%`, "%"}, {`\_`, "_"}, {`\\`, `\`},
	}
	for _, tc := range escapeCases {
		r, _ := match.Match(tc.pat, tc.text, budget.New(0))
		escLit = escLit && r
	}
	check("three-escaped-literals", escLit, "")

	empty1, _ := match.Match("", "", budget.New(0))
	empty2, _ := match.Match("", "x", budget.New(0))
	check("empty-pattern-only-empty", empty1 && !empty2, "")

	base := budget.New(0)
	_, _ = match.Match(worst(8), hardText, base)
	det := true
	for i := 0; i < 1000; i++ {
		c := budget.New(0)
		r, _ := match.Match(worst(8), hardText, c)
		det = det && !r && c.Steps() == base.Steps()
	}
	check("1000-repeat-same-steps", det, fmt.Sprintf("steps=%d", base.Steps()))

	rng := rand.New(rand.NewPCG(0xC0FFEE, 0xC0FFEE^0x9E3779B9))
	cases := audit.GenerateCases(rng, 2000, 8, 10)
	mismatches := audit.CrossCheck(cases)
	check("2000-random-crosschecks-agree", len(mismatches) == 0,
		fmt.Sprintf("pairs=%d mismatches=%d", len(cases), len(mismatches)))

	if fails > 0 {
		fmt.Printf("TOTAL: %d FAIL\n", fails)
		return
	}
	fmt.Println("TOTAL: all checks passed")
}

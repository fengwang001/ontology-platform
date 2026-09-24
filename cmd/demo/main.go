package main

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"ontology/audit"
	"ontology/budget"
	"ontology/match"
	"ontology/pattern"
)

var passed, failed int

func report(ok bool, format string, args ...any) {
	status := "OK"
	if !ok {
		status = "FAIL"
	}
	fmt.Printf("%s %s\n", status, fmt.Sprintf(format, args...))
	if ok {
		passed++
	} else {
		failed++
	}
}

func main() {
	escOK := true
	for _, c := range [][2]string{{`\%`, "%"}, {`\_`, "_"}, {`\\`, `\`}} {
		p, err := pattern.Parse(c[0])
		toks := p.Tokens()
		escOK = escOK && err == nil && len(toks) == 1 &&
			toks[0].Kind == pattern.Lit && toks[0].Lit == rune(c[1][0])
	}
	report(escOK, "escapes: %s %s %s parse as literals", `\%`, `\_`, `\\`)

	_, err := pattern.Parse(`abc\`)
	report(errors.Is(err, pattern.ErrTrailingEscape), "trailing backslash: %v", err)

	_, err = pattern.Parse("ab\xffc")
	var ue *pattern.UTF8Error
	patSide := errors.Is(err, pattern.ErrInvalidUTF8) && errors.As(err, &ue) &&
		ue.Side == "pattern" && ue.Offset == 2
	anyP, _ := pattern.Parse("_")
	_, err = match.Match(anyP, "ab\xffc", nil)
	var te *pattern.UTF8Error
	textSide := errors.Is(err, pattern.ErrInvalidUTF8) && errors.As(err, &te) &&
		te.Side == "text" && te.Offset == 2
	report(patSide && textSide, "invalid UTF-8: %v / %v",
		&pattern.UTF8Error{Side: "pattern", Offset: 2},
		&pattern.UTF8Error{Side: "text", Offset: 2})

	folded, _ := pattern.Parse("%%%a")
	report(folded.Len() == 2 && folded.Len() < len("%%%a"),
		"folding: %s normalizes to %d tokens (raw %d bytes)", "%%%a", folded.Len(), len("%%%a"))

	m := budget.New(3)
	got := []bool{m.Step(), m.Step(), m.Step(), m.Step()}
	report(m.Steps() == 4 && got[0] && got[1] && got[2] && !got[3],
		"budget: meter counts %d steps, limit 3 denies 4th", m.Steps())

	p8, _ := pattern.Parse(strings.Repeat("%a", 7) + "%b")
	text20 := strings.Repeat("a", 20)
	m8 := budget.New(0)
	ok8, err8 := match.Match(p8, text20, m8)
	bound := int64(4 * p8.Len() * len([]rune(text20)))
	report(err8 == nil && !ok8 && m8.Steps() <= bound,
		"steps: 8-%% pattern %d steps <= bound %d", m8.Steps(), bound)

	plain, _ := pattern.Parse("%a")
	same := true
	texts := []string{"", "a", "ba", "ab", "xaaay", "😀a", "%%a"}
	for _, s := range texts {
		g1, _ := match.Match(folded, s, nil)
		g2, _ := match.Match(plain, s, nil)
		same = same && g1 == g2
	}
	report(same, "fold: %s and %s agree on %d texts", "%%%a", "%a", len(texts))

	caf, _ := pattern.Parse("caf_")
	okCaf, _ := match.Match(caf, "café", nil)
	no5, _ := match.Match(caf, "caféx", nil)
	report(okCaf && !no5, "caf_ matches café (4 runes), rejects 5-rune caféx")

	okEmoji, _ := match.Match(anyP, "😀", nil)
	report(okEmoji, "_ matches one 4-byte emoji")

	empty, _ := pattern.Parse("")
	e1, _ := match.Match(empty, "", nil)
	e2, _ := match.Match(empty, "a", nil)
	report(e1 && !e2, "empty pattern matches only empty string")

	agree := true
	for _, pr := range audit.Pairs(42, 2000) {
		ok, err := audit.Agree(pr[0], pr[1])
		agree = agree && err == nil && ok
	}
	report(agree, "audit: 2000 random pairs agree with naive reference")

	m0 := budget.New(0)
	r0, _ := match.Match(p8, text20, m0)
	det := true
	for i := 0; i < 1000; i++ {
		mi := budget.New(0)
		ri, _ := match.Match(p8, text20, mi)
		det = det && ri == r0 && mi.Steps() == m0.Steps()
	}
	report(det, "determinism: 1000 repeats, steps always %d", m0.Steps())

	fmt.Printf("TOTAL %d/%d OK\n", passed, passed+failed)
	if failed > 0 {
		os.Exit(1)
	}
}

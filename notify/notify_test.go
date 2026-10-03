package notify

import (
	"fmt"
	"math"
	"math/rand"
	"strings"
	"sync"
	"testing"
)

func asNotifyErr(t *testing.T, err error) *Error {
	t.Helper()
	e, ok := err.(*Error)
	if !ok {
		t.Fatalf("expected *notify.Error, got %T: %v", err, err)
	}
	return e
}

func TestFallbackChain(t *testing.T) {
	cases := []struct {
		loc, dl string
		want    []string
	}{
		{"zh-Hant-TW", "en", []string{"zh-Hant-TW", "zh-Hant", "zh", "en"}},
		{"zh-TW", "en", []string{"zh-TW", "zh", "en"}},
		{"en-US", "en", []string{"en-US", "en"}},
		{"en", "en", []string{"en"}},
		{"fr", "en", []string{"fr", "en"}},
		{"de-Latn-AT", "en", []string{"de-Latn-AT", "de-Latn", "de", "en"}},
	}
	for _, c := range cases {
		if got := fallbackChain(c.loc, c.dl); fmt.Sprint(got) != fmt.Sprint(c.want) {
			t.Fatalf("fallbackChain(%q,%q)=%v want %v", c.loc, c.dl, got, c.want)
		}
	}
}

func TestValidLang(t *testing.T) {
	valid := []string{"en", "zh", "ab", "eng", "zh-Hant", "en-US", "zh-Hant-TW", "de-Latn-AT"}
	invalid := []string{
		"", "E", "EN", "engl", "en-", "-en", "en-us", "en-usa", "EN-US",
		"zh-hant", "zh-hAnt", "zh-HANT", "zh-US-TW",
		"zh-Hant-Tw", "zh-Hant--TW", "a-b", "ab-x", "en-US-X", "x-Y",
		"en-Hant-US-X", "en-Hant-us",
	}
	for _, l := range valid {
		if !validLang(l) {
			t.Errorf("expected valid lang %q", l)
		}
	}
	for _, l := range invalid {
		if validLang(l) {
			t.Errorf("expected invalid lang %q", l)
		}
	}
}

func TestSpecExample(t *testing.T) {
	s, err := New("en")
	if err != nil {
		t.Fatal(err)
	}
	mustPub := func(name, loc, ch, body string, eff int64) {
		t.Helper()
		if err := s.Publish(name, loc, ch, body, eff); err != nil {
			t.Fatalf("publish %s/%s/%s: %v", name, loc, ch, err)
		}
	}
	mustPub("welcome", "en", "*", "Hi {name}", 0)
	mustPub("welcome", "zh", "sms", "你好{name}，验证码{code}", 0)
	mustPub("welcome", "zh-Hant", "*", "您好 {name|貴賓}", 100)

	r, err := s.Render("welcome", "zh-Hant-TW", "sms", 150, map[string]string{"name": "Li"})
	if err != nil {
		t.Fatal(err)
	}
	if r.Text != "您好 Li" || r.Loc != "zh-Hant" || r.Ch != "*" || r.Eff != 100 {
		t.Fatalf("got %+v", r)
	}
	if r.lookups != 8 {
		t.Fatalf("lookups=%d want 8", r.lookups)
	}

	r, err = s.Render("welcome", "zh-Hant-TW", "sms", 50, map[string]string{"name": "Li"})
	if err != nil {
		t.Fatal(err)
	}
	if r.Text != "Hi Li" || r.Loc != "en" || r.Ch != "*" || r.Eff != 0 {
		t.Fatalf("got %+v", r)
	}

	_, err = s.Render("welcome", "zh-Hant-TW", "sms", 50, nil)
	e := asNotifyErr(t, err)
	if e.Kind != KindMissingVars || fmt.Sprint(e.Missing) != "[code name]" {
		t.Fatalf("got kind=%d missing=%v", e.Kind, e.Missing)
	}
}

func TestEffBoundary(t *testing.T) {
	s, _ := New("en")
	if err := s.Publish("n", "en", "*", "v1", 10); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Render("n", "en", "sms", 9, nil); asNotifyErr(t, err).Kind != KindNoTemplate {
		t.Fatalf("eff>at must not hit, got %v", err)
	}
	r, err := s.Render("n", "en", "sms", 10, nil)
	if err != nil || r.Text != "v1" || r.Eff != 10 {
		t.Fatalf("eff==at must hit, got %+v err=%v", r, err)
	}
}

func TestSameEffOverwriteAndTomb(t *testing.T) {
	s, _ := New("en")
	pub := func(body string, eff int64) {
		t.Helper()
		if err := s.Publish("k", "en", "sms", body, eff); err != nil {
			t.Fatal(err)
		}
	}
	pub("first", 5)
	pub("second", 5)
	r, err := s.Render("k", "en", "sms", 10, nil)
	if err != nil || r.Text != "second" {
		t.Fatalf("overwrite: %+v %v", r, err)
	}

	if err := s.Retire("k", "en", "sms", 5); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Render("k", "en", "sms", 10, nil); asNotifyErr(t, err).Kind != KindNoTemplate {
		t.Fatalf("tombstone: %v", err)
	}

	pub("revived", 5)
	r, err = s.Render("k", "en", "sms", 10, nil)
	if err != nil || r.Text != "revived" {
		t.Fatalf("republish over tomb: %+v %v", r, err)
	}
}

func TestTombSkipsWithoutBlocking(t *testing.T) {
	s, _ := New("en")
	if err := s.Publish("k", "en", "*", "generic", 0); err != nil {
		t.Fatal(err)
	}
	if err := s.Retire("k", "en", "sms", 0); err != nil {
		t.Fatal(err)
	}
	r, err := s.Render("k", "en", "sms", 0, nil)
	if err != nil || r.Text != "generic" || r.Ch != "*" {
		t.Fatalf("tomb on sms must fall through to *, got %+v err=%v", r, err)
	}

	if err := s.Publish("k2", "zh", "sms", "ZH", 0); err != nil {
		t.Fatal(err)
	}
	if err := s.Retire("k2", "zh-TW", "sms", 0); err != nil {
		t.Fatal(err)
	}
	r, err = s.Render("k2", "zh-TW", "sms", 0, nil)
	if err != nil || r.Loc != "zh" || r.Text != "ZH" {
		t.Fatalf("got %+v err=%v", r, err)
	}
}

func TestLanguageOuterChannelInner(t *testing.T) {
	s, _ := New("en")
	if err := s.Publish("p", "zh", "sms", "lang-specific-channel-specific", 0); err != nil {
		t.Fatal(err)
	}
	if err := s.Publish("p", "zh-TW", "*", "closer-lang-generic-channel", 0); err != nil {
		t.Fatal(err)
	}
	r, err := s.Render("p", "zh-TW", "sms", 0, nil)
	if err != nil || r.Text != "closer-lang-generic-channel" {
		t.Fatalf("language must be outer over channel specificity: %+v %v", r, err)
	}
}

func TestMissingFallbackAndFirstReason(t *testing.T) {
	s, _ := New("en")
	if err := s.Publish("m", "zh", "sms", "{a}{b}", 0); err != nil {
		t.Fatal(err)
	}
	if err := s.Publish("m", "en", "*", "{c}", 0); err != nil {
		t.Fatal(err)
	}
	r, err := s.Render("m", "zh", "sms", 0, map[string]string{"c": "see"})
	if err != nil || r.Text != "see" || r.Loc != "en" {
		t.Fatalf("got %+v %v", r, err)
	}
	_, err = s.Render("m", "zh", "sms", 0, map[string]string{"a": ""})
	e := asNotifyErr(t, err)
	if e.Kind != KindMissingVars || fmt.Sprint(e.Missing) != "[b]" {
		t.Fatalf("got %+v", e)
	}
}

func TestEmptyStringVsMissing(t *testing.T) {
	s, _ := New("en")
	if err := s.Publish("v", "en", "*", "[{a}][{b|def}]", 0); err != nil {
		t.Fatal(err)
	}
	r, err := s.Render("v", "en", "sms", 0, map[string]string{"a": ""})
	if err != nil || r.Text != "[][def]" {
		t.Fatalf("got %+v err=%v", r, err)
	}
	r, err = s.Render("v", "en", "sms", 0, map[string]string{"a": "x", "b": ""})
	if err != nil || r.Text != "[x][]" {
		t.Fatalf("got %+v err=%v", r, err)
	}
	_, err = s.Render("v", "en", "sms", 0, nil)
	if asNotifyErr(t, err).Kind != KindMissingVars {
		t.Fatalf("got %v", err)
	}
}

func TestEscaping(t *testing.T) {
	s, _ := New("en")
	body := `{x|a<b>&"c"}`
	if err := s.Publish("e", "en", "*", body, 0); err != nil {
		t.Fatal(err)
	}
	r, err := s.Render("e", "en", "email", 0, map[string]string{"x": `<&>"`})
	if err != nil || r.Text != `&lt;&amp;&gt;&quot;` {
		t.Fatalf("email value escape: %q %v", r.Text, err)
	}
	r, err = s.Render("e", "en", "email", 0, nil)
	if err != nil || r.Text != `a<b>&"c"` {
		t.Fatalf("default text must not escape: %q %v", r.Text, err)
	}
	r, err = s.Render("e", "en", "sms", 0, map[string]string{"x": `<&>"`})
	if err != nil || r.Text != `<&>"` {
		t.Fatalf("sms must not escape: %q %v", r.Text, err)
	}
}

func TestLengthLimit(t *testing.T) {
	s, _ := New("en")
	if err := s.Publish("l", "en", "sms", strings.Repeat("好", 70), 0); err != nil {
		t.Fatal(err)
	}
	r, err := s.Render("l", "en", "sms", 0, nil)
	if err != nil || len([]rune(r.Text)) != 70 {
		t.Fatalf("70 codepoints must succeed: %+v %v", r, err)
	}
	if err := s.Publish("l", "en", "sms", strings.Repeat("好", 71), 1); err != nil {
		t.Fatal(err)
	}
	_, err = s.Render("l", "en", "sms", 1, nil)
	e := asNotifyErr(t, err)
	if e.Kind != KindTooLong || e.Length != 71 {
		t.Fatalf("71 codepoints too long: %+v", e)
	}

	if err := s.Publish("l", "en", "*", "short", 0); err != nil {
		t.Fatal(err)
	}
	r, err = s.Render("l", "en", "sms", 1, nil)
	if err != nil || r.Text != "short" || r.Ch != "*" {
		t.Fatalf("fallback after too long: %+v %v", r, err)
	}

	if err := s.Publish("mail", "en", "*", strings.Repeat("x", 1000), 0); err != nil {
		t.Fatal(err)
	}
	r, err = s.Render("mail", "en", "email", 0, nil)
	if err != nil || len(r.Text) != 1000 {
		t.Fatalf("email unlimited: %v", err)
	}
	if _, err := s.Render("mail", "en", "push", 0, nil); asNotifyErr(t, err).Kind != KindTooLong {
		t.Fatalf("push must reject long body: %v", err)
	}
}

func TestLiteralsAndSyntaxOffset(t *testing.T) {
	s, _ := New("en")
	if err := s.Publish("lit", "en", "*", "{{a}} x {{ }} }} {{", 0); err != nil {
		t.Fatal(err)
	}
	r, err := s.Render("lit", "en", "sms", 0, nil)
	if err != nil || r.Text != `{a} x { } } {` {
		t.Fatalf("literal braces: %q %v", r.Text, err)
	}

	offsets := map[string]int{
		"{a}}":   3, // 变量 a 后的单个 }
		"}":      0,
		"ab}":    2,
		"{a|x}}": 5,
		"{":      0,
		"{a":     0,
		"{A}":    0,
		"{a|":    0,
		"{a|x{":  0,
		"{a|x|}": 0,
	}
	for body, want := range offsets {
		_, perr := parseBody(body)
		if perr == nil || perr.Kind != KindSyntax || perr.Offset != want {
			t.Fatalf("parseBody(%q) = %+v, want syntax offset %d", body, perr, want)
		}
		err := s.Publish("bad", "en", "*", body, 0)
		pe := asNotifyErr(t, err)
		if pe.Kind != KindSyntax || pe.Offset != want {
			t.Fatalf("publish(%q) = %+v, want offset %d", body, pe, want)
		}
	}
}

func TestRejectionDoesNotMutate(t *testing.T) {
	s, _ := New("en")
	if err := s.Publish("ok", "en", "*", "base", 10); err != nil {
		t.Fatal(err)
	}
	before, _ := s.Render("ok", "en", "sms", 100, nil)

	badPub := []struct {
		name, loc, ch, body string
		eff                 int64
	}{
		{"BADNAME", "en", "*", "x", 0},
		{"ok", "en", "sms", "", 0},
		{"ok", "en", "sms", strings.Repeat("x", 1001), 0},
		{"ok", "en", "sms", "{Bad}", 0},
		{"ok", "EN", "*", "x", 0},
		{"ok", "en", "fax", "x", 0},
		{"ok", "en", "*", "x", maxEff + 1},
		{"ok", "en", "*", "x", -1},
	}
	for _, c := range badPub {
		if err := s.Publish(c.name, c.loc, c.ch, c.body, c.eff); err == nil {
			t.Fatalf("publish should reject %+v", c)
		}
	}
	if err := s.Retire("ok", "EN", "sms", 0); err == nil {
		t.Fatal("retire with bad lang must reject")
	}
	if err := s.Retire("ok", "en", "fax", 0); err == nil {
		t.Fatal("retire with bad channel must reject")
	}
	after, err := s.Render("ok", "en", "sms", 100, nil)
	if err != nil || after.Text != before.Text || after.Eff != before.Eff {
		t.Fatalf("state changed by rejected ops: before=%+v after=%+v", before, after)
	}

	if _, err := s.Render("ok", "en", "fax", 0, nil); asNotifyErr(t, err).Kind != KindInvalidParam {
		t.Fatalf("bad render channel: %v", err)
	}
	if _, err := s.Render("ok", "en", "sms", -1, nil); asNotifyErr(t, err).Kind != KindInvalidParam {
		t.Fatalf("negative at: %v", err)
	}
	if _, err := s.Render("ok", "en", "sms", 0, map[string]string{"BAD": "x"}); asNotifyErr(t, err).Kind != KindInvalidParam {
		t.Fatalf("bad var key: %v", err)
	}
	if _, err := s.Render("ok", "EN", "sms", 0, nil); asNotifyErr(t, err).Kind != KindInvalidLang {
		t.Fatalf("bad render lang: %v", err)
	}
	if _, err := New("EN"); err == nil {
		t.Fatal("New with bad dl must fail")
	}
}

func TestProbeBound(t *testing.T) {
	s, _ := New("en")
	const n = 100
	for i := 0; i < n; i++ {
		if err := s.Publish("k", "en", "sms", fmt.Sprintf("b%d", i), int64(i)); err != nil {
			t.Fatal(err)
		}
	}
	wantBound := int(math.Ceil(math.Log2(float64(n + 1))))
	globalMax := 0
	for at := int64(-1); at <= n; at++ {
		r, err := s.Render("k", "en", "sms", at, nil)
		_ = err
		probes := 0
		if r != nil {
			probes = r.maxProbe
		} else {
			_, _, probes0 := lookupAt(s.versions[tplKey{"k", "en", "sms"}], at)
			probes = probes0
		}
		if probes > globalMax {
			globalMax = probes
		}
	}
	if globalMax > wantBound {
		t.Fatalf("max probes %d > ceil(log2(n+1))=%d", globalMax, wantBound)
	}
	if globalMax < wantBound {
		t.Fatalf("bound not tight: max=%d bound=%d", globalMax, wantBound)
	}
}

func TestConcurrent(t *testing.T) {
	s, _ := New("en")
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				_ = s.Publish("c", "en", "*", fmt.Sprintf("v-%d-%d", i, j), int64(i*200+j))
				_, _ = s.Render("c", "en", "sms", int64(i*200+j), nil)
			}
		}(i)
	}
	wg.Wait()
}

func kindName(k ErrorKind) string {
	switch k {
	case KindInvalidParam:
		return "invalid-param"
	case KindInvalidLang:
		return "invalid-lang"
	case KindSyntax:
		return "syntax"
	case KindNoTemplate:
		return "no-template"
	case KindMissingVars:
		return "missing-vars"
	case KindTooLong:
		return "too-long"
	}
	return "unknown"
}

func errFingerprint(e *Error) string {
	if e == nil {
		return "ok"
	}
	switch e.Kind {
	case KindSyntax:
		return fmt.Sprintf("syntax@%d", e.Offset)
	case KindMissingVars:
		return "missing:" + strings.Join(e.Missing, ",")
	case KindTooLong:
		return fmt.Sprintf("toolong:%d", e.Length)
	default:
		return kindName(e.Kind)
	}
}

// TestDifferentialNaive 对拍 2000 组随机登记/退休/渲染序列，
// 朴素实现线性枚举全部候选，二者结果必须逐字段一致。
func TestDifferentialNaive(t *testing.T) {
	const trials = 2000
	rng := rand.New(rand.NewSource(20261003))
	langs := []string{"en", "zh", "zh-Hant", "zh-TW", "zh-Hant-TW", "de-Latn", "de-Latn-AT", "fr", "ja-JP"}
	names := []string{"welcome", "alert", "otp", "news.weekly", "x_1"}
	channels := []string{"sms", "push", "email", "*"}
	bodies := []string{
		"Hi {name}",
		"{greeting|Hi} {name}",
		"你好{name}，验证码{code}",
		"{{literal}} {x|<&>} {y|}",
		strings.Repeat("好", 80),
		"plain",
		"{a}{b|}{c}",
		"email-only & < > \" {v}\"",
	}
	varnames := []string{"name", "code", "greeting", "x", "y", "a", "b", "c", "v"}

	for trial := 0; trial < trials; trial++ {
		s, _ := New("en")
		n := newNaive("en")
		var log strings.Builder
		fmt.Fprintf(&log, "--- trial %d ---\n", trial)

		ops := 2 + rng.Intn(30)
		for op := 0; op < ops; op++ {
			switch rng.Intn(3) {
			case 0, 1: // publish
				name := names[rng.Intn(len(names))]
				loc := langs[rng.Intn(len(langs))]
				ch := channels[rng.Intn(len(channels))]
				body := bodies[rng.Intn(len(bodies))]
				eff := int64(rng.Intn(20))
				if rng.Intn(10) == 0 { // 偶尔制造非法/边界输入
					body = "{Oops}"
				}
				got := s.Publish(name, loc, ch, body, eff)
				want := n.publish(name, loc, ch, body, eff)
				fmt.Fprintf(&log, "publish(%q,%q,%q,%q,%d) => %s\n", name, loc, ch, body, eff, errFingerprint(errp(got)))
				if errFingerprint(errp(got)) != errFingerprint(want) {
					t.Fatalf("trial %d publish mismatch\n%sreal=%v naive=%v", trial, log.String(), got, want)
				}
			case 2: // retire
				name := names[rng.Intn(len(names))]
				loc := langs[rng.Intn(len(langs))]
				ch := channels[rng.Intn(len(channels))]
				eff := int64(rng.Intn(20))
				got := s.Retire(name, loc, ch, eff)
				want := n.retire(name, loc, ch, eff)
				fmt.Fprintf(&log, "retire(%q,%q,%q,%d) => %s\n", name, loc, ch, eff, errFingerprint(errp(got)))
				if errFingerprint(errp(got)) != errFingerprint(want) {
					t.Fatalf("trial %d retire mismatch\n%sreal=%v naive=%v", trial, log.String(), got, want)
				}
			}
		}

		// 若干渲染。
		for r := 0; r < 1+rng.Intn(3); r++ {
			name := names[rng.Intn(len(names))]
			loc := langs[rng.Intn(len(langs))]
			ch := channels[rng.Intn(3)] // 仅具体渠道
			at := int64(rng.Intn(22))
			vars := map[string]string{}
			for _, v := range varnames {
				switch rng.Intn(4) {
				case 0:
					vars[v] = "" // 存在但为空串
				case 1:
					vars[v] = []string{"Li", "<&>", "123456", "好", strings.Repeat("Z", 30)}[rng.Intn(5)]
				}
			}
			got, gerr := s.Render(name, loc, ch, at, vars)
			nr, nerr := n.render(name, loc, ch, at, vars)
			fmt.Fprintf(&log, "render(%q,%q,%q,%d,%v) => ", name, loc, ch, at, vars)
			if gerr != nil {
				fmt.Fprintf(&log, "ERR %s\n", errFingerprint(errp(gerr)))
			} else {
				fmt.Fprintf(&log, "OK text=%q loc=%q ch=%q eff=%d [lookups=%d maxProbe=%d]\n",
					got.Text, got.Loc, got.Ch, got.Eff, got.lookups, got.maxProbe)
			}
			if errFingerprint(errp(gerr)) != errFingerprint(nerr) {
				t.Fatalf("trial %d render error mismatch\n%sreal=%v naive=%v", trial, log.String(), gerr, nerr)
			}
			if gerr == nil {
				if nr == nil || got.Text != nr.text || got.Loc != nr.loc || got.Ch != nr.ch || got.Eff != nr.eff {
					t.Fatalf("trial %d render result mismatch\n%sreal=%+v naive=%+v", trial, log.String(), got, nr)
				}
				if got.lookups > 2*len(fallbackChain(loc, "en")) {
					t.Fatalf("trial %d too many lookups: %d > %d\n%s", trial, got.lookups, 2*len(fallbackChain(loc, "en")), log.String())
				}
				for k, vs := range s.versions {
					_, _, probes := lookupAt(vs, at)
					bound := int(math.Ceil(math.Log2(float64(len(vs) + 1))))
					if probes > bound {
						t.Fatalf("trial %d probe bound violated key=%v n=%d probes=%d bound=%d", trial, k, len(vs), probes, bound)
					}
				}
			}
		}
		if testing.Verbose() {
			t.Log(strings.TrimRight(log.String(), "\n"))
		}
	}
}

func errp(err error) *Error {
	if err == nil {
		return nil
	}
	return err.(*Error)
}

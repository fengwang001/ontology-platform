package notify

import (
	"strings"
	"testing"
)

func mustPublish(t *testing.T, s *Store, name, loc, ch, body string, eff int64) {
	t.Helper()
	if e := s.Publish(name, loc, ch, body, eff); e != nil {
		t.Fatalf("publish %s/%s/%s: %v", name, loc, ch, e)
	}
}

func TestFallbackChain(t *testing.T) {
	cases := []struct {
		loc, dl string
		want    []string
	}{
		{"zh-Hant-TW", "en", []string{"zh-Hant-TW", "zh-Hant", "zh", "en"}},
		{"zh-TW", "en", []string{"zh-TW", "zh", "en"}},
		{"en", "en", []string{"en"}},
		{"en-US", "en", []string{"en-US", "en"}},
		{"de-1901", "en", []string{"de-1901", "de", "en"}},
		{"fr", "en", []string{"fr", "en"}},
		{"zh-Hant", "zh", []string{"zh-Hant", "zh"}},
	}
	for _, c := range cases {
		got := fallbackChain(c.loc, c.dl)
		if strings.Join(got, ",") != strings.Join(c.want, ",") {
			t.Fatalf("chain(%q,%q)=%v want %v", c.loc, c.dl, got, c.want)
		}
	}
}

func TestValidateLanguage(t *testing.T) {
	valid := []string{"en", "zh", "abc", "zh-Hant", "zh-TW", "en-Latn-US"}
	invalid := []string{
		"", "e", "abcd", "ENG", "en-", "en-x", "en-hant",
		"zh-hant-tw", "zh-tw-US", "zh-Hant-us", "zh-Hant-US-X",
		"en--US", "en-US-Latn", "En", "en-latn", "zh-HANT", "de-1996",
	}
	for _, l := range valid {
		if !validateLanguage(l) {
			t.Errorf("expected valid: %q", l)
		}
	}
	for _, l := range invalid {
		if validateLanguage(l) {
			t.Errorf("expected invalid: %q", l)
		}
	}
}

func TestExampleSpec(t *testing.T) {
	s := New("en")
	mustPublish(t, s, "welcome", "en", "*", "Hi {name}", 0)
	mustPublish(t, s, "welcome", "zh", "sms", "你好{name}，验证码{code}", 0)
	mustPublish(t, s, "welcome", "zh-Hant", "*", "您好 {name|貴賓}", 100)

	r, err := s.Render("welcome", "zh-Hant-TW", "sms", 150, map[string]string{"name": "Li"})
	if err != nil {
		t.Fatal(err)
	}
	if r.Text != "您好 Li" || r.Loc != "zh-Hant" || r.ChannelKey != "*" || r.Eff != 100 {
		t.Fatalf("got %+v", r)
	}

	r, err = s.Render("welcome", "zh-Hant-TW", "sms", 50, map[string]string{"name": "Li"})
	if err != nil {
		t.Fatal(err)
	}
	if r.Text != "Hi Li" || r.Loc != "en" || r.ChannelKey != "*" {
		t.Fatalf("got %+v", r)
	}

	_, err = s.Render("welcome", "zh-Hant-TW", "sms", 50, map[string]string{})
	if err == nil || err.Reason != ReasonMissingVars {
		t.Fatalf("got %v", err)
	}
	if strings.Join(err.Missing, ",") != "code,name" {
		t.Fatalf("missing=%v", err.Missing)
	}
}

func TestEffBoundary(t *testing.T) {
	s := New("en")
	mustPublish(t, s, "n", "en", "sms", "a{v}", 10)
	if _, err := s.Render("n", "en", "sms", 10, map[string]string{"v": "1"}); err != nil {
		t.Fatalf("eff==at must hit: %v", err)
	}
	_, err := s.Render("n", "en", "sms", 9, nil)
	if err == nil || err.Reason != ReasonNoTemplate {
		t.Fatalf("eff>at must not hit, got %v", err)
	}
}

func TestSameEffOverrideAndTombstone(t *testing.T) {
	s := New("en")
	mustPublish(t, s, "n", "en", "sms", "first", 5)
	mustPublish(t, s, "n", "en", "sms", "second", 5)
	r, err := s.Render("n", "en", "sms", 10, nil)
	if err != nil || r.Text != "second" {
		t.Fatalf("override: %v %v", r, err)
	}

	if e := s.Retire("n", "en", "sms", 7); e != nil {
		t.Fatal(e)
	}
	_, err = s.Render("n", "en", "sms", 10, nil)
	if err == nil || err.Reason != ReasonNoTemplate {
		t.Fatalf("tombstone must skip key: %v", err)
	}
	r, err = s.Render("n", "en", "sms", 6, nil)
	if err != nil || r.Text != "second" {
		t.Fatalf("pre-tombstone version: %v %v", r, err)
	}

	mustPublish(t, s, "n", "en", "sms", "third", 7)
	r, err = s.Render("n", "en", "sms", 10, nil)
	if err != nil || r.Text != "third" {
		t.Fatalf("body over tombstone: %v %v", r, err)
	}
	if e := s.Retire("n", "en", "sms", 7); e != nil {
		t.Fatal(e)
	}
	if _, err = s.Render("n", "en", "sms", 10, nil); err == nil || err.Reason != ReasonNoTemplate {
		t.Fatalf("tombstone over body: %v", err)
	}
}

func TestTombstoneDoesNotBlockFallback(t *testing.T) {
	s := New("en")
	mustPublish(t, s, "n", "en-US", "sms", "us", 0)
	if e := s.Retire("n", "en-US", "sms", 5); e != nil {
		t.Fatal(e)
	}
	mustPublish(t, s, "n", "en", "sms", "base", 0)
	r, err := s.Render("n", "en-US", "sms", 10, nil)
	if err != nil || r.Text != "base" || r.Loc != "en" {
		t.Fatalf("fallback after tombstone: %v %v", r, err)
	}
}

func TestLanguageOuterChannelInner(t *testing.T) {
	s := New("en")
	mustPublish(t, s, "n", "zh-TW", "*", "star", 0)
	mustPublish(t, s, "n", "zh", "sms", "smsspecific", 0)
	r, err := s.Render("n", "zh-TW", "sms", 10, nil)
	if err != nil || r.Text != "star" || r.ChannelKey != "*" {
		t.Fatalf("language precedes channel specificity: %v %v", r, err)
	}

	s2 := New("en")
	mustPublish(t, s2, "n", "zh", "sms", "concrete", 0)
	mustPublish(t, s2, "n", "zh", "*", "generic", 0)
	r, err = s2.Render("n", "zh", "sms", 10, nil)
	if err != nil || r.Text != "concrete" || r.ChannelKey != "sms" {
		t.Fatalf("concrete channel precedes *: %v %v", r, err)
	}
}

func TestMissingFallbackAndFirstReason(t *testing.T) {
	s := New("en")
	mustPublish(t, s, "n", "zh", "sms", "{a}{b}", 0)
	mustPublish(t, s, "n", "en", "*", "{c}", 0)
	_, err := s.Render("n", "zh", "sms", 0, nil)
	if err == nil || err.Reason != ReasonMissingVars {
		t.Fatalf("all missing: %v", err)
	}
	if strings.Join(err.Missing, ",") != "a,b" {
		t.Fatalf("first candidate reason: %v", err.Missing)
	}
	// 第一个候选缺变量，第二个成功。
	r, err := s.Render("n", "zh", "sms", 0, map[string]string{"a": "1", "b": "2"})
	if err != nil || r.Text != "12" {
		t.Fatalf("first candidate success: %v %v", r, err)
	}

	// 第一个候选超长，第二个缺变量：全部失败时报第一个候选的超长原因。
	s2 := New("en")
	mustPublish(t, s2, "n", "zh", "sms", strings.Repeat("x", 71), 0)
	mustPublish(t, s2, "n", "en", "*", "{c}", 0)
	_, err = s2.Render("n", "zh", "sms", 0, nil)
	if err == nil || err.Reason != ReasonTooLong || err.Codepoints != 71 {
		t.Fatalf("too long is first candidate reason: %v", err)
	}
	// 第一个候选超长，第二个成功。
	r, err = s2.Render("n", "zh", "sms", 0, map[string]string{"c": "ok"})
	if err != nil || r.Text != "ok" || r.ChannelKey != "*" {
		t.Fatalf("fallback after too long: %v %v", r, err)
	}
}

func TestEmptyStringVsMissingAndDefaults(t *testing.T) {
	s := New("en")
	mustPublish(t, s, "n", "en", "email", "[{a}][{b|def}]", 0)
	r, err := s.Render("n", "en", "email", 0, map[string]string{"a": "", "b": ""})
	if err != nil {
		t.Fatal(err)
	}
	if r.Text != "[][]" {
		t.Fatalf("empty value must count as present: %q", r.Text)
	}
	r, err = s.Render("n", "en", "email", 0, map[string]string{"a": "x"})
	if err != nil {
		t.Fatal(err)
	}
	if r.Text != "[x][def]" {
		t.Fatalf("default text: %q", r.Text)
	}
	_, err = s.Render("n", "en", "email", 0, map[string]string{"b": "x"})
	if err == nil || err.Reason != ReasonMissingVars || strings.Join(err.Missing, ",") != "a" {
		t.Fatalf("required missing: %v", err)
	}
	// 空默认文本。
	mustPublish(t, s, "n2", "en", "sms", "[{x|}]", 0)
	r, err = s.Render("n2", "en", "sms", 0, nil)
	if err != nil || r.Text != "[]" {
		t.Fatalf("empty default: %q %v", r.Text, err)
	}
}

func TestEscapingRules(t *testing.T) {
	s := New("en")
	mustPublish(t, s, "n", "en", "*", `{v}|<&>"|{d|<&>"}`, 0)
	r, err := s.Render("n", "en", "email", 0, map[string]string{"v": `<&>"`})
	if err != nil {
		t.Fatal(err)
	}
	want := `&lt;&amp;&gt;&quot;|<&>"|<&>"`
	if r.Text != want {
		t.Fatalf("email escape: %q want %q", r.Text, want)
	}
	r, err = s.Render("n", "en", "sms", 0, map[string]string{"v": `<&>"`})
	if err != nil || r.Text != `<&>"|<&>"|<&>"` {
		t.Fatalf("sms no escape: %q %v", r.Text, err)
	}
	mustPublish(t, s, "lit", "en", "email", "<a>", 0)
	r, err = s.Render("lit", "en", "email", 0, nil)
	if err != nil || r.Text != "<a>" {
		t.Fatalf("literal not escaped: %q", r.Text)
	}
}

func TestLengthLimit(t *testing.T) {
	s := New("en")
	mustPublish(t, s, "n", "en", "sms", strings.Repeat("a", 70), 0)
	r, err := s.Render("n", "en", "sms", 0, nil)
	if err != nil || len([]rune(r.Text)) != 70 {
		t.Fatalf("70 codepoints ok: %v %v", r, err)
	}
	mustPublish(t, s, "n", "en", "sms", strings.Repeat("a", 71), 1)
	_, err = s.Render("n", "en", "sms", 2, nil)
	if err == nil || err.Reason != ReasonTooLong || err.Codepoints != 71 {
		t.Fatalf("71 too long: %v", err)
	}
	mustPublish(t, s, "n", "en", "*", "short", 0)
	r, err = s.Render("n", "en", "sms", 2, nil)
	if err != nil || r.Text != "short" || r.ChannelKey != "*" {
		t.Fatalf("fallback after too long: %v %v", r, err)
	}
	mustPublish(t, s, "p", "en", "push", strings.Repeat("好", 100), 0)
	if _, err = s.Render("p", "en", "push", 0, nil); err != nil {
		t.Fatalf("push 100 ok: %v", err)
	}
	mustPublish(t, s, "p", "en", "push", strings.Repeat("好", 101), 1)
	_, err = s.Render("p", "en", "push", 2, nil)
	if err == nil || err.Reason != ReasonTooLong || err.Codepoints != 101 {
		t.Fatalf("push 101 too long: %v", err)
	}
	mustPublish(t, s, "e", "en", "*", strings.Repeat("好", 300), 0)
	if _, err = s.Render("e", "en", "email", 0, nil); err != nil {
		t.Fatalf("email unlimited: %v", err)
	}
}

func TestLiteralsAndSyntaxOffsets(t *testing.T) {
	cases := []struct {
		body   string
		render string
	}{
		{"{{}}", "{}"},
		{"{{a}}", "{a}"},
		{"{{ {v} }}", "{ x }"},
	}
	for _, c := range cases {
		pb, perr := parseBody(c.body)
		if perr != nil {
			t.Fatalf("parse %q: %v", c.body, perr)
		}
		got, missing := pb.render(map[string]string{"v": "x"}, "sms")
		if missing != nil || got != c.render {
			t.Fatalf("body %q got %q %v", c.body, got, missing)
		}
	}

	if _, perr := parseBody("{a}}"); perr == nil || perr.Offset != 3 {
		t.Fatalf("{a}} offset: %v", perr)
	}
	bad := []struct {
		body string
		off  int
	}{
		{"}", 0},
		{"ab}", 2},
		{"{a", 0},
		{"{}", 0},
		{"{a|b}", -1},
		{"x {bad name} y", 2},
		{"{{}}}", 4},
		{"{A}", 0},
		{"{a{b}}", 0},
		{"{a}{", 3},
	}
	for _, c := range bad {
		_, perr := parseBody(c.body)
		if c.off < 0 {
			if perr != nil {
				t.Errorf("body %q expected valid, got %v", c.body, perr)
			}
			continue
		}
		if perr == nil || perr.Reason != ReasonSyntax || perr.Offset != c.off {
			t.Errorf("body %q want offset %d, got %v", c.body, c.off, perr)
		}
	}
}

func TestRejectionReasonsAndNoStateChange(t *testing.T) {
	s := New("en")
	mustPublish(t, s, "ok", "en", "sms", "keep", 0)

	check := func(e *Error, want Reason) {
		t.Helper()
		if e == nil || e.Reason != want {
			t.Fatalf("want %v got %v", want, e)
		}
	}

	check(s.Publish("BAD", "en", "sms", "x", 0), ReasonInvalidParam)
	check(s.Publish("n", "en", "fax", "x", 0), ReasonInvalidParam)
	check(s.Publish("n", "en", "sms", "x", -1), ReasonInvalidParam)
	check(s.Publish("n", "en", "sms", "x", maxEff+1), ReasonInvalidParam)
	check(s.Publish("n", "en", "sms", "", 0), ReasonInvalidParam)
	check(s.Publish("n", "en", "sms", strings.Repeat("x", 1001), 0), ReasonInvalidParam)
	check(s.Publish("n", "en", "sms", "x\xffy", 0), ReasonInvalidParam)
	check(s.Publish("n", "engg", "sms", "x", 0), ReasonInvalidLanguage)
	check(s.Publish("n", "engg", "sms", "}", 0), ReasonInvalidLanguage) // 语言先于语法
	check(s.Publish("n", "en", "sms", "{a}}", 0), ReasonSyntax)

	check(s.Retire("BAD", "en", "sms", 0), ReasonInvalidParam)
	check(s.Retire("n", "en", "fax", 0), ReasonInvalidParam)
	check(s.Retire("n", "en", "sms", maxEff+1), ReasonInvalidParam)
	check(s.Retire("n", "engg", "sms", 0), ReasonInvalidLanguage)

	checkErr := func(e *Error, want Reason) {
		t.Helper()
		if e == nil || e.Reason != want {
			t.Fatalf("render want %v got %v", want, e)
		}
	}
	_, e := s.Render("BAD", "en", "sms", 0, nil)
	checkErr(e, ReasonInvalidParam)
	_, e = s.Render("ok", "en", "fax", 0, nil)
	checkErr(e, ReasonInvalidParam)
	_, e = s.Render("ok", "en", "sms", -1, nil)
	checkErr(e, ReasonInvalidParam)
	_, e = s.Render("ok", "en", "sms", 0, map[string]string{"BAD": "x"})
	checkErr(e, ReasonInvalidParam)
	_, e = s.Render("ok", "engg", "sms", 0, nil)
	checkErr(e, ReasonInvalidLanguage)

	// 拒绝不得改状态：ok 模板仍可渲染。
	r, err := s.Render("ok", "en", "sms", 0, nil)
	if err != nil || r.Text != "keep" {
		t.Fatalf("state changed by rejected calls: %q %v", r.Text, err)
	}
	// n 从未成功登记。
	_, err = s.Render("n", "en", "sms", 0, nil)
	checkErr(err, ReasonNoTemplate)

	func() {
		defer func() {
			if recover() == nil {
				t.Fatalf("New with invalid default language must panic")
			}
		}()
		New("badd")
	}()
}

func TestProbeAndLookupBounds(t *testing.T) {
	s := New("en")
	// 同一键登记 0..9 共 10 个版本，二分探测次数不超过 ceil(log2(11))=4。
	const n = 10
	for i := 0; i < n; i++ {
		mustPublish(t, s, "k", "en", "sms", "x", int64(i))
	}
	if _, err := s.Render("k", "en", "sms", 5, nil); err != nil {
		t.Fatal(err)
	}
	snap := s.probeSnapshot()
	rec := snap[key{"k", "en", "sms"}]
	if rec.n != n {
		t.Fatalf("recorded n=%d", rec.n)
	}
	if rec.probe > ceilLog2Plus1(n) {
		t.Fatalf("probe %d > ceil(log2(%d+1))=%d", rec.probe, n, ceilLog2Plus1(n))
	}

	// Render 的键查找数不超过回退链长度的 2 倍。
	mustPublish(t, s, "z", "zh-Hant-TW", "*", "hit{v}", 0)
	if _, err := s.Render("z", "zh-Hant-TW", "sms", 0, map[string]string{"v": "!"}); err != nil {
		t.Fatal(err)
	}
	chainLen := len(fallbackChain("zh-Hant-TW", "en"))
	if got := int(s.renderKeyLookups.Load()); got > 2*chainLen {
		t.Fatalf("key lookups %d > 2*%d", got, chainLen)
	}
	// 全失败时应恰好探测 2*链长 个键。
	if got := int(s.renderKeyLookups.Load()); got != 2 {
		t.Fatalf("early-hit lookups=%d want 2", got)
	}
	_, _ = s.Render("missing", "zh-Hant-TW", "sms", 0, nil)
	if got := int(s.renderKeyLookups.Load()); got != 2*chainLen {
		t.Fatalf("full-scan lookups=%d want %d", got, 2*chainLen)
	}

	// 更大版本数的探测界。
	s2 := New("en")
	for i := 0; i < 100; i++ {
		mustPublish(t, s2, "k", "en", "sms", "x", int64(i))
	}
	if _, err := s2.Render("k", "en", "sms", 50, nil); err != nil {
		t.Fatal(err)
	}
	rec = s2.probeSnapshot()[key{"k", "en", "sms"}]
	if rec.probe > ceilLog2Plus1(100) {
		t.Fatalf("probe %d > %d", rec.probe, ceilLog2Plus1(100))
	}
}

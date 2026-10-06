package exports

import (
	"reflect"
	"testing"
)

func resolve(t *testing.T, tbl *Table, subpath string, conds ...string) Result {
	t.Helper()
	res, err := tbl.Resolve(subpath, conds)
	if err != nil {
		t.Fatalf("Resolve(%q, %v): %v", subpath, conds, err)
	}
	t.Logf("subpath=%q conds=%v => target=%q key=%q chain=%v",
		subpath, conds, res.Target, res.Key, res.Conditions)
	return res
}

func resolveErr(t *testing.T, tbl *Table, subpath string, conds ...string) error {
	t.Helper()
	_, err := tbl.Resolve(subpath, conds)
	if err == nil {
		t.Fatalf("Resolve(%q, %v): want error, got success", subpath, conds)
	}
	t.Logf("subpath=%q conds=%v => err=%v", subpath, conds, err)
	return err
}

func TestExactBeatsWildcard(t *testing.T) {
	tbl := mustTable(t,
		Entry{Key: "./a/*", Target: StringTarget("./wild/*")},
		Entry{Key: "./a/b", Target: StringTarget("./exact-b.js")},
	)
	if res := resolve(t, tbl, "./a/b"); res.Key != "./a/b" || res.Target != "./exact-b.js" {
		t.Fatalf("exact key must win: %+v", res)
	}
	if res := resolve(t, tbl, "./a/c"); res.Key != "./a/*" || res.Target != "./wild/c" {
		t.Fatalf("wildcard fallback: %+v", res)
	}
}

func TestWildcardLongestPrefixWins(t *testing.T) {
	tbl := mustTable(t,
		Entry{Key: "./a*", Target: StringTarget("./short/*")},
		Entry{Key: "./ab*", Target: StringTarget("./long/*")},
	)
	// "./ab*" 前缀更长，即使整键更短也应胜出。
	if res := resolve(t, tbl, "./abc"); res.Key != "./ab*" || res.Target != "./long/c" {
		t.Fatalf("longest prefix must win: %+v", res)
	}
}

func TestWildcardTieBreakByKeyLength(t *testing.T) {
	tbl := mustTable(t,
		Entry{Key: "./a*b", Target: StringTarget("./k1/*")},
		Entry{Key: "./a*bc", Target: StringTarget("./k2/*")},
	)
	// 两个键前缀同为 "./a"，都能匹配 "./axybc"：整键更长者胜。
	if res := resolve(t, tbl, "./axybc"); res.Key != "./a*bc" || res.Target != "./k2/xy" {
		t.Fatalf("longer key must win on prefix tie: %+v", res)
	}
}

func TestWildcardMatchedSegmentWithSlash(t *testing.T) {
	tbl := mustTable(t,
		Entry{Key: "./*.js", Target: StringTarget("./dist/*.js")},
	)
	// 匹配段可含斜杠。
	if res := resolve(t, tbl, "./a/b/c.js"); res.Target != "./dist/a/b/c.js" {
		t.Fatalf("matched segment may contain slashes: %+v", res)
	}
}

func TestWildcardEmptyMatchFails(t *testing.T) {
	tbl := mustTable(t,
		Entry{Key: "./a*", Target: StringTarget("./x/*")},
	)
	// 星号至少匹配一个字符："./a" 不应命中 "./a*"。
	if err := resolveErr(t, tbl, "./a"); !IsKind(err, KindNotExported) {
		t.Fatalf("want KindNotExported, got %v", err)
	}
	tbl2 := mustTable(t,
		Entry{Key: "./*", Target: StringTarget("./x/*")},
	)
	if err := resolveErr(t, tbl2, "./"); !IsKind(err, KindNotExported) {
		t.Fatalf("want KindNotExported, got %v", err)
	}
}

func TestNotExported(t *testing.T) {
	tbl := mustTable(t, Entry{Key: "./a", Target: StringTarget("./a.js")})
	if err := resolveErr(t, tbl, "./b"); !IsKind(err, KindNotExported) {
		t.Fatalf("want KindNotExported, got %v", err)
	}
	if err := resolveErr(t, tbl, "."); !IsKind(err, KindNotExported) {
		t.Fatalf("want KindNotExported, got %v", err)
	}
}

func TestNestedConditionsAndDefault(t *testing.T) {
	tbl := mustTable(t,
		Entry{Key: "./x", Target: ConditionsTarget(
			Cond("browser", ConditionsTarget(
				Cond("mobile", StringTarget("./m.js")),
				Cond("default", StringTarget("./b.js")),
			)),
			Cond("default", StringTarget("./d.js")),
		)},
	)
	cases := []struct {
		conds  []string
		target string
		chain  []string
	}{
		{[]string{"browser", "mobile"}, "./m.js", []string{"browser", "mobile"}},
		{[]string{"browser"}, "./b.js", []string{"browser", "default"}},
		{nil, "./d.js", []string{"default"}},
		{[]string{"other"}, "./d.js", []string{"default"}},
	}
	for _, tc := range cases {
		res := resolve(t, tbl, "./x", tc.conds...)
		if res.Target != tc.target || !reflect.DeepEqual(res.Conditions, tc.chain) {
			t.Fatalf("conds=%v: want %q %v, got %+v", tc.conds, tc.target, tc.chain, res)
		}
	}
}

func TestConditionNamesCaseSensitive(t *testing.T) {
	tbl := mustTable(t,
		Entry{Key: "./x", Target: ConditionsTarget(
			Cond("Browser", StringTarget("./b.js")),
			Cond("default", StringTarget("./d.js")),
		)},
	)
	if res := resolve(t, tbl, "./x", "browser"); res.Target != "./d.js" {
		t.Fatalf("condition names are case-sensitive: %+v", res)
	}
	if res := resolve(t, tbl, "./x", "Browser"); res.Target != "./b.js" {
		t.Fatalf("exact case must match: %+v", res)
	}
}

func TestForbiddenVsNoMatch(t *testing.T) {
	tbl := mustTable(t,
		Entry{Key: "./direct", Target: ForbiddenTarget()},
		Entry{Key: "./via-default", Target: ConditionsTarget(
			Cond("never", StringTarget("./x.js")),
			Cond("default", ForbiddenTarget()),
		)},
		Entry{Key: "./no-match", Target: ConditionsTarget(
			Cond("never", StringTarget("./x.js")),
		)},
	)
	if err := resolveErr(t, tbl, "./direct"); !IsKind(err, KindForbidden) {
		t.Fatalf("want KindForbidden, got %v", err)
	}
	if err := resolveErr(t, tbl, "./via-default"); !IsKind(err, KindForbidden) {
		t.Fatalf("want KindForbidden, got %v", err)
	}
	if err := resolveErr(t, tbl, "./no-match"); !IsKind(err, KindNoMatchingCondition) {
		t.Fatalf("want KindNoMatchingCondition, got %v", err)
	}
}

func TestForbiddenStopsFallback(t *testing.T) {
	tbl := mustTable(t,
		Entry{Key: "./x", Target: ConditionsTarget(
			Cond("feat", ForbiddenTarget()),
			Cond("default", StringTarget("./ok.js")),
		)},
	)
	// 命中的条件下是显式禁止：直接报被禁止，不再试 default。
	if err := resolveErr(t, tbl, "./x", "feat"); !IsKind(err, KindForbidden) {
		t.Fatalf("want KindForbidden, got %v", err)
	}
}

func TestInnerNoMatchFallsThrough(t *testing.T) {
	tbl := mustTable(t,
		Entry{Key: "./x", Target: ConditionsTarget(
			Cond("feat", ConditionsTarget(
				Cond("nope", StringTarget("./inner.js")),
			)),
			Cond("default", StringTarget("./ok.js")),
		)},
	)
	// feat 命中但内部没有任何条件命中：继续试后面的条件。
	res := resolve(t, tbl, "./x", "feat")
	if res.Target != "./ok.js" || !reflect.DeepEqual(res.Conditions, []string{"default"}) {
		t.Fatalf("inner no-match must fall through: %+v", res)
	}
}

func TestInvalidTarget(t *testing.T) {
	tbl := mustTable(t,
		Entry{Key: "./no-dot-slash", Target: StringTarget("x.js")},
		Entry{Key: "./dot-seg", Target: StringTarget("./a/./b.js")},
		Entry{Key: "./dotdot-seg", Target: StringTarget("./a/../b.js")},
		Entry{Key: "./nm-seg", Target: StringTarget("./a/node_modules/b.js")},
		Entry{Key: "./nm-case", Target: StringTarget("./a/NODE_MODULES/b.js")},
		Entry{Key: "./fallback", Target: ConditionsTarget(
			Cond("feat", StringTarget("../bad.js")),
			Cond("default", StringTarget("./ok.js")),
		)},
	)
	for _, key := range []string{"./no-dot-slash", "./dot-seg", "./dotdot-seg", "./nm-seg", "./nm-case"} {
		if err := resolveErr(t, tbl, key); !IsKind(err, KindInvalidTarget) {
			t.Fatalf("%s: want KindInvalidTarget, got %v", key, err)
		}
	}
	// 非法目标同样不回退到后面的条件。
	if err := resolveErr(t, tbl, "./fallback", "feat"); !IsKind(err, KindInvalidTarget) {
		t.Fatalf("want KindInvalidTarget, got %v", err)
	}
}

func TestMatchedSegmentCarriesIllegalSegment(t *testing.T) {
	tbl := mustTable(t,
		Entry{Key: "./features/*", Target: StringTarget("./src/*")},
	)
	// 匹配段把非法段带进目标：结果非法。
	if err := resolveErr(t, tbl, "./features/node_modules/x"); !IsKind(err, KindInvalidTarget) {
		t.Fatalf("want KindInvalidTarget, got %v", err)
	}
	if err := resolveErr(t, tbl, "./features/a/../../b"); !IsKind(err, KindInvalidTarget) {
		t.Fatalf("want KindInvalidTarget, got %v", err)
	}
	if res := resolve(t, tbl, "./features/ok/x"); res.Target != "./src/ok/x" {
		t.Fatalf("legal matched segment: %+v", res)
	}
}

func TestReplaceAtomicityAndRejection(t *testing.T) {
	r := NewResolver(mustTable(t,
		Entry{Key: "./*", Target: StringTarget("./old/ok.js")},
	))
	bad := []Entry{{Key: "no-dot", Target: StringTarget("./x.js")}}
	if err := r.Replace(bad); !IsKind(err, KindInvalidTable) {
		t.Fatalf("want KindInvalidTable, got %v", err)
	}
	// 替换被拒绝，旧表继续生效。
	res, err := r.Resolve("./anything", nil)
	if err != nil || res.Target != "./old/ok.js" {
		t.Fatalf("old table must stay in effect: %+v, %v", res, err)
	}

	// 合法替换：新表立即生效，星号匹配段落入非法段。
	if err := r.Replace([]Entry{
		{Key: "./*", Target: StringTarget("./new/*")},
	}); err != nil {
		t.Fatalf("Replace: %v", err)
	}
	res, err = r.Resolve("./a/b", nil)
	if err != nil || res.Target != "./new/a/b" {
		t.Fatalf("new table must be in effect: %+v, %v", res, err)
	}
	if _, err = r.Resolve("./a/node_modules/b", nil); !IsKind(err, KindInvalidTarget) {
		t.Fatalf("want KindInvalidTarget after replace, got %v", err)
	}
}

func TestRejectedRequestKeepsState(t *testing.T) {
	r := NewResolver(mustTable(t,
		Entry{Key: "./a", Target: StringTarget("./a.js")},
	))
	if _, err := r.Resolve("bad", nil); !IsKind(err, KindInvalidRequest) {
		t.Fatalf("want KindInvalidRequest, got %v", err)
	}
	res, err := r.Resolve("./a", nil)
	if err != nil || res.Target != "./a.js" {
		t.Fatalf("resolver state must be unchanged: %+v, %v", res, err)
	}
}

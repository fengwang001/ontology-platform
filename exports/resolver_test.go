package exports

import (
	"reflect"
	"testing"
)

func mustResolver(t *testing.T, entries []Entry) *Resolver {
	t.Helper()
	r, err := NewResolver(entries)
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}
	return r
}

func mustResolve(t *testing.T, r *Resolver, subpath string, conds ...string) Resolution {
	t.Helper()
	res, err := r.Resolve(subpath, conds)
	if err != nil {
		t.Fatalf("Resolve(%q, %v): %v", subpath, conds, err)
	}
	return res
}

func mustErrKind(t *testing.T, r *Resolver, subpath string, want Kind, conds ...string) {
	t.Helper()
	_, err := r.Resolve(subpath, conds)
	if err == nil {
		t.Fatalf("Resolve(%q, %v): 期望错误 %v，实际成功", subpath, conds, want)
	}
	got, ok := KindOf(err)
	if !ok || got != want {
		t.Fatalf("Resolve(%q, %v): 期望错误类别 %v，实际 %v", subpath, conds, want, err)
	}
}

func TestExactKeyBeatsWildcard(t *testing.T) {
	r := mustResolver(t, []Entry{
		{Key: "./*", Target: StringTarget("./wild/*")},
		{Key: "./exact", Target: StringTarget("./exact.js")},
	})
	res := mustResolve(t, r, "./exact")
	if res.Key != "./exact" || res.Target != "./exact.js" {
		t.Fatalf("精确键应优先于通配键: %+v", res)
	}
	res = mustResolve(t, r, "./other")
	if res.Key != "./*" || res.Target != "./wild/other" {
		t.Fatalf("通配键回退失败: %+v", res)
	}
}

func TestWildcardLongestPrefixWins(t *testing.T) {
	// 前缀长度优先于整个键的长度："./ab*" 前缀更长但键更短。
	r := mustResolver(t, []Entry{
		{Key: "./ab*", Target: StringTarget("./short/*")},
		{Key: "./a*xyz", Target: StringTarget("./long/*")},
	})
	res := mustResolve(t, r, "./abxyz")
	if res.Key != "./ab*" || res.Matched != "xyz" {
		t.Fatalf("星号前部分最长者应胜出: %+v", res)
	}
}

func TestWildcardTieBreakByKeyLength(t *testing.T) {
	// 前缀等长时取整个键更长（即星号后缀更长）者。
	r := mustResolver(t, []Entry{
		{Key: "./a*", Target: StringTarget("./short/*")},
		{Key: "./a*.js", Target: StringTarget("./long/*")},
	})
	res := mustResolve(t, r, "./ax.js")
	if res.Key != "./a*.js" || res.Matched != "x" || res.Target != "./long/x" {
		t.Fatalf("前缀等长时应按键长裁决: %+v", res)
	}
	res = mustResolve(t, r, "./ax")
	if res.Key != "./a*" {
		t.Fatalf("后缀不匹配时应落到较短键: %+v", res)
	}
}

func TestMatchedSegmentMayContainSlash(t *testing.T) {
	r := mustResolver(t, []Entry{
		{Key: "./*", Target: StringTarget("./dist/*.js")},
	})
	res := mustResolve(t, r, "./a/b/c")
	if res.Matched != "a/b/c" || res.Target != "./dist/a/b/c.js" {
		t.Fatalf("匹配段应可含斜杠: %+v", res)
	}
}

func TestEmptyMatchSegmentRejected(t *testing.T) {
	r := mustResolver(t, []Entry{
		{Key: "./*.js", Target: StringTarget("./dist/*")},
	})
	mustErrKind(t, r, "./.js", KindSubpathNotExported)
	res := mustResolve(t, r, "./x.js")
	if res.Matched != "x" {
		t.Fatalf("非空匹配段应命中: %+v", res)
	}
}

func TestSubpathNotExported(t *testing.T) {
	r := mustResolver(t, []Entry{
		{Key: ".", Target: StringTarget("./index.js")},
		{Key: "./a/*", Target: StringTarget("./a/*")},
	})
	mustErrKind(t, r, "./b", KindSubpathNotExported)
	res := mustResolve(t, r, ".")
	if res.Key != "." || res.Target != "./index.js" {
		t.Fatalf("根子路径解析失败: %+v", res)
	}
}

func TestNestedConditionsAndDefault(t *testing.T) {
	r := mustResolver(t, []Entry{
		{Key: ".", Target: ConditionsTarget(
			Cond("node", ConditionsTarget(
				Cond("import", StringTarget("./esm.js")),
				Cond("default", StringTarget("./cjs.js")),
			)),
			Cond("browser", StringTarget("./browser.js")),
			Cond("default", StringTarget("./fallback.js")),
		)},
	})
	res := mustResolve(t, r, ".", "node", "import")
	if res.Target != "./esm.js" || !reflect.DeepEqual(res.Conditions, []string{"node", "import"}) {
		t.Fatalf("嵌套条件命中失败: %+v", res)
	}
	res = mustResolve(t, r, ".", "node")
	if res.Target != "./cjs.js" || !reflect.DeepEqual(res.Conditions, []string{"node", "default"}) {
		t.Fatalf("内层 default 应命中: %+v", res)
	}
	res = mustResolve(t, r, ".", "browser")
	if res.Target != "./browser.js" {
		t.Fatalf("browser 条件应命中: %+v", res)
	}
	res = mustResolve(t, r, ".")
	if res.Target != "./fallback.js" || !reflect.DeepEqual(res.Conditions, []string{"default"}) {
		t.Fatalf("default 总是命中: %+v", res)
	}
}

func TestNoMatchingConditionFallsThrough(t *testing.T) {
	// 命中条件的内部无匹配时，继续试后面的条件。
	r := mustResolver(t, []Entry{
		{Key: ".", Target: ConditionsTarget(
			Cond("a", ConditionsTarget(
				Cond("b", StringTarget("./ab.js")),
			)),
			Cond("default", StringTarget("./fallback.js")),
		)},
	})
	res := mustResolve(t, r, ".", "a")
	if res.Target != "./fallback.js" {
		t.Fatalf("内部无匹配应继续试后面的条件: %+v", res)
	}
	res = mustResolve(t, r, ".", "a", "b")
	if res.Target != "./ab.js" {
		t.Fatalf("嵌套条件应命中: %+v", res)
	}
}

func TestNoMatchingConditionTopLevel(t *testing.T) {
	r := mustResolver(t, []Entry{
		{Key: ".", Target: ConditionsTarget(
			Cond("x", StringTarget("./x.js")),
		)},
	})
	mustErrKind(t, r, ".", KindNoMatchingCondition, "y")
	mustErrKind(t, r, ".", KindNoMatchingCondition)
}

func TestForbiddenStopsImmediately(t *testing.T) {
	// 命中条件被禁止时直接报被禁止，不再试后面的条件。
	r := mustResolver(t, []Entry{
		{Key: ".", Target: ConditionsTarget(
			Cond("blocked", ForbiddenTarget()),
			Cond("default", StringTarget("./fallback.js")),
		)},
		{Key: "./null", Target: ForbiddenTarget()},
	})
	mustErrKind(t, r, ".", KindForbidden, "blocked")
	mustErrKind(t, r, "./null", KindForbidden)
	res := mustResolve(t, r, ".")
	if res.Target != "./fallback.js" {
		t.Fatalf("未命中禁止条件时应正常解析: %+v", res)
	}
}

func TestForbiddenNestedStopsImmediately(t *testing.T) {
	// 嵌套内部的禁止同样直接传播，不回退到外层后续条件。
	r := mustResolver(t, []Entry{
		{Key: ".", Target: ConditionsTarget(
			Cond("a", ConditionsTarget(
				Cond("b", ForbiddenTarget()),
			)),
			Cond("default", StringTarget("./fallback.js")),
		)},
	})
	mustErrKind(t, r, ".", KindForbidden, "a", "b")
}

func TestInvalidTarget(t *testing.T) {
	r := mustResolver(t, []Entry{
		{Key: "./bad1", Target: StringTarget("not-relative")},
		{Key: "./bad2", Target: StringTarget("./a/./b")},
		{Key: "./bad3", Target: StringTarget("./a/../b")},
		{Key: "./bad4", Target: StringTarget("./a/NODE_MODULES/b")},
		{Key: "./good", Target: StringTarget("./a/b.js")},
	})
	mustErrKind(t, r, "./bad1", KindInvalidTarget)
	mustErrKind(t, r, "./bad2", KindInvalidTarget)
	mustErrKind(t, r, "./bad3", KindInvalidTarget)
	mustErrKind(t, r, "./bad4", KindInvalidTarget)
	res := mustResolve(t, r, "./good")
	if res.Target != "./a/b.js" {
		t.Fatalf("合法目标解析失败: %+v", res)
	}
}

func TestMatchedSegmentCarryingIllegalSegment(t *testing.T) {
	r := mustResolver(t, []Entry{
		{Key: "./*", Target: StringTarget("./src/*")},
	})
	mustErrKind(t, r, "./../evil", KindInvalidTarget)
	mustErrKind(t, r, "./node_modules/x", KindInvalidTarget)
	mustErrKind(t, r, "./a/./b", KindInvalidTarget)
	res := mustResolve(t, r, "./a/b")
	if res.Target != "./src/a/b" {
		t.Fatalf("合法匹配段解析失败: %+v", res)
	}
}

func TestInvalidRequest(t *testing.T) {
	r := mustResolver(t, []Entry{
		{Key: "./*", Target: ForbiddenTarget()},
	})
	// 请求非法优先于子路径未导出与被禁止。
	mustErrKind(t, r, "", KindInvalidRequest)
	mustErrKind(t, r, "x", KindInvalidRequest)
	mustErrKind(t, r, ".x", KindInvalidRequest)
	mustErrKind(t, r, "./a*", KindInvalidRequest)
	mustErrKind(t, r, ".", KindInvalidRequest, "")
	// 条件名区分大小写。
	mustErrKind(t, r, "./a", KindForbidden, "Default")
}

func TestInvalidTable(t *testing.T) {
	cases := []struct {
		name    string
		entries []Entry
	}{
		{"键不以点开头", []Entry{{Key: "x", Target: StringTarget("./a")}}},
		{"键形如点x", []Entry{{Key: ".x", Target: StringTarget("./a")}}},
		{"键含两个星号", []Entry{{Key: "./a/*/*", Target: StringTarget("./a")}}},
		{"重复键", []Entry{
			{Key: "./a", Target: StringTarget("./a")},
			{Key: "./a", Target: StringTarget("./b")},
		}},
		{"重复通配键", []Entry{
			{Key: "./a/*", Target: StringTarget("./a")},
			{Key: "./a/*", Target: StringTarget("./b")},
		}},
		{"条件名为空", []Entry{{Key: ".", Target: ConditionsTarget(Cond("", StringTarget("./a")))}}},
		{"条件名全数字", []Entry{{Key: ".", Target: ConditionsTarget(Cond("123", StringTarget("./a")))}}},
		{"default不在最后", []Entry{{Key: ".", Target: ConditionsTarget(
			Cond("default", StringTarget("./a")),
			Cond("x", StringTarget("./b")),
		)}}},
		{"重复条件名", []Entry{{Key: ".", Target: ConditionsTarget(
			Cond("x", StringTarget("./a")),
			Cond("x", StringTarget("./b")),
		)}}},
		{"条件映射为空", []Entry{{Key: ".", Target: ConditionsTarget()}}},
		{"嵌套条件映射为空", []Entry{{Key: ".", Target: ConditionsTarget(
			Cond("x", ConditionsTarget()),
		)}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := NewTable(tc.entries)
			if err == nil {
				t.Fatalf("期望表非法，实际构造成功")
			}
			if got, ok := KindOf(err); !ok || got != KindInvalidTable {
				t.Fatalf("期望 KindInvalidTable，实际 %v", err)
			}
		})
	}
}

func TestReplaceRejectedKeepsOldTable(t *testing.T) {
	r := mustResolver(t, []Entry{
		{Key: ".", Target: StringTarget("./old.js")},
	})
	if err := r.Replace([]Entry{{Key: "bad-key", Target: StringTarget("./x")}}); err == nil {
		t.Fatal("非法表应被拒绝")
	}
	res := mustResolve(t, r, ".")
	if res.Target != "./old.js" {
		t.Fatalf("替换被拒绝后旧表应继续生效: %+v", res)
	}
	if err := r.Swap(nil); err == nil {
		t.Fatal("nil 表应被拒绝")
	}
	if err := r.Replace([]Entry{{Key: ".", Target: StringTarget("./new.js")}}); err != nil {
		t.Fatalf("合法表应替换成功: %v", err)
	}
	res = mustResolve(t, r, ".")
	if res.Target != "./new.js" {
		t.Fatalf("替换后新表应生效: %+v", res)
	}
}

func TestStarInTargetWithoutStarInKey(t *testing.T) {
	// 键不含星号时目标中的星号不做替换，原样保留。
	r := mustResolver(t, []Entry{
		{Key: "./a", Target: StringTarget("./x/*")},
	})
	res := mustResolve(t, r, "./a")
	if res.Target != "./x/*" {
		t.Fatalf("键无星号时目标中的星号不应替换: %+v", res)
	}
}

func TestLookupComplexityIndependentOfTableSize(t *testing.T) {
	build := func(n int) *Resolver {
		entries := make([]Entry, 0, n+1)
		entries = append(entries, Entry{Key: "./target/*", Target: StringTarget("./out/*")})
		for i := 0; i < n; i++ {
			entries = append(entries, Entry{
				Key:    "./noise/" + itoa(i) + "/*",
				Target: StringTarget("./noise/*"),
			})
		}
		return mustResolver(t, entries)
	}
	small := build(10)
	large := build(20000)

	probesFor := func(r *Resolver) int64 {
		if _, err := r.Resolve("./target/x/y", nil); err != nil {
			t.Fatalf("Resolve: %v", err)
		}
		return r.table.Load().lookupProbes()
	}
	smallProbes := probesFor(small)
	largeProbes := probesFor(large)
	if smallProbes != largeProbes {
		t.Fatalf("键查找次数不应随表大小增长: 小表 %d 次, 大表 %d 次", smallProbes, largeProbes)
	}
	t.Logf("键查找次数: 小表 %d 次, 大表 %d 次（相等即与表大小无关）", smallProbes, largeProbes)
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var buf [8]byte
	pos := len(buf)
	for i > 0 {
		pos--
		buf[pos] = byte('0' + i%10)
		i /= 10
	}
	return string(buf[pos:])
}

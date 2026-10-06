package ontology

import (
	"errors"
	"testing"
)

var patternTree = []string{
	"a/b/f1",
	"a/b/f2",
	"a/b/c/f3",
	"a/b/c/f4",
	"a/d/f5",
	"x/y/f6",
	"x/f7",
}

// 三类模式各自的命中范围与互不越界。
func TestPatternScopes(t *testing.T) {
	cases := []struct {
		name    string
		rules   []Rule
		wantMat []string
	}{
		{
			name:    "exact-only",
			rules:   []Rule{{Include, "a/b/f1"}},
			wantMat: []string{"a", "a/b", "a/b/f1"},
		},
		{
			name:  "prefix-all-descendants",
			rules: []Rule{{Include, "a/b/"}},
			wantMat: []string{
				"a", "a/b", "a/b/c", "a/b/c/f3", "a/b/c/f4",
				"a/b/f1", "a/b/f2",
			},
		},
		{
			name:    "children-direct-only",
			rules:   []Rule{{Include, "a/b/*"}},
			wantMat: []string{"a", "a/b", "a/b/f1", "a/b/f2"},
		},
		{
			name:    "children-on-root",
			rules:   []Rule{{Include, "*"}},
			wantMat: []string{"rootfile"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tree := patternTree
			if tc.name == "children-on-root" {
				tree = append(append([]string{}, patternTree...), "rootfile")
			}
			e := newTestEngine(t, map[string][]string{"c1": tree})
			v, diff, _, err := e.ReplaceRules(tc.rules, "c1", false)
			if err != nil {
				t.Fatalf("ReplaceRules: %v", err)
			}
			got := e.ListMaterialized("")
			tlog(t, "rules=%v version=%d added=%v removed=%v materialized=%v want=%v",
				tc.rules, v, diff.Added, diff.Removed, got, tc.wantMat)
			if !eqStrings(got, tc.wantMat) {
				t.Fatalf("materialized=%v want=%v", got, tc.wantMat)
			}
		})
	}
}

// 后规则覆盖前规则；目录排除后再包含后代。
func TestOverrideAndReinclude(t *testing.T) {
	e := newTestEngine(t, map[string][]string{"c1": patternTree})
	rules := []Rule{
		{Include, "a/b/"},
		{Exclude, "a/b/*"},
		{Include, "a/b/f2"},
		{Exclude, "a/b/c/"},
		{Include, "a/b/c/f4"},
	}
	_, diff, _, err := e.ReplaceRules(rules, "c1", false)
	if err != nil {
		t.Fatalf("ReplaceRules: %v", err)
	}
	want := []string{"a", "a/b", "a/b/c", "a/b/c/f4", "a/b/f2"}
	got := e.ListMaterialized("")
	tlog(t, "rules=%v added=%v materialized=%v want=%v", rules, diff.Added, got, want)
	if !eqStrings(got, want) {
		t.Fatalf("materialized=%v want=%v", got, want)
	}
	for p, st := range map[string]PathStatus{
		"a/b/f1":   StatusExcludedByRule,
		"a/b/c/f3": StatusExcludedByRule,
		"x/y/f6":   StatusNoRuleMatch,
		"x":        StatusNoRuleMatch,
	} {
		got := e.QueryPath(p)
		tlog(t, "QueryPath(%q)=%s want=%s", p, statusName(got), statusName(st))
		if got != st {
			t.Fatalf("QueryPath(%q)=%s want=%s", p, statusName(got), statusName(st))
		}
	}
}

// 空目录不物化，即使有包含规则命中它。
func TestEmptyDirectoryNotMaterialized(t *testing.T) {
	e := newTestEngine(t, map[string][]string{"c1": patternTree})
	rules := []Rule{
		{Include, "a/d/"},
		{Exclude, "a/d/f5"},
	}
	if _, _, _, err := e.ReplaceRules(rules, "c1", false); err != nil {
		t.Fatal(err)
	}
	got := e.QueryPath("a/d")
	tlog(t, "QueryPath(a/d)=%s want=EMPTY_DIRECTORY", statusName(got))
	if got != StatusEmptyDirectory {
		t.Fatalf("a/d = %s want EMPTY_DIRECTORY", statusName(got))
	}
	e2 := newTestEngine(t, map[string][]string{"c1": patternTree})
	if _, _, _, err := e2.ReplaceRules(nil, "c1", false); err != nil {
		t.Fatal(err)
	}
	if got := e2.QueryPath("a/d"); got != StatusNoRuleMatch {
		t.Fatalf("empty rules a/d = %s want NO_RULE_MATCH", statusName(got))
	}
	tlog(t, "empty rules => a/d=NO_RULE_MATCH, materialized=%v", e2.ListMaterialized(""))
}

// 无意义重复规则被拒；参数非法模式被拒。
func TestMeaninglessDuplicatesRejected(t *testing.T) {
	bad := [][]Rule{
		{{Include, "a/"}, {Exclude, "b"}, {Include, "a/"}},
		{{Exclude, "a/*"}, {Exclude, "a/*"}},
		{{Include, "a/b"}, {Include, "a/b"}},
	}
	for i, rs := range bad {
		_, err := validateAndCompile(rs)
		tlog(t, "bad[%d]=%v err=%v", i, rs, err)
		if err == nil {
			t.Fatalf("bad[%d] should be rejected", i)
		}
	}
	good := []Rule{{Include, "a/"}, {Exclude, "a/"}, {Include, "a/"}}
	if _, err := validateAndCompile(good); err != nil {
		t.Fatalf("toggle rules should be legal: %v", err)
	}
	tlog(t, "toggle rules accepted: %v", good)

	invalid := []string{"", "a//b", "../x", "a/../b", "a/b*/c", "/a/b"}
	for _, p := range invalid {
		if _, err := validateAndCompile([]Rule{{Include, p}}); err == nil {
			t.Fatalf("pattern %q should be rejected", p)
		}
		tlog(t, "invalid pattern %q rejected", p)
	}
}

func sortedCopy(s []string) []string {
	out := append([]string{}, s...)
	sortStringsCopy(out)
	return out
}

func sortStringsCopy(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j-1] > s[j]; j-- {
			s[j-1], s[j] = s[j], s[j-1]
		}
	}
}

// 变更差集：新增、撤销、作用域内保持。
func TestDiffAddedRemovedKept(t *testing.T) {
	files := []string{"a/f1", "a/f2", "b/f3", "b/f4"}
	e := newTestEngine(t, map[string][]string{"c1": files})
	_, d, _, err := e.ReplaceRules([]Rule{{Include, "a/"}, {Include, "b/f3"}}, "c1", false)
	if err != nil {
		t.Fatal(err)
	}
	tlog(t, "initial added=%v", d.Added)
	if !eqStrings(d.Added, []string{"a/f1", "a/f2", "b/f3"}) {
		t.Fatalf("initial added=%v", d.Added)
	}
	_, d2, _, err := e.ReplaceRules([]Rule{{Include, "b/"}}, "", false)
	if err != nil {
		t.Fatal(err)
	}
	tlog(t, "second added=%v removed=%v kept=%v", d2.Added, d2.Removed, d2.Kept)
	if !eqStrings(d2.Added, []string{"b/f4"}) {
		t.Fatalf("added=%v want [b/f4]", d2.Added)
	}
	if !eqStrings(sortedCopy(d2.Removed), []string{"a/f1", "a/f2"}) {
		t.Fatalf("removed=%v", d2.Removed)
	}
	if !eqStrings(d2.Kept, []string{"b/f3"}) {
		t.Fatalf("kept=%v want [b/f3]", d2.Kept)
	}
}

// 受阻列表完整性 + 强制丢弃列表；拒绝时状态不变。
func TestBlockedAndForce(t *testing.T) {
	files := []string{"a/f1", "a/f2", "a/f3"}
	e := newTestEngine(t, map[string][]string{"c1": files})
	if _, _, _, err := e.ReplaceRules([]Rule{{Include, "a/"}}, "c1", false); err != nil {
		t.Fatal(err)
	}
	if err := e.MarkDirty("a/f1"); err != nil {
		t.Fatal(err)
	}
	if err := e.MarkDirty("a/f3"); err != nil {
		t.Fatal(err)
	}
	vBefore := e.CurrentVersion()
	_, _, _, err := e.ReplaceRules([]Rule{{Include, "a/f2"}}, "", false)
	var be *BlockedError
	if !errors.As(err, &be) {
		t.Fatalf("want BlockedError, got %v", err)
	}
	tlog(t, "blocked paths=%v", be.Paths)
	if !eqStrings(be.Paths, []string{"a/f1", "a/f3"}) {
		t.Fatalf("blocked=%v", be.Paths)
	}
	if e.CurrentVersion() != vBefore {
		t.Fatalf("version changed after rejection")
	}
	if !e.IsDirty("a/f1") || !e.IsDirty("a/f3") {
		t.Fatalf("dirty flags changed after rejection")
	}
	if got := e.ListMaterialized(""); !eqStrings(got, []string{"a", "a/f1", "a/f2", "a/f3"}) {
		t.Fatalf("materialized changed after rejection: %v", got)
	}
	_, d, discarded, err := e.ReplaceRules([]Rule{{Include, "a/f2"}}, "", true)
	if err != nil {
		t.Fatal(err)
	}
	tlog(t, "force removed=%v discarded=%v", d.Removed, discarded)
	if !eqStrings(discarded, []string{"a/f1", "a/f3"}) {
		t.Fatalf("discarded=%v", discarded)
	}
	if e.IsDirty("a/f1") || e.IsDirty("a/f3") {
		t.Fatalf("dirty flags should be cleared on discard")
	}
	if got := e.ListMaterialized(""); !eqStrings(got, []string{"a", "a/f2"}) {
		t.Fatalf("after force materialized=%v", got)
	}
}

// 切换提交时目标提交缺失的带本地修改路径同样受阻。
func TestCommitSwitchBlocked(t *testing.T) {
	e := newTestEngine(t, map[string][]string{
		"c1": {"a/f1", "shared/f"},
		"c2": {"b/f2", "shared/f"},
	})
	if _, _, _, err := e.ReplaceRules([]Rule{{Include, "a/"}, {Include, "shared/"}}, "c1", false); err != nil {
		t.Fatal(err)
	}
	if err := e.MarkDirty("a/f1"); err != nil {
		t.Fatal(err)
	}
	_, _, err := e.ApplyRuleset("c2", 0, false)
	var be *BlockedError
	if !errors.As(err, &be) || !eqStrings(be.Paths, []string{"a/f1"}) {
		t.Fatalf("want blocked [a/f1], got %v", err)
	}
	tlog(t, "commit switch blocked=%v", be.Paths)
	if e.CurrentCommit() != "c1" {
		t.Fatalf("commit changed after rejection")
	}
}

// 同时换提交与换规则：任一路径受阻则二者都不变；成功则同时生效。
func TestAtomicCombined(t *testing.T) {
	e := newTestEngine(t, map[string][]string{
		"c1": {"a/f1"},
		"c2": {"a/f1", "b/f2"},
	})
	if _, _, _, err := e.ReplaceRules([]Rule{{Include, "a/f1"}}, "c1", false); err != nil {
		t.Fatal(err)
	}
	if err := e.MarkDirty("a/f1"); err != nil {
		t.Fatal(err)
	}
	_, _, _, err := e.ReplaceRules([]Rule{{Include, "b/"}}, "c2", false)
	var be *BlockedError
	if !errors.As(err, &be) {
		t.Fatalf("want block, got %v", err)
	}
	tlog(t, "combined rejected err=%v", err)
	if e.CurrentCommit() != "c1" || e.CurrentVersion() != 1 {
		t.Fatalf("state changed on rejection")
	}
	v, d, discarded, err := e.ReplaceRules([]Rule{{Include, "b/"}}, "c2", true)
	if err != nil {
		t.Fatal(err)
	}
	tlog(t, "combined force v=%d added=%v removed=%v discarded=%v", v, d.Added, d.Removed, discarded)
	if e.CurrentCommit() != "c2" || e.CurrentVersion() != 2 {
		t.Fatalf("state not applied")
	}
	if !eqStrings(e.ListMaterialized(""), []string{"b", "b/f2"}) {
		t.Fatalf("materialized=%v", e.ListMaterialized(""))
	}
}

// 五种查询结果互斥覆盖。
func TestFiveStatusesExclusive(t *testing.T) {
	files := []string{"inc/f", "exc/f", "none/f", "empty/f"}
	e := newTestEngine(t, map[string][]string{"c1": files})
	rules := []Rule{
		{Include, "inc/"},
		{Include, "exc/"},
		{Exclude, "exc/f"},
		{Include, "empty/"},
		{Exclude, "empty/f"},
	}
	if _, _, _, err := e.ReplaceRules(rules, "c1", false); err != nil {
		t.Fatal(err)
	}
	want := map[string]PathStatus{
		"inc/f":     StatusMaterialized,
		"inc":       StatusMaterialized,
		"exc/f":     StatusExcludedByRule,
		"none/f":    StatusNoRuleMatch,
		"none":      StatusNoRuleMatch,
		"empty":     StatusEmptyDirectory,
		"missing/f": StatusNotInCommit,
	}
	for p, w := range want {
		got := e.QueryPath(p)
		tlog(t, "QueryPath(%q)=%s want=%s", p, statusName(got), statusName(w))
		if got != w {
			t.Fatalf("QueryPath(%q)=%s want=%s", p, statusName(got), statusName(w))
		}
	}
}

// 错误次序：非法参数 > 提交不存在 > 版本不存在 > 本地修改受阻。
func TestErrorOrder(t *testing.T) {
	e := newTestEngine(t, map[string][]string{"c1": {"a/f1"}})
	if _, _, _, err := e.ReplaceRules([]Rule{{Include, "a/"}}, "c1", false); err != nil {
		t.Fatal(err)
	}
	if err := e.MarkDirty("a/f1"); err != nil {
		t.Fatal(err)
	}
	_, _, _, err := e.ReplaceRules([]Rule{{Include, "a//x"}}, "missing", false)
	tlog(t, "invalid-vs-missingcommit: %v", err)
	if !errors.Is(err, ErrInvalidRules) {
		t.Fatalf("want ErrInvalidRules, got %v", err)
	}
	_, _, err = e.ApplyRuleset("missing", 999, false)
	tlog(t, "missingcommit-vs-missingversion: %v", err)
	if !errors.Is(err, ErrCommitNotFound) {
		t.Fatalf("want ErrCommitNotFound, got %v", err)
	}
	_, _, err = e.ApplyRuleset("", 999, false)
	tlog(t, "missingversion: %v", err)
	if !errors.Is(err, ErrRulesetNotFound) {
		t.Fatalf("want ErrRulesetNotFound, got %v", err)
	}
	if _, _, _, err := e.ReplaceRules([]Rule{{Include, "b/"}}, "", true); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := e.ReplaceRules([]Rule{{Include, "a/"}}, "", true); err != nil {
		t.Fatal(err)
	}
	if err := e.MarkDirty("a/f1"); err != nil {
		t.Fatal(err)
	}
	_, _, err = e.ApplyRuleset("", 2, false)
	tlog(t, "blocked: %v", err)
	var be *BlockedError
	if !errors.As(err, &be) {
		t.Fatalf("want BlockedError, got %v", err)
	}
}

package sparse

import (
	"errors"
	"reflect"
	"testing"
)

func mustAddCommit(t *testing.T, e *Engine, id string, files []string) {
	t.Helper()
	if err := e.AddCommit(id, files); err != nil {
		t.Fatalf("AddCommit(%s): %v", id, err)
	}
}

func mustAddRuleset(t *testing.T, e *Engine, rules ...Rule) int {
	t.Helper()
	v, err := e.AddRuleset(rules)
	if err != nil {
		t.Fatalf("AddRuleset(%v): %v", rules, err)
	}
	return v
}

func mustApply(t *testing.T, e *Engine, commit string, version int, force bool) *ChangeResult {
	t.Helper()
	res, err := e.Apply(commit, version, force)
	if err != nil {
		t.Fatalf("Apply(%q, %d, %v): %v", commit, version, force, err)
	}
	return res
}

func query(t *testing.T, e *Engine, path string) PathStatus {
	t.Helper()
	st, err := e.QueryPath(path)
	if err != nil {
		t.Fatalf("QueryPath(%q): %v", path, err)
	}
	return st
}

// TestPatternScopes covers the three pattern forms: each must hit exactly
// its own range and never overreach.
func TestPatternScopes(t *testing.T) {
	e := NewEngine()
	files := []string{
		"a/b", "a/bc", // exact-pattern probes
		"ad/f1",              // exact-on-directory probe
		"p/f", "p/q/f", "pf", // prefix-pattern probes
		"s/f", "s/d/f", // star-pattern probes
	}
	mustAddCommit(t, e, "c1", files)
	v := mustAddRuleset(t, e,
		Rule{Include, "a/b"},
		Rule{Include, "ad"},
		Rule{Include, "p/"},
		Rule{Include, "s/*"},
	)
	res := mustApply(t, e, "c1", v, false)
	t.Logf("input: files=%v rules=[include a/b, include ad, include p/, include s/*]", files)
	t.Logf("actual added=%v kept=%d", res.Added, res.Kept)

	cases := map[string]PathStatus{
		"a/b":   StatusMaterialized,   // exact hit
		"a/bc":  StatusNoMatchingRule, // exact must not hit string-prefix sibling
		"ad":    StatusEmptyDir,       // exact include hits the dir, but no descendant is materialized
		"ad/f1": StatusNoMatchingRule, // exact on a dir must not hit its children
		"p/f":   StatusMaterialized,   // prefix hits direct child
		"p/q/f": StatusMaterialized,   // prefix hits deeper descendant
		"pf":    StatusNoMatchingRule, // prefix must not hit string-prefix sibling
		"s/f":   StatusMaterialized,   // star hits direct child file
		"s/d/f": StatusNoMatchingRule, // star must not hit deeper levels
		"s/d":   StatusEmptyDir,       // star hits child dir, but it stays empty
	}
	for path, want := range cases {
		if got := query(t, e, path); got != want {
			t.Errorf("QueryPath(%q) = %v, want %v (依据: 三类模式各自命中范围且不越界)", path, got, want)
		}
	}
	t.Logf("verified %d path statuses against pattern-scope expectations", len(cases))
}

// TestLastRuleWins: for equal-scope rules the later one decides.
func TestLastRuleWins(t *testing.T) {
	e := NewEngine()
	mustAddCommit(t, e, "c1", []string{"a/f", "a/g"})
	v1 := mustAddRuleset(t, e, Rule{Include, "a/"}, Rule{Exclude, "a/f"})
	mustApply(t, e, "c1", v1, false)
	t.Logf("input: rules=[include a/, exclude a/f]; expect exclude wins for a/f")
	if got := query(t, e, "a/f"); got != StatusExcludedByRule {
		t.Errorf("a/f = %v, want excluded-by-rule (依据: 后规则覆盖前规则)", got)
	}
	if got := query(t, e, "a/g"); got != StatusMaterialized {
		t.Errorf("a/g = %v, want materialized", got)
	}

	// Reverse the order on the same paths: now include wins.
	v2 := mustAddRuleset(t, e, Rule{Exclude, "a/f"}, Rule{Include, "a/"})
	res := mustApply(t, e, "", v2, false)
	t.Logf("input: rules=[exclude a/f, include a/]; actual added=%v removed=%v", res.Added, res.Removed)
	if got := query(t, e, "a/f"); got != StatusMaterialized {
		t.Errorf("a/f after reorder = %v, want materialized (依据: 后规则覆盖前规则)", got)
	}
}

// TestDirExcludeThenReinclude: excluding a directory does not exclude its
// descendants; a later rule can pull one back in, and its ancestors are
// materialized implicitly.
func TestDirExcludeThenReinclude(t *testing.T) {
	e := NewEngine()
	mustAddCommit(t, e, "c1", []string{"a/x", "a/b/y", "a/b/deep/z"})
	v := mustAddRuleset(t, e,
		Rule{Exclude, "a/"},
		Rule{Include, "a/b/y"},
	)
	res := mustApply(t, e, "c1", v, false)
	t.Logf("input: rules=[exclude a/, include a/b/y]; actual added=%v", res.Added)

	wantAdded := []string{"a", "a/b", "a/b/y"}
	if !reflect.DeepEqual(res.Added, wantAdded) {
		t.Errorf("added = %v, want %v (依据: 目录排除后后代可被再包含, 祖先目录隐含物化)", res.Added, wantAdded)
	}
	if got := query(t, e, "a/x"); got != StatusExcludedByRule {
		t.Errorf("a/x = %v, want excluded-by-rule", got)
	}
	if got := query(t, e, "a/b/deep/z"); got != StatusExcludedByRule {
		t.Errorf("a/b/deep/z = %v, want excluded-by-rule (prefix exclude reaches all descendants)", got)
	}
	if got := query(t, e, "a/b/deep"); got != StatusExcludedByRule {
		t.Errorf("a/b/deep = %v, want excluded-by-rule (dir with no materialized descendants, last match is exclude)", got)
	}
}

// TestEmptyDirNotMaterialized: a directory matched by an include rule still
// does not materialize when no descendant file is materialized.
func TestEmptyDirNotMaterialized(t *testing.T) {
	e := NewEngine()
	mustAddCommit(t, e, "c1", []string{"e/f", "e/g/h"})
	v := mustAddRuleset(t, e,
		Rule{Include, "e"},  // exact include on the directory itself
		Rule{Exclude, "e/"}, // ...but exclude every descendant
	)
	res := mustApply(t, e, "c1", v, false)
	t.Logf("input: rules=[include e, exclude e/]; actual added=%v", res.Added)
	if len(res.Added) != 0 {
		t.Errorf("added = %v, want empty (依据: 空目录不物化)", res.Added)
	}
	if got := query(t, e, "e"); got != StatusEmptyDir {
		t.Errorf("e = %v, want empty-dir", got)
	}
	if got := query(t, e, "e/g"); got != StatusExcludedByRule {
		t.Errorf("e/g = %v, want excluded-by-rule (prefix exclude hits the dir itself as a descendant)", got)
	}
}

// TestInvalidRulesets: malformed patterns and meaningless duplicates are
// rejected as a whole and consume no version.
func TestInvalidRulesets(t *testing.T) {
	e := NewEngine()
	bad := [][]Rule{
		{Rule{Include, ""}},                                              // empty pattern
		{Rule{Include, "a//b"}},                                          // consecutive separators
		{Rule{Include, "a/../b"}},                                        // parent-directory component
		{Rule{Include, "a//"}},                                           // consecutive separators in prefix form
		{Rule{Include, "a/*/b"}},                                         // wildcard not final
		{Rule{Include, "a/"}, Rule{Include, "a/"}},                       // meaningless duplicate
		{Rule{Exclude, "x"}, Rule{Exclude, "x"}},                         // meaningless duplicate
		{Rule{Include, "a/"}, Rule{Exclude, "a/b"}, Rule{Include, "a/"}}, // duplicate after other rules
	}
	for i, rules := range bad {
		if _, err := e.AddRuleset(rules); !errors.Is(err, ErrInvalidParam) {
			t.Errorf("case %d: AddRuleset(%v) err = %v, want ErrInvalidParam (依据: 参数非法整组拒绝)", i, rules, err)
		}
		t.Logf("case %d rejected as expected: %v", i, rules)
	}
	// Same pattern with different action is NOT a duplicate.
	v, err := e.AddRuleset([]Rule{Rule{Include, "a/"}, Rule{Exclude, "a/"}})
	if err != nil {
		t.Fatalf("include+exclude same pattern should be valid: %v", err)
	}
	if v != 1 {
		t.Errorf("version = %d, want 1 (invalid rulesets must not consume versions)", v)
	}
	v2, _ := e.AddRuleset(nil) // empty ruleset is valid
	if v2 != 2 {
		t.Errorf("version = %d, want 2 (versions strictly increasing)", v2)
	}
	t.Logf("versions strictly increasing: %d, %d", v, v2)
}

// TestBlockedListAndForce: a change that would dematerialize locally
// modified paths is rejected wholesale with the complete blocked list;
// force discards the modifications and reports them.
func TestBlockedListAndForce(t *testing.T) {
	e := NewEngine()
	mustAddCommit(t, e, "c1", []string{"a/f1", "a/f2", "b/g1"})
	v1 := mustAddRuleset(t, e, Rule{Include, "a/"}, Rule{Include, "b/"})
	mustApply(t, e, "c1", v1, false)
	for _, p := range []string{"b/g1", "a/f1", "a"} { // unsorted on purpose
		if err := e.SetLocalModified(p, true); err != nil {
			t.Fatalf("SetLocalModified(%q): %v", p, err)
		}
	}
	v2 := mustAddRuleset(t, e, Rule{Exclude, "a/"}, Rule{Exclude, "b/"})

	_, err := e.Apply("", v2, false)
	var blocked *BlockedError
	if !errors.As(err, &blocked) {
		t.Fatalf("Apply err = %v, want *BlockedError", err)
	}
	wantBlocked := []string{"a", "a/f1", "b/g1"} // byte order, deduplicated
	t.Logf("input: dematerialize all; modified={a, a/f1, b/g1}; actual blocked=%v", blocked.Paths)
	if !reflect.DeepEqual(blocked.Paths, wantBlocked) {
		t.Fatalf("blocked = %v, want %v (依据: 受阻列表完整、字节序、不重复)", blocked.Paths, wantBlocked)
	}

	// Rejection changed nothing.
	if got := query(t, e, "a/f1"); got != StatusMaterialized {
		t.Errorf("a/f1 = %v after rejection, want materialized (依据: 被拒绝的调用不改变任何状态)", got)
	}
	if err := e.SetLocalModified("a/f1", false); err != nil {
		t.Errorf("local-mod mark lost after rejection: %v", err)
	}
	if err := e.SetLocalModified("a/f1", true); err != nil {
		t.Fatal(err)
	}

	// Force: modifications are discarded and listed.
	res := mustApply(t, e, "", v2, true)
	t.Logf("force apply: discarded=%v removed=%v", res.Discarded, res.Removed)
	if !reflect.DeepEqual(res.Discarded, wantBlocked) {
		t.Errorf("discarded = %v, want %v (依据: 强制标志的丢弃列表)", res.Discarded, wantBlocked)
	}
	if got := query(t, e, "a/f1"); got != StatusExcludedByRule {
		t.Errorf("a/f1 = %v after force, want excluded-by-rule", got)
	}
	if err := e.SetLocalModified("a/f1", true); !errors.Is(err, ErrNotMaterialized) {
		t.Errorf("SetLocalModified on dematerialized path err = %v, want ErrNotMaterialized", err)
	}
}

// TestCombinedChangeAtomic: switching commit and ruleset in one call is
// all-or-nothing; a single blocked path keeps both unchanged.
func TestCombinedChangeAtomic(t *testing.T) {
	e := NewEngine()
	mustAddCommit(t, e, "c1", []string{"a/f", "keep/g"})
	mustAddCommit(t, e, "c2", []string{"a/f2", "keep/g", "keep/g2"})
	v1 := mustAddRuleset(t, e, Rule{Include, "a/"}, Rule{Include, "keep/"})
	v2 := mustAddRuleset(t, e, Rule{Include, "keep/"})
	mustApply(t, e, "c1", v1, false)
	if err := e.SetLocalModified("a/f", true); err != nil {
		t.Fatal(err)
	}

	_, err := e.Apply("c2", v2, false)
	var blocked *BlockedError
	if !errors.As(err, &blocked) {
		t.Fatalf("Apply err = %v, want *BlockedError", err)
	}
	t.Logf("combined change blocked on %v", blocked.Paths)

	// Neither commit nor ruleset moved.
	if got := query(t, e, "a/f"); got != StatusMaterialized {
		t.Errorf("a/f = %v, want materialized (ruleset unchanged)", got)
	}
	if got := query(t, e, "a/f2"); got != StatusNotInCommit {
		t.Errorf("a/f2 = %v, want not-in-commit (commit unchanged)", got)
	}
	if got := query(t, e, "keep/g"); got != StatusMaterialized {
		t.Errorf("keep/g = %v, want materialized", got)
	}

	// Forced: both halves take effect together.
	res := mustApply(t, e, "c2", v2, true)
	t.Logf("forced combined change: added=%v removed=%v discarded=%v kept=%d",
		res.Added, res.Removed, res.Discarded, res.Kept)
	// a/f2 is not materialized: v2 has no rule covering it.
	if !reflect.DeepEqual(res.Added, []string{"keep/g2"}) {
		t.Errorf("added = %v, want [keep/g2]", res.Added)
	}
	if !reflect.DeepEqual(res.Removed, []string{"a", "a/f"}) {
		t.Errorf("removed = %v, want [a a/f]", res.Removed)
	}
	if !reflect.DeepEqual(res.Discarded, []string{"a/f"}) {
		t.Errorf("discarded = %v, want [a/f]", res.Discarded)
	}
	wantKept := 2 // keep/g and keep
	if res.Kept != wantKept {
		t.Errorf("kept = %d, want %d", res.Kept, wantKept)
	}
}

// TestCommitSwitchMissingFileBlocked: on commit switch, a materialized
// locally-modified path absent from the target commit blocks the switch.
func TestCommitSwitchMissingFileBlocked(t *testing.T) {
	e := NewEngine()
	mustAddCommit(t, e, "c1", []string{"a/f", "b/g"})
	mustAddCommit(t, e, "c2", []string{"b/g"})
	v := mustAddRuleset(t, e, Rule{Include, "a/"}, Rule{Include, "b/"})
	mustApply(t, e, "c1", v, false)
	if err := e.SetLocalModified("a/f", true); err != nil {
		t.Fatal(err)
	}
	_, err := e.Apply("c2", 0, false)
	var blocked *BlockedError
	if !errors.As(err, &blocked) {
		t.Fatalf("Apply err = %v, want *BlockedError", err)
	}
	t.Logf("commit switch blocked on %v (target commit lacks a/f)", blocked.Paths)
	if !reflect.DeepEqual(blocked.Paths, []string{"a/f"}) {
		t.Errorf("blocked = %v, want [a/f]", blocked.Paths)
	}
	if got := query(t, e, "a/f"); got != StatusMaterialized {
		t.Errorf("a/f = %v, want materialized (switch rejected)", got)
	}
}

// TestQueryStatusesExclusive: one scenario per status; the five results are
// mutually exclusive by construction (single enum returned per path).
func TestQueryStatusesExclusive(t *testing.T) {
	e := NewEngine()
	files := []string{"m/f", "x/f", "n/f", "e/f", "xd/f"}
	mustAddCommit(t, e, "c1", files)
	v := mustAddRuleset(t, e,
		Rule{Include, "m/"},
		Rule{Exclude, "x/"},
		Rule{Include, "e"},
		Rule{Exclude, "e/f"},
		Rule{Exclude, "xd"},
	)
	mustApply(t, e, "c1", v, false)
	cases := map[string]PathStatus{
		"m/f":  StatusMaterialized,
		"m":    StatusMaterialized,
		"x/f":  StatusExcludedByRule,
		"xd":   StatusExcludedByRule, // dir whose own last match is exclude
		"n/f":  StatusNoMatchingRule,
		"e":    StatusEmptyDir,
		"zzz":  StatusNotInCommit,
		"xd/f": StatusNoMatchingRule, // exclude hits dir xd, not its child
	}
	for path, want := range cases {
		got := query(t, e, path)
		t.Logf("QueryPath(%q) = %v (want %v)", path, got, want)
		if got != want {
			t.Errorf("QueryPath(%q) = %v, want %v (依据: 五种查询结果互斥)", path, got, want)
		}
	}
}

// TestErrorPrecedence: every adjacent pair of the documented error order.
func TestErrorPrecedence(t *testing.T) {
	e := NewEngine()
	mustAddCommit(t, e, "c1", []string{"a/f"})
	v1 := mustAddRuleset(t, e, Rule{Include, "a/"})
	mustApply(t, e, "c1", v1, false)
	if err := e.SetLocalModified("a/f", true); err != nil {
		t.Fatal(err)
	}
	v2 := mustAddRuleset(t, e, Rule{Exclude, "a/"}) // applying it would block

	// Pair 1: invalid param beats commit-not-found.
	if _, err := e.Apply("missing", -1, false); !errors.Is(err, ErrInvalidParam) {
		t.Errorf("pair1: err = %v, want ErrInvalidParam", err)
	}
	// Pair 2: commit-not-found beats ruleset-version-not-found.
	if _, err := e.Apply("missing", 999, false); !errors.Is(err, ErrCommitNotFound) {
		t.Errorf("pair2: err = %v, want ErrCommitNotFound", err)
	}
	// Pair 3: ruleset-version-not-found beats blocked-by-local-modification.
	if _, err := e.Apply("", 999, false); !errors.Is(err, ErrRulesetVersionNotFound) {
		t.Errorf("pair3: err = %v, want ErrRulesetVersionNotFound", err)
	}
	// Sanity: with valid references the blocked error finally surfaces.
	var blocked *BlockedError
	if _, err := e.Apply("", v2, false); !errors.As(err, &blocked) {
		t.Errorf("sanity: err = %v, want *BlockedError", err)
	}
	t.Logf("error precedence verified: invalid-param > commit-not-found > version-not-found > blocked")
}

// TestListMaterializedPrefix: byte-ordered listing under a prefix.
func TestListMaterializedPrefix(t *testing.T) {
	e := NewEngine()
	mustAddCommit(t, e, "c1", []string{"a/x", "a/b/y", "b/z", "c/w"})
	v := mustAddRuleset(t, e, Rule{Include, "a/"}, Rule{Include, "b/"})
	mustApply(t, e, "c1", v, false)

	got, err := e.ListMaterialized("a")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"a/b", "a/b/y", "a/x"} // strictly under "a", byte order
	t.Logf("ListMaterialized(a) = %v (want %v)", got, want)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ListMaterialized(a) = %v, want %v (依据: 前缀下物化路径按字节序)", got, want)
	}

	all, err := e.ListMaterialized("")
	if err != nil {
		t.Fatal(err)
	}
	wantAll := []string{"a", "a/b", "a/b/y", "a/x", "b", "b/z"}
	t.Logf("ListMaterialized(\"\") = %v (want %v)", all, wantAll)
	if !reflect.DeepEqual(all, wantAll) {
		t.Errorf("ListMaterialized(\"\") = %v, want %v", all, wantAll)
	}

	// A prefix with no materialized paths and a nonexistent prefix both list empty.
	for _, p := range []string{"c", "nope"} {
		got, err := e.ListMaterialized(p)
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 0 {
			t.Errorf("ListMaterialized(%q) = %v, want empty", p, got)
		}
	}
	// Trailing separator is tolerated.
	if got, _ := e.ListMaterialized("a/"); !reflect.DeepEqual(got, want) {
		t.Errorf("ListMaterialized(a/) = %v, want %v", got, want)
	}
}

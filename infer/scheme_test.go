package infer

import (
	"fmt"
	"testing"
)

// 规格主例：层级调整、泛化边界与实例化编号。
func TestPromptExampleGeneralization(t *testing.T) {
	s := mustSession(t, 100)
	must(t, s.Enter())     // L=1
	v1 := mustNewVar(t, s) // Var(1), level 1
	must(t, s.Enter())     // L=2
	v2 := mustNewVar(t, s) // Var(2), level 2
	must(t, s.Unify(v1, Con("List", v2)))
	if l := mustLevel(t, s, 2); l != 1 {
		t.Fatalf("Var(2) level = %d, want 1", l)
	}
	must(t, s.Leave()) // L=1
	// R = Fn(Var(2), List(Var(2)))，Var(2) 层级 1 不大于 1，无量化。
	must(t, s.Bind("f", Con("Fn", v2, v1), false))
	if got := mustLookupStr(t, s, "f"); got != "Fn(Var(2), List(Var(2)))" {
		t.Fatalf("Lookup(f) = %s, want Fn(Var(2), List(Var(2)))", got)
	}
	must(t, s.Leave()) // L=0
	// Var(2) 层级 1 大于 0，量化列表为 [2]，模式为 Fn(G0, G0)。
	must(t, s.Bind("g", Con("Fn", v2, v2), false))
	if got := mustLookupStr(t, s, "g"); got != "Fn(Var(3), Var(3))" {
		t.Fatalf("first Lookup(g) = %s, want Fn(Var(3), Var(3))", got)
	}
	if got := mustLookupStr(t, s, "g"); got != "Fn(Var(4), Var(4))" {
		t.Fatalf("second Lookup(g) = %s, want Fn(Var(4), Var(4))", got)
	}
}

// 规格值限制例：昂贵 Bind 降层级后，后续 Bind 不再量化；
// 非昂贵 Bind 不降层级，后续 Bind 会量化。
func TestPromptExampleValueRestriction(t *testing.T) {
	s := mustSession(t, 100)
	must(t, s.Enter())
	v1 := mustNewVar(t, s) // level 1
	must(t, s.Leave())     // L=0
	must(t, s.Bind("r", Con("List", v1), true))
	if l := mustLevel(t, s, 1); l != 0 {
		t.Fatalf("Var(1) level = %d, want 0 after value restriction", l)
	}
	must(t, s.Bind("h", Con("Fn", v1, v1), false))
	if got := mustLookupStr(t, s, "h"); got != "Fn(Var(1), Var(1))" {
		t.Fatalf("Lookup(h) = %s, want Fn(Var(1), Var(1)) (shared, unquantified)", got)
	}

	s2 := mustSession(t, 100)
	must(t, s2.Enter())
	u1 := mustNewVar(t, s2) // level 1
	must(t, s2.Leave())
	must(t, s2.Bind("r", Con("List", u1), false))
	if l := mustLevel(t, s2, 1); l != 1 {
		t.Fatalf("Var(1) level = %d, want 1 (non-expensive Bind must not lower)", l)
	}
	must(t, s2.Bind("h", Con("Fn", u1, u1), false))
	if got := mustLookupStr(t, s2, "h"); got != "Fn(Var(2), Var(2))" {
		t.Fatalf("Lookup(h) = %s, want Fn(Var(2), Var(2)) (quantified)", got)
	}
}

// 量化次序按先序首次出现，重复出现只记一次。
func TestQuantificationOrderIsPreorderFirstOccurrence(t *testing.T) {
	s := mustSession(t, 100)
	must(t, s.Enter())    // L=1
	a := mustNewVar(t, s) // Var(1)
	b := mustNewVar(t, s) // Var(2)
	c := mustNewVar(t, s) // Var(3)
	must(t, s.Leave())    // L=0
	ty := Con("Fn", Con("Pair", a, b), Con("Pair", a, c))
	must(t, s.Bind("q", ty, false))
	want := "Fn(Pair(Var(4), Var(5)), Pair(Var(4), Var(6)))"
	if got := mustLookupStr(t, s, "q"); got != want {
		t.Fatalf("Lookup(q) = %s, want %s", got, want)
	}
}

// 层级恰等于 L 不量化，L+1 量化。
func TestLevelEqualToLIsNotQuantified(t *testing.T) {
	s := mustSession(t, 100)
	v0 := mustNewVar(t, s) // level 0
	must(t, s.Enter())
	v1 := mustNewVar(t, s) // level 1
	must(t, s.Leave())     // L=0
	must(t, s.Bind("x", Con("Fn", v0, v1), false))
	if got := mustLookupStr(t, s, "x"); got != "Fn(Var(1), Var(3))" {
		t.Fatalf("Lookup(x) = %s, want Fn(Var(1), Var(3))", got)
	}
}

// 模式与原变量解除关联：之后再合一原变量不影响 Lookup 结果。
func TestSchemeDisassociatedFromOriginalVars(t *testing.T) {
	s := mustSession(t, 100)
	must(t, s.Enter())
	v1 := mustNewVar(t, s) // level 1
	must(t, s.Leave())
	must(t, s.Bind("g", Con("Fn", v1, v1), false))
	must(t, s.Unify(v1, Con("Int")))
	if got := mustLookupStr(t, s, "g"); got != "Fn(Var(2), Var(2))" {
		t.Fatalf("Lookup(g) = %s, want Fn(Var(2), Var(2))", got)
	}
}

// Lookup 实例变量层级为当前 L，编号连续递增。
func TestLookupFreshVarsGetCurrentLevel(t *testing.T) {
	s := mustSession(t, 100)
	must(t, s.Enter())
	v1 := mustNewVar(t, s)
	must(t, s.Leave())
	must(t, s.Bind("g", Con("Fn", v1, v1), false))
	must(t, s.Enter()) // L=1
	ty, err := s.Lookup("g")
	must(t, err)
	if got := resolveStr(t, s, ty); got != "Fn(Var(2), Var(2))" {
		t.Fatalf("Lookup(g) = %s, want Fn(Var(2), Var(2))", got)
	}
	if l := mustLevel(t, s, 2); l != 1 {
		t.Fatalf("fresh var level = %d, want current level 1", l)
	}
}

// 变量上限 V：恰好达到允许，再超一被拒；Lookup 超限不消耗编号。
func TestVariableLimitExact(t *testing.T) {
	s := mustSession(t, 2)
	mustNewVar(t, s)
	mustNewVar(t, s)
	_, err := s.NewVar()
	wantErr(t, err, ErrVarLimit)

	s2 := mustSession(t, 3)
	must(t, s2.Enter())
	a := mustNewVar(t, s2)
	b := mustNewVar(t, s2)
	must(t, s2.Leave())
	must(t, s2.Bind("g", Con("Fn", a, b), false))
	mustNewVar(t, s2) // Var(3)，达到上限
	before := dumpSession(s2)
	if _, err := s2.Lookup("g"); err == nil {
		t.Fatal("Lookup needing 2 fresh vars over V=3 should fail")
	} else {
		wantErr(t, err, ErrVarLimit)
	}
	if after := dumpSession(s2); after != before {
		t.Fatalf("rejected Lookup changed state:\nbefore:\n%s\nafter:\n%s", before, after)
	}

	s3 := mustSession(t, 4)
	must(t, s3.Enter())
	x := mustNewVar(t, s3)
	y := mustNewVar(t, s3)
	must(t, s3.Leave())
	must(t, s3.Bind("g", Con("Fn", x, y), false))
	if _, err := s3.Lookup("g"); err != nil {
		t.Fatalf("Lookup at exact limit (2+2=4) should succeed: %v", err)
	}
}

// 层级上限 1000：恰好达到允许，再超一被拒；Leave 到 0 后再降被拒。
func TestLevelLimitExact(t *testing.T) {
	s := mustSession(t, 1)
	for i := 0; i < 1000; i++ {
		must(t, s.Enter())
	}
	wantErr(t, s.Enter(), ErrLevelLimit)
	for i := 0; i < 1000; i++ {
		must(t, s.Leave())
	}
	wantErr(t, s.Leave(), ErrLevelZero)
}

// 环境容量 1000：恰好达到允许，再超一被拒。
func TestEnvLimitExact(t *testing.T) {
	s := mustSession(t, 1)
	for i := 0; i < 1000; i++ {
		must(t, s.Bind(fmt.Sprintf("n%d", i), Con("Int"), false))
	}
	wantErr(t, s.Bind("overflow", Con("Int"), false), ErrEnvLimit)
}

// 同一操作内拒绝原因的优先级：参数非法优先；
// Bind 先重名后容量；Lookup 先不存在后变量超限。
func TestErrorPrecedence(t *testing.T) {
	s := mustSession(t, 10)
	must(t, s.Bind("dup", Con("Int"), false))
	wantErr(t, s.Bind("dup", Con("Nope"), false), ErrInvalidArgument)
	wantErr(t, s.Bind("", Con("Int"), false), ErrInvalidArgument)
	if _, err := s.Lookup(""); err == nil {
		t.Fatal("Lookup with empty name should fail")
	} else {
		wantErr(t, err, ErrInvalidArgument)
	}

	// Bind：重名优先于容量。
	s2 := mustSession(t, 1)
	must(t, s2.Bind("dup", Con("Int"), false))
	for i := 0; i < 999; i++ {
		must(t, s2.Bind(fmt.Sprintf("n%d", i), Con("Int"), false))
	}
	wantErr(t, s2.Bind("dup", Con("Int"), false), ErrNameExists)

	// Lookup：不存在优先于变量超限。
	s3 := mustSession(t, 1)
	must(t, s3.Enter())
	v := mustNewVar(t, s3)
	must(t, s3.Leave())
	must(t, s3.Bind("g", Con("List", v), false)) // 量化 1 个变量，实例化将超限
	if _, err := s3.Lookup("missing"); err == nil {
		t.Fatal("Lookup of missing name should fail")
	} else {
		wantErr(t, err, ErrNameNotFound)
	}
	if _, err := s3.Lookup("g"); err == nil {
		t.Fatal("Lookup exceeding variable limit should fail")
	} else {
		wantErr(t, err, ErrVarLimit)
	}
}

// 各类被拒绝的操作不得改变任何状态。
func TestRejectedOpsDoNotChangeState(t *testing.T) {
	s := mustSession(t, 1)
	must(t, s.Enter())
	v1 := mustNewVar(t, s)
	must(t, s.Leave())
	must(t, s.Bind("a", Con("List", v1), false))
	snapshot := dumpSession(s)
	reject := func(op func() error) {
		t.Helper()
		if err := op(); err == nil {
			t.Fatal("expected rejection, got success")
		}
		if now := dumpSession(s); now != snapshot {
			t.Fatalf("state changed:\nbefore:\n%s\nafter:\n%s", snapshot, now)
		}
	}
	reject(func() error { _, err := s.NewVar(); return err })             // 变量超限
	reject(func() error { _, err := s.Lookup("a"); return err })          // 实例化变量超限
	reject(func() error { _, err := s.Lookup("zz"); return err })         // 名字不存在
	reject(func() error { _, err := s.Lookup(""); return err })           // 空名字
	reject(func() error { return s.Bind("a", Con("Int"), false) })        // 重名
	reject(func() error { return s.Bind("", Con("Int"), false) })         // 空名字
	reject(func() error { return s.Bind("b", Con("Nope"), false) })       // 非法类型
	reject(func() error { return s.Unify(v1, Con("Nope")) })              // 非法类型
	reject(func() error { return s.Unify(v1, Con("List", v1)) })          // 出现检查
	reject(func() error { return s.Unify(Con("Int"), Con("Bool")) })      // 构造子不匹配
	reject(func() error { return s.Leave() })                             // 层级为零
	reject(func() error { _, err := s.Resolve(Con("Nope")); return err }) // 非法类型
	reject(func() error { _, err := s.Level(9); return err })             // 未知变量
}

// Level 对已绑定到构造子应用的变量拒绝；沿变量链走到未绑定变量。
func TestLevelOfBoundAndChainedVars(t *testing.T) {
	s := mustSession(t, 10)
	v1 := mustNewVar(t, s)
	must(t, s.Unify(v1, Con("Int")))
	if _, err := s.Level(1); err == nil {
		t.Fatal("Level(1) should fail: bound to constructor")
	} else {
		wantErr(t, err, ErrBound)
	}

	s2 := mustSession(t, 10)
	must(t, s2.Enter())
	a := mustNewVar(t, s2)
	b := mustNewVar(t, s2)
	must(t, s2.Unify(a, b))
	if l := mustLevel(t, s2, 1); l != 1 {
		t.Fatalf("Level(1) should follow chain to Var(2) at level 1, got %d", l)
	}
}

// 相同操作序列重放得到完全相同的状态。
func TestReplayDeterminism(t *testing.T) {
	script := func(s *Session) {
		must(t, s.Enter())
		a := mustNewVar(t, s)
		must(t, s.Enter())
		b := mustNewVar(t, s)
		must(t, s.Unify(a, Con("List", b)))
		must(t, s.Leave())
		must(t, s.Bind("f", Con("Fn", b, a), false))
		must(t, s.Leave())
		must(t, s.Bind("g", Con("Fn", b, b), false))
		if _, err := s.Lookup("g"); err != nil {
			t.Fatal(err)
		}
		if _, err := s.Lookup("f"); err != nil {
			t.Fatal(err)
		}
	}
	s1 := mustSession(t, 100)
	s2 := mustSession(t, 100)
	script(s1)
	script(s2)
	if d1, d2 := dumpSession(s1), dumpSession(s2); d1 != d2 {
		t.Fatalf("replay diverged:\n%s\nvs\n%s", d1, d2)
	}
}

package matcher

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
)

func mustDefine(t *testing.T, e *Engine, name string, ctors ...Ctor) {
	t.Helper()
	if err := e.DefineType(name, ctors); err != nil {
		t.Fatalf("DefineType(%s) 失败: %v", name, err)
	}
}

func natListEngine(t *testing.T) *Engine {
	t.Helper()
	e := NewEngine()
	mustDefine(t, e, "Nat", Ctor{Name: "Z"}, Ctor{Name: "S", Fields: []string{"Nat"}})
	mustDefine(t, e, "List", Ctor{Name: "Nil"}, Ctor{Name: "Cons", Fields: []string{"Nat", "List"}})
	return e
}

func mustSession(t *testing.T, e *Engine, typ string) int {
	t.Helper()
	id, err := e.NewSession(typ)
	if err != nil {
		t.Fatalf("NewSession(%s) 失败: %v", typ, err)
	}
	return id
}

type armResult struct {
	idx      int
	branches []bool
	arm      bool
}

func mustAdd(t *testing.T, e *Engine, sid int, p *Pat, guarded bool) armResult {
	t.Helper()
	idx, branches, arm, err := e.AddArm(sid, p, guarded)
	if err != nil {
		t.Fatalf("AddArm(%d, %v) 失败: %v", sid, p, err)
	}
	return armResult{idx, branches, arm}
}

func mustCheck(t *testing.T, e *Engine, sid int) (bool, string) {
	t.Helper()
	exh, ce, err := e.Check(sid)
	if err != nil {
		t.Fatalf("Check(%d) 失败: %v", sid, err)
	}
	return exh, ce
}

func errKind(err error) ErrKind {
	var me *Error
	if errors.As(err, &me) {
		return me.Kind
	}
	return 0
}

func boolsEq(a, b []bool) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func (r armResult) expect(t *testing.T, idx int, branches []bool, arm bool) {
	t.Helper()
	if r.idx != idx || !boolsEq(r.branches, branches) || r.arm != arm {
		t.Fatalf("臂结果 = %+v, 期望 idx=%d branches=%v arm=%v", r, idx, branches, arm)
	}
}

// 题目给出的完整走查示例。
func TestListWalkthrough(t *testing.T) {
	e := natListEngine(t)
	s := mustSession(t, e, "List")

	mustAdd(t, e, s, C("Cons", C("Z"), W()), false).expect(t, 1, []bool{false}, false)
	if exh, ce := mustCheck(t, e, s); exh || ce != "Nil" {
		t.Fatalf("Check = (%v, %q), 期望 (false, Nil)", exh, ce)
	}

	mustAdd(t, e, s, C("Cons", C("S", W()), C("Nil")), false).expect(t, 2, []bool{false}, false)
	if exh, ce := mustCheck(t, e, s); exh || ce != "Nil" {
		t.Fatalf("Check = (%v, %q), 期望 (false, Nil)", exh, ce)
	}

	mustAdd(t, e, s, C("Nil"), false).expect(t, 3, []bool{false}, false)
	if exh, ce := mustCheck(t, e, s); exh || ce != "Cons(S(_), Cons(_, _))" {
		t.Fatalf("Check = (%v, %q), 期望 (false, Cons(S(_), Cons(_, _)))", exh, ce)
	}

	mustAdd(t, e, s, C("Cons", C("Z"), C("Nil")), false).expect(t, 4, []bool{true}, true)

	mustAdd(t, e, s, W(), true).expect(t, 5, []bool{false}, false)
	if exh, ce := mustCheck(t, e, s); exh || ce != "Cons(S(_), Cons(_, _))" {
		t.Fatalf("Check = (%v, %q), 期望仍不穷尽", exh, ce)
	}

	mustAdd(t, e, s,
		Or(C("Cons", C("S", W()), C("Cons", W(), W())),
			C("Cons", C("S", C("Z")), C("Cons", C("Z"), W()))),
		false).expect(t, 6, []bool{false, true}, false)

	if exh, ce := mustCheck(t, e, s); !exh || ce != "" {
		t.Fatalf("Check = (%v, %q), 期望穷尽", exh, ce)
	}
}

// 反例构造三分支：Σ 为空取 _、Σ 不完全取第一个缺失构造子、Σ 完全按声明顺序特化。
func TestMissingThreeCases(t *testing.T) {
	e := natListEngine(t)

	// Σ 为空（空行集）：反例为 _。
	s1 := mustSession(t, e, "Nat")
	if exh, ce := mustCheck(t, e, s1); exh || ce != "_" {
		t.Fatalf("空会话 Check = (%v, %q), 期望 (false, _)", exh, ce)
	}

	// Σ 不完全：取声明顺序中第一个缺失构造子。
	s2 := mustSession(t, e, "Nat")
	mustAdd(t, e, s2, C("S", W()), false)
	if exh, ce := mustCheck(t, e, s2); exh || ce != "Z" {
		t.Fatalf("Check = (%v, %q), 期望 (false, Z)", exh, ce)
	}

	// Σ 完全：按声明顺序逐个特化，取第一个有反例的构造子。
	s3 := mustSession(t, e, "Nat")
	mustAdd(t, e, s3, C("Z"), false)
	mustAdd(t, e, s3, C("S", C("Z")), false)
	if exh, ce := mustCheck(t, e, s3); exh || ce != "S(S(_))" {
		t.Fatalf("Check = (%v, %q), 期望 (false, S(S(_)))", exh, ce)
	}
}

// 反例按声明顺序取第一个，而非字典序。
func TestDeclOrderNotLexical(t *testing.T) {
	e := NewEngine()
	// 声明顺序 Mango < Apple < Cherry（字典序相反）。
	mustDefine(t, e, "T", Ctor{Name: "Mango"}, Ctor{Name: "Apple"}, Ctor{Name: "Cherry"})
	s := mustSession(t, e, "T")
	mustAdd(t, e, s, C("Cherry"), false)
	if exh, ce := mustCheck(t, e, s); exh || ce != "Mango" {
		t.Fatalf("Check = (%v, %q), 期望 (false, Mango)", exh, ce)
	}

	// Σ 完全时特化也按声明顺序：Q 在 B 之前声明。
	e2 := NewEngine()
	mustDefine(t, e2, "D",
		Ctor{Name: "Q", Fields: []string{"D"}},
		Ctor{Name: "B", Fields: []string{"D"}},
		Ctor{Name: "Z0"})
	s2 := mustSession(t, e2, "D")
	mustAdd(t, e2, s2, C("Q", C("Z0")), false)
	mustAdd(t, e2, s2, C("B", C("Z0")), false)
	mustAdd(t, e2, s2, C("Z0"), false)
	if exh, ce := mustCheck(t, e2, s2); exh || ce != "Q(Q(_))" {
		t.Fatalf("Check = (%v, %q), 期望 (false, Q(Q(_)))", exh, ce)
	}
}

// 嵌套 Or 按从左到右先序展开成多行。
func TestNestedOrExpansion(t *testing.T) {
	e := natListEngine(t)
	s := mustSession(t, e, "Nat")
	// 顶层 Or 有 2 个分支；展开行为 Z, S(Z), S(S(Z))。
	mustAdd(t, e, s, Or(Or(C("Z"), C("S", C("Z"))), C("S", C("S", C("Z")))), false).
		expect(t, 1, []bool{false, false}, false)
	if exh, ce := mustCheck(t, e, s); exh || ce != "S(S(S(_)))" {
		t.Fatalf("Check = (%v, %q), 期望 (false, S(S(S(_))))", exh, ce)
	}
	// 嵌套 Or 出现在构造子内部。
	s2 := mustSession(t, e, "List")
	mustAdd(t, e, s2, C("Cons", Or(C("Z"), C("S", C("Z"))), C("Nil")), false)
	mustAdd(t, e, s2, Or(C("Nil"), C("Cons", C("S", C("S", W())), C("Nil"))), false)
	// 已覆盖 Nil、Cons(Z,Nil)、Cons(S(Z),Nil)、Cons(S(S(_)),Nil)。
	// 首列 Σ={Z,S} 完全，特化 Z 后 Cons 缺失。
	if exh, ce := mustCheck(t, e, s2); exh || ce != "Cons(Z, Cons(_, _))" {
		t.Fatalf("Check = (%v, %q), 期望 (false, Cons(Z, Cons(_, _)))", exh, ce)
	}
}

// 顶层 Or 分支分别报告冗余；本臂内更前分支参与判定（含本臂带守卫时）。
func TestTopLevelOrBranchRedundancy(t *testing.T) {
	e := natListEngine(t)
	s := mustSession(t, e, "Nat")
	mustAdd(t, e, s, C("S", C("Z")), false).expect(t, 1, []bool{false}, false)

	// 分支 1 新增、分支 2 被臂 1 覆盖。
	mustAdd(t, e, s, Or(C("Z"), C("S", C("Z"))), false).
		expect(t, 2, []bool{false, true}, false)

	// 带守卫的臂：本臂内更前分支仍参与后面分支的判定。
	// 覆盖集目前只有 {Z, S(Z)}，分支 1 不冗余，分支 2 被本臂分支 1 覆盖。
	mustAdd(t, e, s, Or(C("S", C("S", W())), C("S", C("S", C("S", C("Z"))))), true).
		expect(t, 3, []bool{false, true}, false)

	// 守卫臂不参与后续判定：S(S(S(Z))) 此前只被守卫臂覆盖。
	mustAdd(t, e, s, C("S", C("S", C("S", C("Z")))), false).
		expect(t, 4, []bool{false}, false)
}

// 带守卫的臂不参与后续冗余判定与穷尽性。
func TestGuardedArmNotParticipating(t *testing.T) {
	e := natListEngine(t)
	s := mustSession(t, e, "Nat")
	mustAdd(t, e, s, W(), true).expect(t, 1, []bool{false}, false)
	if exh, _ := mustCheck(t, e, s); exh {
		t.Fatal("带守卫的 _ 不应使 Check 穷尽")
	}
	// 若守卫臂参与，则下面的 _ 会被判冗余。
	mustAdd(t, e, s, W(), false).expect(t, 2, []bool{false}, false)
	if exh, ce := mustCheck(t, e, s); !exh || ce != "" {
		t.Fatalf("Check = (%v, %q), 期望穷尽", exh, ce)
	}
}

// 整条冗余的臂与带守卫的臂不使缓存失效；含新覆盖的臂使其失效。
func TestCheckCache(t *testing.T) {
	e := natListEngine(t)
	s := mustSession(t, e, "Nat")

	mustCheck(t, e, s)
	if e.missingCalls != 1 {
		t.Fatalf("missingCalls = %d, 期望 1", e.missingCalls)
	}
	mustCheck(t, e, s) // 命中缓存
	if e.missingCalls != 1 {
		t.Fatalf("missingCalls = %d, 期望 1（缓存命中）", e.missingCalls)
	}

	mustAdd(t, e, s, W(), true) // 带守卫：不失效
	mustCheck(t, e, s)
	if e.missingCalls != 1 {
		t.Fatalf("missingCalls = %d, 期望 1（守卫臂不失效）", e.missingCalls)
	}

	mustAdd(t, e, s, C("Z"), false) // 新覆盖：失效
	mustCheck(t, e, s)
	if e.missingCalls != 2 {
		t.Fatalf("missingCalls = %d, 期望 2（新覆盖使失效）", e.missingCalls)
	}

	mustAdd(t, e, s, Or(C("Z")), false) // 整条冗余：不失效
	mustCheck(t, e, s)
	if e.missingCalls != 2 {
		t.Fatalf("missingCalls = %d, 期望 2（冗余臂不失效）", e.missingCalls)
	}

	mustAdd(t, e, s, C("S", W()), true) // 带守卫：不失效
	mustCheck(t, e, s)
	if e.missingCalls != 2 {
		t.Fatalf("missingCalls = %d, 期望 2（守卫臂不失效）", e.missingCalls)
	}

	mustAdd(t, e, s, C("S", W()), false) // 新覆盖：失效
	if exh, _ := mustCheck(t, e, s); !exh {
		t.Fatal("期望穷尽")
	}
	if e.missingCalls != 3 {
		t.Fatalf("missingCalls = %d, 期望 3", e.missingCalls)
	}
}

// 零字段构造子与递归类型。
func TestNullaryAndRecursive(t *testing.T) {
	e := NewEngine()
	mustDefine(t, e, "B", Ctor{Name: "T"}, Ctor{Name: "F"})
	s := mustSession(t, e, "B")
	mustAdd(t, e, s, C("T"), false)
	if exh, ce := mustCheck(t, e, s); exh || ce != "F" {
		t.Fatalf("Check = (%v, %q), 期望 (false, F)", exh, ce)
	}
	mustAdd(t, e, s, C("F"), false)
	if exh, _ := mustCheck(t, e, s); !exh {
		t.Fatal("期望穷尽")
	}
}

// 无有限值类型被拒；字段类型未登记被拒。
func TestDefineTypeInvalid(t *testing.T) {
	e := NewEngine()
	if err := e.DefineType("X", []Ctor{{Name: "Only", Fields: []string{"X"}}}); errKind(err) != ErrInvalidArgument {
		t.Fatalf("无有限值类型应报参数非法, got %v", err)
	}
	if err := e.DefineType("Y", []Ctor{{Name: "Mk", Fields: []string{"Nope"}}}); errKind(err) != ErrInvalidArgument {
		t.Fatalf("字段类型未登记应报参数非法, got %v", err)
	}
	if err := e.DefineType("", []Ctor{{Name: "A"}}); errKind(err) != ErrInvalidArgument {
		t.Fatalf("空类型名应报参数非法, got %v", err)
	}
	if err := e.DefineType("Z", nil); errKind(err) != ErrInvalidArgument {
		t.Fatalf("空构造子列表应报参数非法, got %v", err)
	}
	long := strings.Repeat("n", 33)
	if err := e.DefineType(long, []Ctor{{Name: "A"}}); errKind(err) != ErrInvalidArgument {
		t.Fatalf("超长类型名应报参数非法, got %v", err)
	}
	if err := e.DefineType("W", []Ctor{{Name: "A", Fields: []string{"W", "W", "W", "W", "W"}}}); errKind(err) != ErrInvalidArgument {
		t.Fatalf("字段数越界应报参数非法, got %v", err)
	}
	// 被拒绝后状态不变：同名可重新登记。
	mustDefine(t, e, "X", Ctor{Name: "Base"}, Ctor{Name: "Rec", Fields: []string{"X"}})
}

// 拒绝类别顺序：参数非法先于重名。
func TestDefineTypeRejectionOrder(t *testing.T) {
	e := natListEngine(t)
	// 类型名重名 + 构造子名空：报参数非法。
	if err := e.DefineType("Nat", []Ctor{{Name: ""}}); errKind(err) != ErrInvalidArgument {
		t.Fatalf("参数非法应先于重名, got %v", err)
	}
	// 类型名重名。
	if err := e.DefineType("Nat", []Ctor{{Name: "Fresh1"}}); errKind(err) != ErrDuplicateName {
		t.Fatalf("类型名重名, got %v", err)
	}
	// 构造子名与其他类型冲突。
	if err := e.DefineType("M", []Ctor{{Name: "Z"}}); errKind(err) != ErrDuplicateName {
		t.Fatalf("构造子名重名, got %v", err)
	}
	// 构造子名在本类型内重复。
	if err := e.DefineType("M", []Ctor{{Name: "Dup"}, {Name: "Dup"}}); errKind(err) != ErrDuplicateName {
		t.Fatalf("构造子名本类型内重复, got %v", err)
	}
}

// AddArm 各类拒绝的先后顺序。
func TestAddArmRejectionOrder(t *testing.T) {
	e := natListEngine(t)
	s := mustSession(t, e, "Nat")

	deep := C("Z")
	for i := 0; i < 16; i++ {
		deep = C("S", deep)
	}
	// 深度超限 + 会话号不存在：报参数非法。
	if _, _, _, err := e.AddArm(999, deep, false); errKind(err) != ErrInvalidArgument {
		t.Fatalf("参数非法应先于会话号不存在, got %v", err)
	}
	// 会话号不存在 + 构造子不存在：报会话号不存在。
	if _, _, _, err := e.AddArm(999, C("Nope"), false); errKind(err) != ErrNoSuchSession {
		t.Fatalf("会话号不存在应先于模式校验, got %v", err)
	}
	// 模式校验：构造子名不存在。
	if _, _, _, err := e.AddArm(s, C("Nope"), false); errKind(err) != ErrInvalidPattern {
		t.Fatalf("构造子名不存在, got %v", err)
	}
	// 模式校验：构造子不属于期望类型。
	if _, _, _, err := e.AddArm(s, C("Nil"), false); errKind(err) != ErrInvalidPattern {
		t.Fatalf("构造子不属于期望类型, got %v", err)
	}
	// 模式校验：子模式个数不符。
	if _, _, _, err := e.AddArm(s, C("S"), false); errKind(err) != ErrInvalidPattern {
		t.Fatalf("子模式个数不符, got %v", err)
	}
	// 先序：外层构造子不属于期望类型先于内层名不存在。
	if _, _, _, err := e.AddArm(s, C("Cons", C("Nope"), C("Nope")), false); errKind(err) != ErrInvalidPattern {
		t.Fatalf("先序第一个违规, got %v", err)
	}
	var me *Error
	if _, _, _, err := e.AddArm(s, C("Cons", C("Nope"), C("Nope")), false); errors.As(err, &me) &&
		!strings.Contains(me.Msg, "不属于期望类型") {
		t.Fatalf("先序第一个违规应为外层类型不符, got %v", me.Msg)
	}
	// 深度 16 恰好允许。
	ok := C("Z")
	for i := 0; i < 15; i++ {
		ok = C("S", ok)
	}
	if _, _, _, err := e.AddArm(s, ok, false); err != nil {
		t.Fatalf("深度 16 应允许, got %v", err)
	}
}

// 展开数 4096 恰好允许，超过则被拒；会话累计 20000 超限被拒。
func TestExpansionLimits(t *testing.T) {
	e := NewEngine()
	mustDefine(t, e, "T8",
		Ctor{Name: "A"}, Ctor{Name: "B"}, Ctor{Name: "C"}, Ctor{Name: "D"},
		Ctor{Name: "E"}, Ctor{Name: "F"}, Ctor{Name: "G"}, Ctor{Name: "H"})
	mustDefine(t, e, "Box", Ctor{Name: "Mk", Fields: []string{"T8", "T8", "T8", "T8"}})
	orAll := func() *Pat {
		return Or(C("A"), C("B"), C("C"), C("D"), C("E"), C("F"), C("G"), C("H"))
	}
	full := func() *Pat { return C("Mk", orAll(), orAll(), orAll(), orAll()) } // 8^4 = 4096

	s := mustSession(t, e, "Box")
	// 恰好 4096：允许。
	if _, _, _, err := e.AddArm(s, full(), false); err != nil {
		t.Fatalf("展开数 4096 应允许, got %v", err)
	}
	// 4097：被拒，且不改变状态（臂序号、累计展开数）。
	over := Or(full(), C("Mk", C("A"), C("A"), C("A"), C("A")))
	if _, _, _, err := e.AddArm(s, over, false); errKind(err) != ErrExpansionLimit {
		t.Fatalf("展开数 4097 应报展开超限, got %v", err)
	}
	// 模式校验先于展开超限：含不存在构造子的超大模式报模式校验。
	bad := Or(full(), C("Nope"))
	if _, _, _, err := e.AddArm(s, bad, false); errKind(err) != ErrInvalidPattern {
		t.Fatalf("模式校验应先于展开超限, got %v", err)
	}
}

// 会话累计展开超限被拒，且被拒不改变累计值。
func TestSessionExpansionCumulative(t *testing.T) {
	e := NewEngine()
	mustDefine(t, e, "T8",
		Ctor{Name: "A"}, Ctor{Name: "B"}, Ctor{Name: "C"}, Ctor{Name: "D"},
		Ctor{Name: "E"}, Ctor{Name: "F"}, Ctor{Name: "G"}, Ctor{Name: "H"})
	mustDefine(t, e, "Box", Ctor{Name: "Mk", Fields: []string{"T8", "T8", "T8", "T8"}})
	orAll := func() *Pat {
		return Or(C("A"), C("B"), C("C"), C("D"), C("E"), C("F"), C("G"), C("H"))
	}
	full := func() *Pat { return C("Mk", orAll(), orAll(), orAll(), orAll()) }

	s := mustSession(t, e, "Box")
	for i := 0; i < 4; i++ { // 4 * 4096 = 16384
		mustAdd(t, e, s, full(), i%2 == 1) // 混入带守卫的臂，同样计入
	}
	if _, _, _, err := e.AddArm(s, full(), false); errKind(err) != ErrExpansionLimit {
		t.Fatalf("累计 20480 应报展开超限, got %v", err)
	}
	// 被拒后小展开臂仍可加入，且序号连续；覆盖已穷尽故冗余。
	mustAdd(t, e, s, C("Mk", C("A"), W(), W(), W()), false).expect(t, 5, []bool{true}, true)
}

// 被拒绝的操作不改变任何状态。
func TestRejectionKeepsState(t *testing.T) {
	e := natListEngine(t)
	// 失败的 NewSession 不消耗会话号。
	if _, err := e.NewSession("Nope"); errKind(err) != ErrInvalidArgument {
		t.Fatalf("未登记类型应报参数非法, got %v", err)
	}
	s := mustSession(t, e, "Nat")
	if s != 1 {
		t.Fatalf("会话号 = %d, 期望 1", s)
	}
	// 失败的 AddArm 不消耗臂序号。
	if _, _, _, err := e.AddArm(s, C("Nope"), false); errKind(err) != ErrInvalidPattern {
		t.Fatalf("模式校验失败, got %v", err)
	}
	mustAdd(t, e, s, C("Z"), false).expect(t, 1, []bool{false}, false)
	// 失败的 DefineType 不登记构造子名。
	if err := e.DefineType("Bad", []Ctor{{Name: "Fresh2", Fields: []string{"Nope"}}}); errKind(err) != ErrInvalidArgument {
		t.Fatalf("字段类型未登记, got %v", err)
	}
	mustDefine(t, e, "Good", Ctor{Name: "Fresh2"})
}

// 相同操作序列重放得到完全相同的判定与反例文本。
func TestReplayDeterminism(t *testing.T) {
	run := func() []string {
		e := natListEngine(t)
		s := mustSession(t, e, "List")
		var out []string
		record := func(p *Pat, g bool) {
			idx, br, arm, err := e.AddArm(s, p, g)
			if err != nil {
				out = append(out, "ERR:"+err.Error())
				return
			}
			out = append(out, fmt.Sprintf("%d %v %v", idx, br, arm))
			exh, ce, _ := e.Check(s)
			out = append(out, fmt.Sprintf("%v %q", exh, ce))
		}
		record(C("Cons", C("Z"), W()), false)
		record(C("Cons", C("S", W()), C("Nil")), false)
		record(C("Nil"), false)
		record(C("Cons", C("Z"), C("Nil")), false)
		record(W(), true)
		record(Or(C("Cons", C("S", W()), C("Cons", W(), W())),
			C("Cons", C("S", C("Z")), C("Cons", C("Z"), W()))), false)
		return out
	}
	first := run()
	second := run()
	if strings.Join(first, "\n") != strings.Join(second, "\n") {
		t.Fatalf("重放结果不一致:\n%v\nvs\n%v", first, second)
	}
}

// 并发调用等价于某个串行顺序（配合 -race）。
func TestConcurrent(t *testing.T) {
	e := natListEngine(t)
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s, err := e.NewSession("Nat")
			if err != nil {
				t.Error(err)
				return
			}
			for i := 0; i < 20; i++ {
				if _, _, _, err := e.AddArm(s, C("Z"), i%3 == 0); err != nil {
					t.Error(err)
					return
				}
				if _, _, err := e.Check(s); err != nil {
					t.Error(err)
					return
				}
			}
		}()
	}
	wg.Wait()
}

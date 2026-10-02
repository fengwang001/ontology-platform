package module

import (
	"reflect"
	"strings"
	"testing"
)

// ---- 构造辅助 ----

func imp(src string, names ...string) Import { return Import{Source: src, Names: names} }

func lets(names ...string) []Export {
	var out []Export
	for _, n := range names {
		out = append(out, Export{Name: n, Kind: Let})
	}
	return out
}

func funcs(names ...string) []Export {
	var out []Export
	for _, n := range names {
		out = append(out, Export{Name: n, Kind: Func})
	}
	return out
}

func cat(parts ...[]Export) []Export {
	var out []Export
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}

func mustAdd(t *testing.T, s *Session, name string, imports []Import, exports []Export, reexports []Reexport, body []Step) {
	t.Helper()
	if rej := s.AddModule(name, imports, exports, reexports, body); rej != nil {
		t.Fatalf("AddModule(%s) 被意外拒绝: %v", name, rej)
	}
}

func mustEvalOK(t *testing.T, s *Session, root string) EvalResult {
	t.Helper()
	res, rej := s.Evaluate(root)
	if rej != nil {
		t.Fatalf("Evaluate(%s) 被意外拒绝: %v", root, rej)
	}
	if !res.OK {
		t.Fatalf("Evaluate(%s) 意外出错: %q", root, res.ErrValue)
	}
	return res
}

func mustEvalErr(t *testing.T, s *Session, root, wantErr string) EvalResult {
	t.Helper()
	res, rej := s.Evaluate(root)
	if rej != nil {
		t.Fatalf("Evaluate(%s) 被意外拒绝: %v", root, rej)
	}
	if res.OK || res.ErrValue != wantErr {
		t.Fatalf("Evaluate(%s) 结果 = (%v, %q)，想要错误 %q", root, res.OK, res.ErrValue, wantErr)
	}
	return res
}

func mustReject(t *testing.T, s *Session, root string, reason RejectReason) *Reject {
	t.Helper()
	_, rej := s.Evaluate(root)
	if rej == nil {
		t.Fatalf("Evaluate(%s) 未被拒绝，想要 %v", root, reason)
	}
	if rej.Reason != reason {
		t.Fatalf("Evaluate(%s) 拒绝原因 = %v，想要 %v（%v）", root, rej.Reason, reason, rej)
	}
	return rej
}

func mustState(t *testing.T, s *Session, name string, want State) {
	t.Helper()
	st, rej := s.Status(name)
	if rej != nil {
		t.Fatalf("Status(%s) 被意外拒绝: %v", name, rej)
	}
	if st.State != want {
		t.Fatalf("Status(%s) = %v，想要 %v", name, st.State, want)
	}
}

func mustOrder(t *testing.T, s *Session, want ...string) {
	t.Helper()
	if got := s.Order(); !reflect.DeepEqual(got, want) {
		t.Fatalf("Order() = %v，想要 %v", got, want)
	}
}

// snapshot 会话全量可观察状态（含非导出计数器），用于验证"被拒绝不改状态"。
type snapshot struct {
	states   map[string]State
	errs     map[string]string
	inited   map[string]map[string]bool
	bodyRuns map[string]int
	order    []string
}

func capture(s *Session) snapshot {
	sn := snapshot{
		states:   make(map[string]State),
		errs:     make(map[string]string),
		inited:   make(map[string]map[string]bool),
		bodyRuns: make(map[string]int),
		order:    s.Order(),
	}
	for name, m := range s.modules {
		sn.states[name] = m.state
		sn.errs[name] = m.errValue
		sn.bodyRuns[name] = m.bodyRuns
		bits := make(map[string]bool)
		for k, v := range m.inited {
			bits[k] = v
		}
		sn.inited[name] = bits
	}
	return sn
}

func mustSameSnapshot(t *testing.T, what string, before, after snapshot) {
	t.Helper()
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("%s 后状态被改变:\n前: %+v\n后: %+v", what, before, after)
	}
}

// checkInvariants 验证全局不变量。
func checkInvariants(t *testing.T, s *Session) {
	t.Helper()
	order := s.Order()
	seen := make(map[string]int)
	for _, n := range order {
		seen[n]++
		if seen[n] > 1 {
			t.Fatalf("模块 %s 在次序中出现 %d 次", n, seen[n])
		}
	}
	for name, m := range s.modules {
		if m.state == StateEvaluating {
			t.Fatalf("模块 %s 仍处于求值中", name)
		}
		if m.bodyRuns > 1 {
			t.Fatalf("模块 %s 的体执行了 %d 次", name, m.bodyRuns)
		}
		if m.state == StateEvaluated && seen[name] != 1 {
			t.Fatalf("已求值模块 %s 不在次序中", name)
		}
		if m.state == StateError && seen[name] != 0 {
			t.Fatalf("出错模块 %s 出现在次序中", name)
		}
	}
}

// ---- 无环依赖与求值次序 ----

func TestAcyclicPostorderAndImportOrder(t *testing.T) {
	build := func(rFirst, rSecond Import) *Session {
		s := NewSession()
		mustAdd(t, s, "L1", nil, funcs("a"), nil, nil)
		mustAdd(t, s, "L2", nil, funcs("b"), nil, nil)
		mustAdd(t, s, "M", []Import{imp("L1", "a"), imp("L2", "b")}, funcs("x"), nil, nil)
		mustAdd(t, s, "R", []Import{rFirst, rSecond}, nil, nil, nil)
		return s
	}
	// 导入语句次序决定求值次序：先 M 后 L2。
	s1 := build(imp("M", "x"), imp("L2", "b"))
	res := mustEvalOK(t, s1, "R")
	mustOrder(t, s1, "L1", "L2", "M", "R")
	if !reflect.DeepEqual(res.Appended, []string{"L1", "L2", "M", "R"}) {
		t.Fatalf("Appended = %v", res.Appended)
	}
	// 先 L2 后 M。
	s2 := build(imp("L2", "b"), imp("M", "x"))
	mustEvalOK(t, s2, "R")
	mustOrder(t, s2, "L2", "L1", "M", "R")
	checkInvariants(t, s1)
	checkInvariants(t, s2)
}

func TestDuplicateSourceEvaluatedOnce(t *testing.T) {
	s := NewSession()
	mustAdd(t, s, "L", nil, funcs("a", "b"), nil, nil)
	mustAdd(t, s, "D", []Import{imp("L", "a"), imp("L", "b")}, nil, nil, nil)
	mustEvalOK(t, s, "D")
	mustOrder(t, s, "L", "D")
	if got := s.modules["L"].bodyRuns; got != 1 {
		t.Fatalf("L 的体执行 %d 次，想要 1", got)
	}
}

// ---- 循环依赖：入口不同结果不同 ----

func addCycleAB(t *testing.T, s *Session) {
	mustAdd(t, s, "A",
		[]Import{imp("B", "h")},
		cat(lets("x"), funcs("f")),
		nil,
		[]Step{Read("B", "h"), Init("x")})
	mustAdd(t, s, "B",
		[]Import{imp("A", "x", "f")},
		cat(funcs("h"), lets("y")),
		nil,
		[]Step{Read("A", "f"), Init("y"), Read("A", "x")})
}

func TestCycleEntryA(t *testing.T) {
	s := NewSession()
	addCycleAB(t, s)
	res := mustEvalErr(t, s, "A", "TDZ A.x")
	if len(res.Appended) != 0 {
		t.Fatalf("Appended = %v，想要空", res.Appended)
	}
	mustOrder(t, s)
	mustState(t, s, "A", StateError)
	mustState(t, s, "B", StateError)
	stB, _ := s.Status("B")
	if stB.ErrValue != "TDZ A.x" {
		t.Fatalf("B 的错误值 = %q", stB.ErrValue)
	}
	// B 的体执行过（Init(y) 生效），A 的体未执行（x 未初始化）。
	stA, _ := s.Status("A")
	if stA.LetInit["x"] {
		t.Fatalf("A.x 不应已初始化")
	}
	if !stB.LetInit["y"] {
		t.Fatalf("B.y 应已初始化")
	}
	if s.modules["A"].bodyRuns != 0 || s.modules["B"].bodyRuns != 1 {
		t.Fatalf("体执行次数 A=%d B=%d", s.modules["A"].bodyRuns, s.modules["B"].bodyRuns)
	}
	checkInvariants(t, s)
}

func TestCycleEntryB(t *testing.T) {
	s := NewSession()
	addCycleAB(t, s)
	res := mustEvalOK(t, s, "B")
	if !reflect.DeepEqual(res.Appended, []string{"A", "B"}) {
		t.Fatalf("Appended = %v", res.Appended)
	}
	mustOrder(t, s, "A", "B")
	mustState(t, s, "A", StateEvaluated)
	mustState(t, s, "B", StateEvaluated)
	checkInvariants(t, s)
}

func TestFuncExportReadableInCycle(t *testing.T) {
	s := NewSession()
	mustAdd(t, s, "P", []Import{imp("Q", "g")}, funcs("f"), nil, []Step{Read("Q", "g")})
	mustAdd(t, s, "Q", []Import{imp("P", "f")}, funcs("g"), nil, []Step{Read("P", "f")})
	mustEvalOK(t, s, "P")
	mustOrder(t, s, "Q", "P")
	checkInvariants(t, s)
}

// ---- 错误粘滞与传播 ----

func TestDepErrorImporterSkipsBody(t *testing.T) {
	s := NewSession()
	addCycleAB(t, s)
	mustEvalErr(t, s, "A", "TDZ A.x")
	// C 导入已出错的 A：体不执行，继承原错误值。
	mustAdd(t, s, "C", []Import{imp("A", "x")}, lets("c"), nil, []Step{Init("c")})
	res := mustEvalErr(t, s, "C", "TDZ A.x")
	if len(res.Appended) != 0 {
		t.Fatalf("Appended = %v，想要空", res.Appended)
	}
	mustState(t, s, "C", StateError)
	stC, _ := s.Status("C")
	if stC.ErrValue != "TDZ A.x" {
		t.Fatalf("C 的错误值 = %q", stC.ErrValue)
	}
	if stC.LetInit["c"] {
		t.Fatalf("C.c 不应已初始化（体未执行）")
	}
	if s.modules["C"].bodyRuns != 0 {
		t.Fatalf("C 的体执行了 %d 次", s.modules["C"].bodyRuns)
	}
	mustOrder(t, s)
	checkInvariants(t, s)
}

func TestEarlierDepErrorSkipsLaterDeps(t *testing.T) {
	s := NewSession()
	mustAdd(t, s, "L1", nil, funcs("a"), nil, []Step{Throw("boom")})
	mustAdd(t, s, "L2", nil, funcs("b"), nil, nil)
	mustAdd(t, s, "R", []Import{imp("L1", "a"), imp("L2", "b")}, nil, nil, nil)
	mustEvalErr(t, s, "R", "boom")
	// 靠后的依赖 L2 未被求值，保持已链接。
	mustState(t, s, "L2", StateLinked)
	mustState(t, s, "L1", StateError)
	mustState(t, s, "R", StateError)
	mustOrder(t, s)
	// 之后从别的根可继续求值 L2。
	mustEvalOK(t, s, "L2")
	mustOrder(t, s, "L2")
	// 再从一个只依赖 L2 的新根继续。
	mustAdd(t, s, "R2", []Import{imp("L2", "b")}, nil, nil, nil)
	res := mustEvalOK(t, s, "R2")
	if !reflect.DeepEqual(res.Appended, []string{"R2"}) {
		t.Fatalf("Appended = %v", res.Appended)
	}
	mustOrder(t, s, "L2", "R2")
	checkInvariants(t, s)
}

func TestThrowMidBodyKeepsPriorInit(t *testing.T) {
	s := NewSession()
	mustAdd(t, s, "M", nil, lets("x", "y"), nil,
		[]Step{Init("x"), Throw("stop"), Init("y")})
	mustEvalErr(t, s, "M", "stop")
	st, _ := s.Status("M")
	if !st.LetInit["x"] || st.LetInit["y"] {
		t.Fatalf("LetInit = %v，想要 x=true y=false", st.LetInit)
	}
	// 已出错的根再次 Evaluate 不重做。
	before := capture(s)
	res := mustEvalErr(t, s, "M", "stop")
	if len(res.Appended) != 0 {
		t.Fatalf("Appended = %v", res.Appended)
	}
	mustSameSnapshot(t, "出错根再次 Evaluate", before, capture(s))
	if s.lastLinkVisited != 1 {
		t.Fatalf("lastLinkVisited = %d，想要 1", s.lastLinkVisited)
	}
	checkInvariants(t, s)
}

func TestEvaluatedRootReevaluateNoRedo(t *testing.T) {
	s := NewSession()
	mustAdd(t, s, "L", nil, lets("x"), nil, []Step{Init("x")})
	mustAdd(t, s, "R", []Import{imp("L", "x")}, nil, nil, nil)
	mustEvalOK(t, s, "R")
	before := capture(s)
	res := mustEvalOK(t, s, "R")
	if len(res.Appended) != 0 {
		t.Fatalf("Appended = %v，想要空", res.Appended)
	}
	mustSameSnapshot(t, "已求值根再次 Evaluate", before, capture(s))
	if s.lastLinkVisited != 1 {
		t.Fatalf("lastLinkVisited = %d，想要 1", s.lastLinkVisited)
	}
	if s.modules["L"].bodyRuns != 1 || s.modules["R"].bodyRuns != 1 {
		t.Fatalf("体被重复执行")
	}
	checkInvariants(t, s)
}

// ---- 拒绝的顺序与报告对象 ----

func TestEvaluateInvalidRootName(t *testing.T) {
	s := NewSession()
	mustReject(t, s, "", RejectInvalidArgument)
	mustReject(t, s, strings.Repeat("r", 33), RejectInvalidArgument)
	// 参数非法优先于模块不存在。
	if rej := mustReject(t, s, strings.Repeat("r", 33), RejectInvalidArgument); rej == nil {
		t.Fatalf("应报参数非法")
	}
}

func TestModuleNotFoundBeatsLinkError(t *testing.T) {
	s := NewSession()
	mustAdd(t, s, "L", nil, funcs("a"), nil, nil)
	// L 不导出 zz（链接错误），Z 未登记（模块不存在）：遍历先遇到 Z。
	mustAdd(t, s, "R", []Import{imp("L", "zz"), imp("Z", "a")}, nil, nil, nil)
	rej := mustReject(t, s, "R", RejectModuleNotFound)
	if rej.Name != "Z" {
		t.Fatalf("报告名字 = %q，想要 Z", rej.Name)
	}
	// 根未登记。
	rej = mustReject(t, s, "NOPE", RejectModuleNotFound)
	if rej.Name != "NOPE" {
		t.Fatalf("报告名字 = %q，想要 NOPE", rej.Name)
	}
}

func TestLinkErrorPreorderFirst(t *testing.T) {
	s := NewSession()
	mustAdd(t, s, "L1", nil, funcs("a"), nil, nil)
	mustAdd(t, s, "L2", nil, funcs("b"), nil, nil)
	// 同一模块两条导入都失败：按语句次序报第一个。
	mustAdd(t, s, "R", []Import{imp("L1", "bad1"), imp("L2", "bad2")}, nil, nil, nil)
	rej := mustReject(t, s, "R", RejectLinkError)
	if rej.Module != "R" || rej.Source != "L1" || rej.Name != "bad1" {
		t.Fatalf("报告 = (%s,%s,%s)，想要 (R,L1,bad1)", rej.Module, rej.Source, rej.Name)
	}
	// 先序：根的导入先检查，子模块的失败在根之后。
	mustAdd(t, s, "M", []Import{imp("L1", "bad1")}, funcs("x"), nil, nil)
	mustAdd(t, s, "R2", []Import{imp("M", "x"), imp("L2", "bad2")}, nil, nil, nil)
	rej = mustReject(t, s, "R2", RejectLinkError)
	if rej.Module != "R2" || rej.Source != "L2" || rej.Name != "bad2" {
		t.Fatalf("报告 = (%s,%s,%s)，想要 (R2,L2,bad2)", rej.Module, rej.Source, rej.Name)
	}
	// 根的导入全部通过后，报子模块（先序下一个）的失败。
	mustAdd(t, s, "R3", []Import{imp("M", "x")}, nil, nil, nil)
	rej = mustReject(t, s, "R3", RejectLinkError)
	if rej.Module != "M" || rej.Source != "L1" || rej.Name != "bad1" {
		t.Fatalf("报告 = (%s,%s,%s)，想要 (M,L1,bad1)", rej.Module, rej.Source, rej.Name)
	}
}

func TestRejectionsKeepState(t *testing.T) {
	s := NewSession()
	mustAdd(t, s, "F", nil, cat(funcs("f"), lets("x")), nil, []Step{Init("x")})
	mustAdd(t, s, "G", []Import{imp("F", "f")}, funcs("g"), nil, nil)
	mustEvalOK(t, s, "G") // F、G 已求值，函数位已置真
	mustAdd(t, s, "E1", nil, lets("v"), nil, nil)
	mustAdd(t, s, "E2", nil, lets("v"), nil, nil)
	mustAdd(t, s, "S", nil, nil, []Reexport{StarReexport("E1"), StarReexport("E2")}, nil)
	mustAdd(t, s, "T", []Import{imp("S", "v")}, nil, nil, nil)
	mustAdd(t, s, "BAD", []Import{imp("G", "nope")}, nil, nil, nil)
	mustAdd(t, s, "MISS", []Import{imp("ZZ", "a")}, nil, nil, nil)

	cases := []struct {
		what   string
		root   string
		reason RejectReason
	}{
		{"模块不存在", "MISS", RejectModuleNotFound},
		{"链接错误", "BAD", RejectLinkError},
		{"链接歧义", "T", RejectLinkAmbiguous},
	}
	for _, c := range cases {
		before := capture(s)
		mustReject(t, s, c.root, c.reason)
		mustSameSnapshot(t, c.what+"拒绝", before, capture(s))
	}
	// AddModule 的拒绝也不改状态。
	before := capture(s)
	if rej := s.AddModule("G", nil, nil, nil, nil); rej == nil || rej.Reason != RejectNameRegistered {
		t.Fatalf("重复登记应报名字已登记，得到 %v", rej)
	}
	if rej := s.AddModule("", nil, nil, nil, nil); rej == nil || rej.Reason != RejectInvalidArgument {
		t.Fatalf("空名应报参数非法，得到 %v", rej)
	}
	mustSameSnapshot(t, "AddModule 拒绝", before, capture(s))
	checkInvariants(t, s)
}

// ---- 星转出与具名转出 ----

func addE1E2(t *testing.T, s *Session) {
	mustAdd(t, s, "E1", nil, lets("v"), nil, []Step{Init("v")})
	mustAdd(t, s, "E2", nil, lets("v"), nil, []Step{Init("v")})
}

func TestStarAmbiguousDifferentBindings(t *testing.T) {
	s := NewSession()
	addE1E2(t, s)
	mustAdd(t, s, "S", nil, nil, []Reexport{StarReexport("E1"), StarReexport("E2")}, nil)
	mustAdd(t, s, "T", []Import{imp("S", "v")}, nil, nil, nil)
	rej := mustReject(t, s, "T", RejectLinkAmbiguous)
	if rej.Module != "T" || rej.Source != "S" || rej.Name != "v" {
		t.Fatalf("报告 = (%s,%s,%s)，想要 (T,S,v)", rej.Module, rej.Source, rej.Name)
	}
	mustState(t, s, "T", StateRegistered)
	mustState(t, s, "S", StateRegistered)
	mustState(t, s, "E1", StateRegistered)
	checkInvariants(t, s)
}

func TestStarSameBindingNotAmbiguous(t *testing.T) {
	s := NewSession()
	addE1E2(t, s)
	// 两个星转出指向同一来源：第二个 (E1,v) 已在解析集合内，返回未找到，不构成歧义。
	mustAdd(t, s, "U", nil, nil, []Reexport{StarReexport("E1"), StarReexport("E1")}, nil)
	mustAdd(t, s, "W", []Import{imp("U", "v")}, nil, nil, []Step{Read("U", "v")})
	mustEvalOK(t, s, "W")
	mustOrder(t, s, "E1", "U", "W")
	if s.lastResolveExpansions != s.lastResolveSetSize {
		t.Fatalf("展开次数 %d != 解析集合大小 %d", s.lastResolveExpansions, s.lastResolveSetSize)
	}
	checkInvariants(t, s)
}

func TestNamedBeatsStarAndDepsOrder(t *testing.T) {
	s := NewSession()
	addE1E2(t, s)
	mustAdd(t, s, "V", nil, nil, []Reexport{NamedReexport("v", "E2", "v"), StarReexport("E1")}, nil)
	mustAdd(t, s, "X", []Import{imp("V", "v")}, nil, nil, []Step{Read("V", "v")})
	mustEvalOK(t, s, "X")
	// 依赖列表次序为 E2、E1，求值次序 [E2, E1, V, X]。
	mustOrder(t, s, "E2", "E1", "V", "X")
	checkInvariants(t, s)
}

func TestStarDoesNotProvideOwnNames(t *testing.T) {
	s := NewSession()
	// E1 的 v 永不初始化：若解析到 (E1,v) 会 TDZ。
	mustAdd(t, s, "E1", nil, lets("v"), nil, nil)
	// G 本地定义 v 并星转出 E1：本地优先，星转出不提供本模块已定义的名字。
	mustAdd(t, s, "G", nil, lets("v"), []Reexport{StarReexport("E1")}, []Step{Init("v")})
	mustAdd(t, s, "H", []Import{imp("G", "v")}, nil, nil, []Step{Read("G", "v")})
	mustEvalOK(t, s, "H")
	mustOrder(t, s, "E1", "G", "H")
	checkInvariants(t, s)
}

func TestReexportRingsNotFound(t *testing.T) {
	s := NewSession()
	// 具名环。
	mustAdd(t, s, "C1", nil, nil, []Reexport{NamedReexport("a", "C2", "a")}, nil)
	mustAdd(t, s, "C2", nil, nil, []Reexport{NamedReexport("a", "C1", "a")}, nil)
	mustAdd(t, s, "D2", []Import{imp("C1", "a")}, nil, nil, nil)
	rej := mustReject(t, s, "D2", RejectLinkError)
	if rej.Module != "D2" || rej.Source != "C1" || rej.Name != "a" {
		t.Fatalf("报告 = (%s,%s,%s)，想要 (D2,C1,a)", rej.Module, rej.Source, rej.Name)
	}
	// 星环。
	mustAdd(t, s, "P1", nil, nil, []Reexport{StarReexport("P2")}, nil)
	mustAdd(t, s, "P2", nil, nil, []Reexport{StarReexport("P1")}, nil)
	mustAdd(t, s, "Q", []Import{imp("P1", "nope")}, nil, nil, nil)
	rej = mustReject(t, s, "Q", RejectLinkError)
	if rej.Module != "Q" || rej.Source != "P1" || rej.Name != "nope" {
		t.Fatalf("报告 = (%s,%s,%s)，想要 (Q,P1,nope)", rej.Module, rej.Source, rej.Name)
	}
	checkInvariants(t, s)
}

func TestAmbiguityPropagatesThroughNamed(t *testing.T) {
	s := NewSession()
	addE1E2(t, s)
	mustAdd(t, s, "H", nil, nil, []Reexport{StarReexport("E1"), StarReexport("E2")}, nil)
	mustAdd(t, s, "J", nil, nil, []Reexport{NamedReexport("v", "H", "v")}, nil)
	mustAdd(t, s, "K", []Import{imp("J", "v")}, nil, nil, nil)
	rej := mustReject(t, s, "K", RejectLinkAmbiguous)
	if rej.Module != "K" || rej.Source != "J" || rej.Name != "v" {
		t.Fatalf("报告 = (%s,%s,%s)，想要 (K,J,v)", rej.Module, rej.Source, rej.Name)
	}
	checkInvariants(t, s)
}

func TestNamedReexportCheckedAtLinkAfterImports(t *testing.T) {
	s := NewSession()
	mustAdd(t, s, "L", nil, funcs("a"), nil, nil)
	mustAdd(t, s, "L2", nil, funcs("good"), nil, nil)
	// 导入语句先于具名转出检查：报 (M, L, bad1)。
	mustAdd(t, s, "M",
		[]Import{imp("L", "bad1")},
		nil,
		[]Reexport{NamedReexport("r", "L2", "bad2")},
		nil)
	rej := mustReject(t, s, "M", RejectLinkError)
	if rej.Module != "M" || rej.Source != "L" || rej.Name != "bad1" {
		t.Fatalf("报告 = (%s,%s,%s)，想要 (M,L,bad1)", rej.Module, rej.Source, rej.Name)
	}
	// 具名转出自身的解析失败也在链接时报告（即使无人导入 r）。
	mustAdd(t, s, "M2", nil, nil, []Reexport{NamedReexport("r", "L2", "bad2")}, nil)
	rej = mustReject(t, s, "M2", RejectLinkError)
	if rej.Module != "M2" || rej.Source != "L2" || rej.Name != "bad2" {
		t.Fatalf("报告 = (%s,%s,%s)，想要 (M2,L2,bad2)", rej.Module, rej.Source, rej.Name)
	}
	// 具名转出解析成功则链接通过。
	mustAdd(t, s, "M3", nil, nil, []Reexport{NamedReexport("r", "L2", "good")}, nil)
	mustEvalOK(t, s, "M3")
	checkInvariants(t, s)
}

func TestTDZReportsDefiningModule(t *testing.T) {
	s := NewSession()
	mustAdd(t, s, "E1", nil, lets("v"), nil, nil) // v 永不初始化
	mustAdd(t, s, "S", nil, nil, []Reexport{NamedReexport("v", "E1", "v")}, nil)
	mustAdd(t, s, "M", []Import{imp("S", "v")}, nil, nil, []Step{Read("S", "v")})
	res := mustEvalErr(t, s, "M", "TDZ E1.v")
	if !reflect.DeepEqual(res.Appended, []string{"E1", "S"}) {
		t.Fatalf("Appended = %v，想要 [E1 S]", res.Appended)
	}
	// E1 与 S 的体为空，先求值完成；M 出错。
	mustOrder(t, s, "E1", "S")
	mustState(t, s, "M", StateError)
	checkInvariants(t, s)
}

// ---- AddModule 参数非法 ----

func TestAddModuleConflicts(t *testing.T) {
	cases := []struct {
		what      string
		name      string
		imports   []Import
		exports   []Export
		reexports []Reexport
		body      []Step
	}{
		{"空名字", "", nil, nil, nil, nil},
		{"超长名字", strings.Repeat("n", 33), nil, nil, nil, nil},
		{"自导入", "M", []Import{imp("M", "a")}, nil, nil, nil},
		{"导入名与本地导出重名", "M", []Import{imp("S", "a")}, lets("a"), nil, nil},
		{"导入名与具名转出重名", "M", []Import{imp("S", "a")}, nil, []Reexport{NamedReexport("a", "T", "b")}, nil},
		{"导入名重复（同语句）", "M", []Import{imp("S", "a", "a")}, nil, nil, nil},
		{"导入名重复（跨语句）", "M", []Import{imp("S", "a"), imp("T", "a")}, nil, nil, nil},
		{"本地导出重复", "M", nil, lets("a", "a"), nil, nil},
		{"具名转出与本地导出重名", "M", nil, lets("a"), []Reexport{NamedReexport("a", "S", "b")}, nil},
		{"具名转出重复", "M", nil, nil, []Reexport{NamedReexport("a", "S", "b"), NamedReexport("a", "T", "c")}, nil},
		{"具名转出来源是自身", "M", nil, nil, []Reexport{NamedReexport("a", "M", "b")}, nil},
		{"星转出来源是自身", "M", nil, nil, []Reexport{StarReexport("M")}, nil},
		{"空导入名", "M", []Import{imp("S", "")}, nil, nil, nil},
		{"空导入语句", "M", []Import{imp("S")}, nil, nil, nil},
		{"Init 非 let 导出", "M", nil, funcs("f"), nil, []Step{Init("f")}},
		{"Init 未导出名字", "M", nil, lets("x"), nil, []Step{Init("y")}},
		{"Read 自身非本地导出", "M", nil, lets("x"), nil, []Step{Read("M", "y")}},
		{"Read 未导入的来源", "M", nil, nil, nil, []Step{Read("S", "a")}},
		{"Read 未导入的名字", "M", []Import{imp("S", "a")}, nil, nil, []Step{Read("S", "b")}},
		{"Throw 空消息", "M", nil, nil, nil, []Step{Throw("")}},
		{"Throw 超长消息", "M", nil, nil, nil, []Step{Throw(strings.Repeat("m", 65))}},
	}
	for _, c := range cases {
		s := NewSession()
		rej := s.AddModule(c.name, c.imports, c.exports, c.reexports, c.body)
		if rej == nil || rej.Reason != RejectInvalidArgument {
			t.Fatalf("%s: 拒绝 = %v，想要参数非法", c.what, rej)
		}
		if _, rej := s.Status(c.name); c.name != "" && len(c.name) <= 32 && (rej == nil || rej.Reason != RejectModuleNotFound) {
			t.Fatalf("%s: 被拒绝的模块不应被登记", c.what)
		}
	}
}

func TestLimitsBoundary(t *testing.T) {
	// 名字长度：32 允许，33 拒绝。
	s := NewSession()
	if rej := s.AddModule(strings.Repeat("n", 32), nil, nil, nil, nil); rej != nil {
		t.Fatalf("32 字节名字应允许: %v", rej)
	}
	if rej := s.AddModule(strings.Repeat("n", 33), nil, nil, nil, nil); rej == nil {
		t.Fatalf("33 字节名字应拒绝")
	}

	// 导入语句数：20 允许，21 拒绝。
	mkImports := func(n int) []Import {
		var out []Import
		for i := 0; i < n; i++ {
			out = append(out, imp("S", "n"+strings.Repeat("x", i%5)+itoa(i)))
		}
		return out
	}
	s = NewSession()
	if rej := s.AddModule("M", mkImports(20), nil, nil, nil); rej != nil {
		t.Fatalf("20 条导入应允许: %v", rej)
	}
	if rej := s.AddModule("M2", mkImports(21), nil, nil, nil); rej == nil {
		t.Fatalf("21 条导入应拒绝")
	}

	// 每条语句名字数：10 允许，11 拒绝。
	s = NewSession()
	if rej := s.AddModule("M", []Import{imp("S", names(10)...)}, nil, nil, nil); rej != nil {
		t.Fatalf("10 个名字应允许: %v", rej)
	}
	if rej := s.AddModule("M2", []Import{imp("S", names(11)...)}, nil, nil, nil); rej == nil {
		t.Fatalf("11 个名字应拒绝")
	}

	// 本地导出数：20 允许，21 拒绝。
	s = NewSession()
	if rej := s.AddModule("M", nil, lets(names(20)...), nil, nil); rej != nil {
		t.Fatalf("20 个导出应允许: %v", rej)
	}
	if rej := s.AddModule("M2", nil, lets(names(21)...), nil, nil); rej == nil {
		t.Fatalf("21 个导出应拒绝")
	}

	// 转出项数：20 允许，21 拒绝。
	mkStars := func(n int) []Reexport {
		var out []Reexport
		for i := 0; i < n; i++ {
			out = append(out, StarReexport("S"))
		}
		return out
	}
	s = NewSession()
	if rej := s.AddModule("M", nil, nil, mkStars(20), nil); rej != nil {
		t.Fatalf("20 个转出项应允许: %v", rej)
	}
	if rej := s.AddModule("M2", nil, nil, mkStars(21), nil); rej == nil {
		t.Fatalf("21 个转出项应拒绝")
	}

	// 体步骤数：50 允许，51 拒绝。
	mkSteps := func(n int) []Step {
		var out []Step
		for i := 0; i < n; i++ {
			out = append(out, Read("M", "x"))
		}
		return out
	}
	s = NewSession()
	if rej := s.AddModule("M", nil, lets("x"), nil, mkSteps(50)); rej != nil {
		t.Fatalf("50 步应允许: %v", rej)
	}
	if rej := s.AddModule("M2", nil, lets("x"), nil, mkSteps(51)); rej == nil {
		t.Fatalf("51 步应拒绝")
	}

	// Throw 消息：64 允许，65 拒绝。
	s = NewSession()
	if rej := s.AddModule("M", nil, nil, nil, []Step{Throw(strings.Repeat("m", 64))}); rej != nil {
		t.Fatalf("64 字节消息应允许: %v", rej)
	}
	if rej := s.AddModule("M2", nil, nil, nil, []Step{Throw(strings.Repeat("m", 65))}); rej == nil {
		t.Fatalf("65 字节消息应拒绝")
	}

	// 模块数：100 允许，第 101 个报模块数超限；已登记名字优先报已登记。
	s = NewSession()
	for i := 0; i < 100; i++ {
		if rej := s.AddModule("mod"+itoa(i), nil, nil, nil, nil); rej != nil {
			t.Fatalf("第 %d 个模块应允许: %v", i+1, rej)
		}
	}
	if rej := s.AddModule("mod101", nil, nil, nil, nil); rej == nil || rej.Reason != RejectTooManyModules {
		t.Fatalf("第 101 个模块应报模块数超限，得到 %v", rej)
	}
	if rej := s.AddModule("mod0", nil, nil, nil, nil); rej == nil || rej.Reason != RejectNameRegistered {
		t.Fatalf("已登记名字应优先报名字已登记，得到 %v", rej)
	}
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var buf [8]byte
	p := len(buf)
	for i > 0 {
		p--
		buf[p] = byte('0' + i%10)
		i /= 10
	}
	return string(buf[p:])
}

func names(n int) []string {
	var out []string
	for i := 0; i < n; i++ {
		out = append(out, "n"+itoa(i))
	}
	return out
}

// ---- 查询 ----

func TestStatusAndOrderQueries(t *testing.T) {
	s := NewSession()
	if got := s.Order(); len(got) != 0 {
		t.Fatalf("初始 Order = %v，想要空", got)
	}
	if _, rej := s.Status("NOPE"); rej == nil || rej.Reason != RejectModuleNotFound {
		t.Fatalf("未登记名字应报不存在，得到 %v", rej)
	}
	mustAdd(t, s, "M", nil, cat(lets("x", "y"), funcs("f")), nil, []Step{Init("x")})
	st, rej := s.Status("M")
	if rej != nil || st.State != StateRegistered {
		t.Fatalf("Status = (%v, %v)", st.State, rej)
	}
	if len(st.LetInit) != 2 || st.LetInit["x"] || st.LetInit["y"] {
		t.Fatalf("LetInit = %v", st.LetInit)
	}
	mustEvalOK(t, s, "M")
	st, _ = s.Status("M")
	if st.State != StateEvaluated || !st.LetInit["x"] || st.LetInit["y"] {
		t.Fatalf("Status = (%v, %v)", st.State, st.LetInit)
	}
	// 出错模块的状态带错误值。
	mustAdd(t, s, "B", nil, nil, nil, []Step{Throw("bad")})
	mustEvalErr(t, s, "B", "bad")
	st, _ = s.Status("B")
	if st.State != StateError || st.ErrValue != "bad" {
		t.Fatalf("Status = (%v, %q)", st.State, st.ErrValue)
	}
}

// ---- 计数器不变量 ----

func TestResolveCounterInvariant(t *testing.T) {
	s := NewSession()
	addE1E2(t, s)
	mustAdd(t, s, "U", nil, nil, []Reexport{StarReexport("E1"), StarReexport("E1")}, nil)
	mustAdd(t, s, "W", []Import{imp("U", "v")}, nil, nil, []Step{Read("U", "v")})
	mustEvalOK(t, s, "W")
	if s.lastResolveExpansions != s.lastResolveSetSize {
		t.Fatalf("展开次数 %d != 解析集合大小 %d", s.lastResolveExpansions, s.lastResolveSetSize)
	}
	// 具名环：每个 (模块, 名字) 对至多展开一次。
	s2 := NewSession()
	mustAdd(t, s2, "C1", nil, nil, []Reexport{NamedReexport("a", "C2", "a")}, nil)
	mustAdd(t, s2, "C2", nil, nil, []Reexport{NamedReexport("a", "C1", "a")}, nil)
	mustAdd(t, s2, "D2", []Import{imp("C1", "a")}, nil, nil, nil)
	mustReject(t, s2, "D2", RejectLinkError)
	if s2.lastResolveExpansions != s2.lastResolveSetSize {
		t.Fatalf("展开次数 %d != 解析集合大小 %d", s2.lastResolveExpansions, s2.lastResolveSetSize)
	}
	if s2.lastResolveSetSize != 2 { // (C1,a) 与 (C2,a) 各展开一次
		t.Fatalf("解析集合大小 = %d，想要 2", s2.lastResolveSetSize)
	}
}

func TestLinkVisitedBounds(t *testing.T) {
	s := NewSession()
	mustAdd(t, s, "A", nil, funcs("a"), nil, nil)
	mustAdd(t, s, "B", []Import{imp("A", "a")}, funcs("b"), nil, nil)
	// 先链接并求值 B（A、B 进入已求值）。
	mustEvalOK(t, s, "B")
	// 新根 R 依赖已求值的 B 与新登记的 C、D。
	mustAdd(t, s, "C", nil, funcs("c"), nil, nil)
	mustAdd(t, s, "D", []Import{imp("C", "c")}, funcs("d"), nil, nil)
	mustAdd(t, s, "R", []Import{imp("B", "b"), imp("D", "d")}, nil, nil, nil)
	before := capture(s)
	mustEvalOK(t, s, "R")
	newly := 0
	for name, st := range before.states {
		if st == StateRegistered && s.modules[name].state != StateRegistered {
			newly++
		}
	}
	if newly != 3 { // C、D、R
		t.Fatalf("新链接模块数 = %d，想要 3", newly)
	}
	if s.lastLinkVisited != newly {
		t.Fatalf("lastLinkVisited = %d，想要 %d", s.lastLinkVisited, newly)
	}
	if s.lastLinkVisited > newly+1 {
		t.Fatalf("lastLinkVisited %d 超过新链接数 %d + 1", s.lastLinkVisited, newly)
	}
	// 已求值的根再次 Evaluate：访问数为 1。
	mustEvalOK(t, s, "R")
	if s.lastLinkVisited != 1 {
		t.Fatalf("lastLinkVisited = %d，想要 1", s.lastLinkVisited)
	}
	checkInvariants(t, s)
}

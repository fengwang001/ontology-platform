package linker

import (
	"errors"
	"reflect"
	"testing"
)

func mustAdd(t *testing.T, s *Session, d ModuleDef) {
	t.Helper()
	if err := s.AddModule(d); err != nil {
		t.Fatalf("AddModule(%s): %v", d.Name, err)
	}
}

func evalMust(t *testing.T, s *Session, root string) EvalResult {
	t.Helper()
	r, err := s.Evaluate(root)
	if err != nil {
		t.Fatalf("Evaluate(%s) rejected: %v", root, err)
	}
	return r
}

func statusOf(t *testing.T, s *Session, name string) StatusResult {
	t.Helper()
	st, err := s.Status(name)
	if err != nil {
		t.Fatalf("Status(%s): %v", name, err)
	}
	return st
}

func starX(source string) ReExport { return ReExport{Source: source} }

func namedX(name, source, from string) ReExport {
	return ReExport{Name: name, Source: source, From: from}
}

// 题目例 1：Evaluate(A) 时 B 读到 A.x 的 TDZ。
func TestExampleCycleTDZ(t *testing.T) {
	s := NewSession()
	mustAdd(t, s, ModuleDef{
		Name:    "A",
		Imports: []ImportStmt{{Source: "B", Bindings: []string{"h"}}},
		Exports: []Export{{"x", KindLet}, {"f", KindFunction}},
		Body:    []Step{Read("B", "h"), Init("x")},
	})
	mustAdd(t, s, ModuleDef{
		Name:    "B",
		Imports: []ImportStmt{{Source: "A", Bindings: []string{"x", "f"}}},
		Exports: []Export{{"h", KindFunction}, {"y", KindLet}},
		Body:    []Step{Read("A", "f"), Init("y"), Read("A", "x")},
	})
	r := evalMust(t, s, "A")
	if r.OK || r.Error != "TDZ A.x" || len(r.Appended) != 0 {
		t.Fatalf("got %+v", r)
	}
	if got := statusOf(t, s, "B"); got.Status != StatusErrored || got.Error != "TDZ A.x" || !got.Lets["y"] {
		t.Fatalf("B status=%+v", got)
	}
	if got := statusOf(t, s, "A"); got.Status != StatusErrored || got.Lets["x"] {
		t.Fatalf("A status=%+v", got)
	}
	// 再次 Evaluate 不重做、不遍历，粘滞错误。
	r2 := evalMust(t, s, "A")
	if r2.OK || r2.Error != "TDZ A.x" || s.lastLinkVisitCount != 1 || s.lastEvalVisitCount != 1 {
		t.Fatalf("re-eval got %+v visits=(%d,%d)", r2, s.lastLinkVisitCount, s.lastEvalVisitCount)
	}
}

// 题目例 2：新会话 Evaluate(B) 入口不同，次序 [A,B] 成功。
func TestExampleCycleOtherEntry(t *testing.T) {
	s := NewSession()
	mustAdd(t, s, ModuleDef{
		Name:    "A",
		Imports: []ImportStmt{{Source: "B", Bindings: []string{"h"}}},
		Exports: []Export{{"x", KindLet}, {"f", KindFunction}},
		Body:    []Step{Read("B", "h"), Init("x")},
	})
	mustAdd(t, s, ModuleDef{
		Name:    "B",
		Imports: []ImportStmt{{Source: "A", Bindings: []string{"x", "f"}}},
		Exports: []Export{{"h", KindFunction}, {"y", KindLet}},
		Body:    []Step{Read("A", "f"), Init("y"), Read("A", "x")},
	})
	r := evalMust(t, s, "B")
	if !r.OK || !reflect.DeepEqual(r.Appended, []string{"A", "B"}) {
		t.Fatalf("got %+v", r)
	}
}

// 无环：后序次序由导入语句次序决定；同一来源重复出现只处理一次。
func TestPostorderAndStatementOrder(t *testing.T) {
	s := NewSession()
	mustAdd(t, s, ModuleDef{Name: "L1", Exports: []Export{{"g", KindFunction}, {"h", KindFunction}}})
	mustAdd(t, s, ModuleDef{Name: "L2", Exports: []Export{{"k", KindFunction}}})
	mustAdd(t, s, ModuleDef{
		Name: "M",
		Imports: []ImportStmt{
			{Source: "L1", Bindings: []string{"g", "h"}},
			{Source: "L2", Bindings: []string{"k"}},
		},
		Exports: []Export{{"m", KindFunction}},
	})
	r := evalMust(t, s, "M")
	if !r.OK || !reflect.DeepEqual(r.Appended, []string{"L1", "L2", "M"}) {
		t.Fatalf("got %+v", r)
	}
}

// 依赖出错：导入者体不执行并继承原错误值；靠前依赖出错时靠后依赖保持已链接；
// 之后从别的根可继续求值。
func TestErrorStickyAndResume(t *testing.T) {
	s := NewSession()
	mustAdd(t, s, ModuleDef{
		Name:    "Bad",
		Exports: []Export{{"x", KindLet}},
		Body:    []Step{Throw("boom")},
	})
	mustAdd(t, s, ModuleDef{Name: "Later", Exports: []Export{{"g", KindFunction}}})
	mustAdd(t, s, ModuleDef{
		Name: "Mid",
		Imports: []ImportStmt{
			{Source: "Bad", Bindings: []string{"x"}},
			{Source: "Later", Bindings: []string{"g"}},
		},
		Exports: []Export{{"m", KindFunction}},
		Body:    []Step{Read("Bad", "x")},
	})
	r := evalMust(t, s, "Mid")
	if r.OK || r.Error != "boom" || len(r.Appended) != 0 {
		t.Fatalf("got %+v", r)
	}
	if got := statusOf(t, s, "Mid"); got.Status != StatusErrored || got.Error != "boom" {
		t.Fatalf("Mid: %+v", got)
	}
	if got := statusOf(t, s, "Later"); got.Status != StatusLinked {
		t.Fatalf("Later should remain linked, got %v", got.Status)
	}
	r2 := evalMust(t, s, "Later")
	if !r2.OK || !reflect.DeepEqual(r2.Appended, []string{"Later"}) {
		t.Fatalf("Later eval: %+v", r2)
	}
	r3 := evalMust(t, s, "Mid")
	if r3.OK || r3.Error != "boom" {
		t.Fatalf("Mid retry: %+v", r3)
	}
}

// Throw 在体中途抛出：后续步骤不执行，此前 Init 保留；体只执行一次。
func TestThrowKeepsPriorInit(t *testing.T) {
	s := NewSession()
	mustAdd(t, s, ModuleDef{
		Name:    "M",
		Exports: []Export{{"a", KindLet}, {"b", KindLet}},
		Body:    []Step{Init("a"), Throw("x"), Init("b")},
	})
	r := evalMust(t, s, "M")
	if r.OK || r.Error != "x" {
		t.Fatalf("got %+v", r)
	}
	st := statusOf(t, s, "M")
	if !st.Lets["a"] || st.Lets["b"] {
		t.Fatalf("lets=%+v", st.Lets)
	}
	if s.modules["M"].bodyRuns != 1 {
		t.Fatalf("bodyRuns=%d", s.modules["M"].bodyRuns)
	}
	_ = evalMust(t, s, "M")
	if s.modules["M"].bodyRuns != 1 {
		t.Fatalf("bodyRuns after retry=%d", s.modules["M"].bodyRuns)
	}
}

// 已求值的根再次 Evaluate：不重做，访问模块数为 1。
func TestEvaluatedRootNoRedo(t *testing.T) {
	s := NewSession()
	mustAdd(t, s, ModuleDef{Name: "D", Exports: []Export{{"g", KindFunction}}})
	mustAdd(t, s, ModuleDef{
		Name:    "R",
		Imports: []ImportStmt{{Source: "D", Bindings: []string{"g"}}},
		Exports: []Export{{"r", KindFunction}},
	})
	r1 := evalMust(t, s, "R")
	if !r1.OK || !reflect.DeepEqual(r1.Appended, []string{"D", "R"}) {
		t.Fatalf("first %+v", r1)
	}
	r2 := evalMust(t, s, "R")
	if !r2.OK || len(r2.Appended) != 0 || s.lastLinkVisitCount != 1 || s.lastEvalVisitCount != 1 {
		t.Fatalf("second %+v visits=%d,%d", r2, s.lastLinkVisitCount, s.lastEvalVisitCount)
	}
}

// 链接阶段：根不存在、来源不存在、根名非法；拒绝不改状态。
func TestLinkMissingModule(t *testing.T) {
	s := NewSession()
	mustAdd(t, s, ModuleDef{
		Name:    "Root",
		Imports: []ImportStmt{{Source: "Ghost", Bindings: []string{"z"}}},
	})
	if _, err := s.Evaluate("Root"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
	if got := statusOf(t, s, "Root"); got.Status != StatusRegistered {
		t.Fatalf("Root status=%v", got.Status)
	}
	if _, err := s.Evaluate("Nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing root: %v", err)
	}
	if _, err := s.Evaluate(""); !errors.Is(err, ErrInvalidArg) {
		t.Fatalf("empty root: %v", err)
	}
	if _, err := s.Status("Ghost"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("status missing: %v", err)
	}
}

// 先序第一个缺失来源：进入 B 后 B 的 X 缺失，先于 A 的 Y 报告。
func TestLinkMissingFirstPreorder(t *testing.T) {
	s := NewSession()
	mustAdd(t, s, ModuleDef{
		Name: "A",
		Imports: []ImportStmt{
			{Source: "B", Bindings: []string{"g"}},
			{Source: "Y", Bindings: []string{"gy"}},
		},
	})
	mustAdd(t, s, ModuleDef{
		Name:    "B",
		Imports: []ImportStmt{{Source: "X", Bindings: []string{"g"}}},
		Exports: []Export{{"b", KindFunction}},
	})
	if _, err := s.Evaluate("A"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("want not found X, got %v", err)
	}
}

// 遍历缺来源先于解析失败；补齐后报 (A,B,zz) 链接错误。
func TestLinkErrorNotFoundOrdering(t *testing.T) {
	s := NewSession()
	mustAdd(t, s, ModuleDef{
		Name: "A",
		Imports: []ImportStmt{
			{Source: "B", Bindings: []string{"zz"}},
			{Source: "C", Bindings: []string{"g"}},
		},
	})
	mustAdd(t, s, ModuleDef{Name: "B", Exports: []Export{{"g", KindFunction}}})
	if _, err := s.Evaluate("A"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound(C), got %v", err)
	}
	mustAdd(t, s, ModuleDef{Name: "C", Exports: []Export{{"g", KindFunction}}})
	_, err := s.Evaluate("A")
	le, ok := AsLinkError(err)
	if !ok || le.Ambiguous || le.Module != "A" || le.Source != "B" || le.Name != "zz" {
		t.Fatalf("want link error (A,B,zz), got %v", err)
	}
}

// 三类拒绝后模块仍为已登记、函数位保持假。
func TestRejectionKeepsState(t *testing.T) {
	s := NewSession()
	mustAdd(t, s, ModuleDef{Name: "B", Exports: []Export{{"g", KindFunction}}})
	mustAdd(t, s, ModuleDef{
		Name:    "A",
		Imports: []ImportStmt{{Source: "B", Bindings: []string{"zz"}}},
		Exports: []Export{{"f", KindFunction}},
	})
	_, _ = s.Evaluate("A")
	if statusOf(t, s, "A").Status != StatusRegistered {
		t.Fatalf("A status changed")
	}
	if s.modules["A"].initialized["f"] {
		t.Fatalf("function bit must remain false after rejection")
	}
}

// 登记参数非法：自导入、导入名冲突等。
func TestAddModuleValidation(t *testing.T) {
	s := NewSession()
	cases := []struct {
		label string
		def   ModuleDef
	}{
		{"empty name", ModuleDef{Name: "", Exports: []Export{{"f", KindFunction}}}},
		{"name too long", ModuleDef{Name: string(make([]byte, maxNameLen+1))}},
		{"self import", ModuleDef{Name: "M", Imports: []ImportStmt{{Source: "M", Bindings: []string{"x"}}}}},
		{"dup import", ModuleDef{Name: "M", Imports: []ImportStmt{{Source: "S", Bindings: []string{"x", "x"}}}}},
		{"import collides", ModuleDef{Name: "M", Imports: []ImportStmt{{Source: "S", Bindings: []string{"f"}}}, Exports: []Export{{"f", KindLet}}}},
		{"dup local export", ModuleDef{Name: "M", Exports: []Export{{"f", KindLet}, {"f", KindFunction}}}},
		{"self reexport", ModuleDef{Name: "M", ReExports: []ReExport{namedX("v", "M", "v")}, Exports: []Export{{"f", KindFunction}}}},
		{"named collides", ModuleDef{Name: "M", Exports: []Export{{"v", KindLet}}, ReExports: []ReExport{namedX("v", "S", "v")}}},
		{"dup named", ModuleDef{Name: "M", ReExports: []ReExport{namedX("v", "S", "v"), namedX("v", "T", "w")}, Exports: []Export{{"f", KindFunction}}}},
		{"import named clash", ModuleDef{Name: "M", Imports: []ImportStmt{{Source: "S", Bindings: []string{"v"}}}, ReExports: []ReExport{namedX("v", "T", "v")}, Exports: []Export{{"f", KindFunction}}}},
		{"init non-let", ModuleDef{Name: "M", Exports: []Export{{"f", KindFunction}}, Body: []Step{Init("f")}}},
		{"init missing", ModuleDef{Name: "M", Exports: []Export{{"f", KindLet}}, Body: []Step{Init("z")}}},
		{"read unknown src", ModuleDef{Name: "M", Exports: []Export{{"f", KindLet}}, Body: []Step{Read("Z", "f")}}},
		{"read unknown name", ModuleDef{Name: "M", Imports: []ImportStmt{{Source: "Z", Bindings: []string{"a"}}}, Exports: []Export{{"f", KindLet}}, Body: []Step{Read("Z", "b")}}},
		{"throw empty", ModuleDef{Name: "M", Body: []Step{Throw("")}}},
	}
	for _, c := range cases {
		label, d := c.label, c.def
		if err := s.AddModule(d); !errors.Is(err, ErrInvalidArg) {
			t.Fatalf("%s: want ErrInvalidArg, got %v", label, err)
		}
	}
	// 星转出带 From 非法。
	if err := s.AddModule(ModuleDef{Name: "M", ReExports: []ReExport{{Source: "S", From: "x"}}}); !errors.Is(err, ErrInvalidArg) {
		t.Fatalf("star with From: %v", err)
	}
	// 名字已登记、模块数超限（用极小场景间接验证优先级，超限需要构造 100 个，另测）。
	mustAdd(t, s, ModuleDef{Name: "Dup", Exports: []Export{{"f", KindFunction}}})
	if err := s.AddModule(ModuleDef{Name: "Dup"}); !errors.Is(err, ErrNameExists) {
		t.Fatalf("dup name: %v", err)
	}
}

// 星转出两来源不同绑定为歧义。
func TestStarAmbiguity(t *testing.T) {
	s := NewSession()
	mustAdd(t, s, ModuleDef{Name: "E1", Exports: []Export{{"v", KindLet}}, Body: []Step{Init("v")}})
	mustAdd(t, s, ModuleDef{Name: "E2", Exports: []Export{{"v", KindLet}}, Body: []Step{Init("v")}})
	mustAdd(t, s, ModuleDef{Name: "S", ReExports: []ReExport{starX("E1"), starX("E2")}})
	mustAdd(t, s, ModuleDef{Name: "T", Imports: []ImportStmt{{Source: "S", Bindings: []string{"v"}}}})
	_, err := s.Evaluate("T")
	le, ok := AsLinkError(err)
	if !ok || !le.Ambiguous || le.Module != "T" || le.Source != "S" || le.Name != "v" {
		t.Fatalf("want ambiguity (T,S,v), got %v", err)
	}
	for _, n := range []string{"T", "S", "E1", "E2"} {
		if statusOf(t, s, n).Status != StatusRegistered {
			t.Fatalf("%s changed status", n)
		}
	}
}

// 星 E1、星 E1：解析集合使第二次为未找到，不构成歧义，次序 [E1,U,W]。
func TestStarSameBindingResolutionSet(t *testing.T) {
	s := NewSession()
	mustAdd(t, s, ModuleDef{Name: "E1", Exports: []Export{{"v", KindLet}}, Body: []Step{Init("v")}})
	mustAdd(t, s, ModuleDef{Name: "U", ReExports: []ReExport{starX("E1"), starX("E1")}})
	mustAdd(t, s, ModuleDef{
		Name:    "W",
		Imports: []ImportStmt{{Source: "U", Bindings: []string{"v"}}},
		Body:    []Step{Read("U", "v")},
	})
	r := evalMust(t, s, "W")
	if !r.OK || !reflect.DeepEqual(r.Appended, []string{"E1", "U", "W"}) {
		t.Fatalf("got %+v order=%v", r, s.Order())
	}
}

// 具名转出优先于星转出；依赖列表次序含两个转出来源：[E2,E1,V,X]。
func TestNamedReExportPriority(t *testing.T) {
	s := NewSession()
	mustAdd(t, s, ModuleDef{Name: "E1", Exports: []Export{{"v", KindLet}}, Body: []Step{Init("v")}})
	mustAdd(t, s, ModuleDef{Name: "E2", Exports: []Export{{"v", KindLet}}, Body: []Step{Init("v")}})
	mustAdd(t, s, ModuleDef{Name: "V", ReExports: []ReExport{namedX("v", "E2", "v"), starX("E1")}})
	mustAdd(t, s, ModuleDef{
		Name:    "X",
		Imports: []ImportStmt{{Source: "V", Bindings: []string{"v"}}},
		Body:    []Step{Read("V", "v")},
	})
	r := evalMust(t, s, "X")
	if !r.OK || !reflect.DeepEqual(r.Appended, []string{"E2", "E1", "V", "X"}) {
		t.Fatalf("got %+v order=%v", r, s.Order())
	}
}

// 具名转出环 -> 回到已在集合内的对，未找到 -> 链接错误。
func TestNamedReExportCycle(t *testing.T) {
	s := NewSession()
	mustAdd(t, s, ModuleDef{Name: "C1", ReExports: []ReExport{namedX("a", "C2", "a")}})
	mustAdd(t, s, ModuleDef{Name: "C2", ReExports: []ReExport{namedX("a", "C1", "a")}})
	mustAdd(t, s, ModuleDef{Name: "D2", Imports: []ImportStmt{{Source: "C1", Bindings: []string{"a"}}}})
	_, err := s.Evaluate("D2")
	le, ok := AsLinkError(err)
	if !ok || le.Ambiguous || le.Module != "D2" || le.Source != "C1" || le.Name != "a" {
		t.Fatalf("want link error (D2,C1,a), got %v", err)
	}
}

// 星转出环也得未找到（集合命中）。
func TestStarCycleNotFound(t *testing.T) {
	s := NewSession()
	mustAdd(t, s, ModuleDef{Name: "P", ReExports: []ReExport{starX("Q")}})
	mustAdd(t, s, ModuleDef{Name: "Q", ReExports: []ReExport{starX("P")}})
	mustAdd(t, s, ModuleDef{Name: "R", Imports: []ImportStmt{{Source: "P", Bindings: []string{"z"}}}})
	_, err := s.Evaluate("R")
	le, ok := AsLinkError(err)
	if !ok || le.Ambiguous || le.Name != "z" {
		t.Fatalf("want link error z, got %v", err)
	}
}

// 歧义沿具名转出向上传播。
func TestAmbiguityPropagatesThroughNamed(t *testing.T) {
	s := NewSession()
	mustAdd(t, s, ModuleDef{Name: "E1", Exports: []Export{{"v", KindLet}}})
	mustAdd(t, s, ModuleDef{Name: "E2", Exports: []Export{{"v", KindLet}}})
	mustAdd(t, s, ModuleDef{Name: "S", ReExports: []ReExport{starX("E1"), starX("E2")}})
	mustAdd(t, s, ModuleDef{Name: "W", ReExports: []ReExport{namedX("v", "S", "v")}})
	mustAdd(t, s, ModuleDef{Name: "R", Imports: []ImportStmt{{Source: "W", Bindings: []string{"v"}}}})
	_, err := s.Evaluate("R")
	le, ok := AsLinkError(err)
	if !ok || !le.Ambiguous || le.Module != "R" || le.Source != "W" || le.Name != "v" {
		t.Fatalf("want propagated ambiguity, got %v", err)
	}
}

// 具名转出自身解析失败也在链接时报告，且在导入语句之后检查。
func TestNamedReExportCheckedAfterImports(t *testing.T) {
	s := NewSession()
	mustAdd(t, s, ModuleDef{Name: "S", Exports: []Export{{"ok", KindFunction}}})
	// M 先有一条会失败的导入 (S,bad)，再有一条失败的具名转出 (v,S,gone)：
	// 导入语句先检查，因此报 (M,S,bad)。
	mustAdd(t, s, ModuleDef{
		Name: "M",
		Imports: []ImportStmt{
			{Source: "S", Bindings: []string{"bad"}},
		},
		ReExports: []ReExport{namedX("v", "S", "gone")},
	})
	_, err := s.Evaluate("M")
	le, ok := AsLinkError(err)
	if !ok || le.Ambiguous || le.Name != "bad" || le.Source != "S" {
		t.Fatalf("imports first, got %v", err)
	}
}

// 本地导出优先于星转出：星不提供本模块已定义的名字。
func TestLocalShadowsStar(t *testing.T) {
	s := NewSession()
	mustAdd(t, s, ModuleDef{Name: "E1", Exports: []Export{{"v", KindLet}, {"w", KindLet}}})
	mustAdd(t, s, ModuleDef{Name: "M", Exports: []Export{{"v", KindFunction}}, ReExports: []ReExport{starX("E1")}})
	mustAdd(t, s, ModuleDef{
		Name:    "R",
		Imports: []ImportStmt{{Source: "M", Bindings: []string{"v", "w"}}},
		Body:    []Step{Read("M", "v")},
	})
	r := evalMust(t, s, "R")
	if !r.OK {
		t.Fatalf("got %+v", r)
	}
	// v 解析到本地 (M,v)；w 来自星 (E1,w)。
	if b := s.modules["R"].importBinding["v"]; b != (binding{module: "M", name: "v"}) {
		t.Fatalf("v binding=%+v", b)
	}
	if b := s.modules["R"].importBinding["w"]; b != (binding{module: "E1", name: "w"}) {
		t.Fatalf("w binding=%+v", b)
	}
}

// Read 经转出引入名时 TDZ 报告定义模块名。
func TestTDZReportsDefiningModule(t *testing.T) {
	s := NewSession()
	mustAdd(t, s, ModuleDef{Name: "E", Exports: []Export{{"v", KindLet}}}) // 不 Init
	mustAdd(t, s, ModuleDef{Name: "M", ReExports: []ReExport{namedX("z", "E", "v")}})
	mustAdd(t, s, ModuleDef{
		Name:    "R",
		Imports: []ImportStmt{{Source: "M", Bindings: []string{"z"}}},
		Body:    []Step{Read("M", "z")},
	})
	r := evalMust(t, s, "R")
	if r.OK || r.Error != "TDZ E.v" {
		t.Fatalf("got %+v", r)
	}
}

// 上限恰好达到允许；超一被拒。
func TestLimitsBoundary(t *testing.T) {
	s := NewSession()
	bigName := string(make([]byte, maxNameLen))
	mkSteps := func(n int) []Step {
		out := make([]Step, n)
		for i := range out {
			out[i] = Throw("e")
		}
		return out
	}
	if err := s.AddModule(ModuleDef{Name: bigName, Body: mkSteps(maxSteps)}); err != nil {
		t.Fatalf("exact name/steps: %v", err)
	}
	if err := s.AddModule(ModuleDef{Name: "M1", Body: mkSteps(maxSteps + 1)}); !errors.Is(err, ErrInvalidArg) {
		t.Fatalf("steps+1: %v", err)
	}
	if err := s.AddModule(ModuleDef{Name: string(make([]byte, maxNameLen+1))}); !errors.Is(err, ErrInvalidArg) {
		t.Fatalf("name+1: %v", err)
	}
	// 20 条导入语句，每条恰好 10 个互不重复名字；20 个本地导出。
	var stmts []ImportStmt
	for i := 0; i < maxImports; i++ {
		binds := make([]string, maxBindings)
		for j := range binds {
			binds[j] = uniqueName(i*100 + j)
		}
		stmts = append(stmts, ImportStmt{Source: "Src", Bindings: binds})
	}
	exports := make([]Export, maxExports)
	for i := range exports {
		exports[i] = Export{Name: uniqueName(10000 + i), Kind: KindFunction}
	}
	if err := s.AddModule(ModuleDef{Name: "Exact", Imports: stmts, Exports: exports}); err != nil {
		t.Fatalf("exact imports/exports: %v", err)
	}
	overBind := make([]string, maxBindings+1)
	for i := range overBind {
		overBind[i] = uniqueName(20000 + i)
	}
	if err := s.AddModule(ModuleDef{Name: "Over", Imports: []ImportStmt{{Source: "Src", Bindings: overBind}}}); !errors.Is(err, ErrInvalidArg) {
		t.Fatalf("bindings+1: %v", err)
	}
}

func uniqueName(i int) string {
	// 返回 1..32 字节、互不相同的名字。
	return "n" + itoa(i)
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b []byte
	for i > 0 {
		b = append([]byte{byte('0' + i%10)}, b...)
		i /= 10
	}
	return string(b)
}

// 模块数恰好 100 允许，第 101 个报超限。
func TestModuleCountLimit(t *testing.T) {
	s := NewSession()
	for i := 0; i < maxModules; i++ {
		if err := s.AddModule(ModuleDef{Name: "m" + itoa(i)}); err != nil {
			t.Fatalf("add %d: %v", i, err)
		}
	}
	if err := s.AddModule(ModuleDef{Name: "overflow"}); !errors.Is(err, ErrTooMany) {
		t.Fatalf("101st: %v", err)
	}
}

// 最近一次顶层 ResolveExport：每个 (模块,名字) 对至多展开一次，
// 计数器键数等于解析集合最终大小。
func TestResolutionExpansionCounter(t *testing.T) {
	s := NewSession()
	mustAdd(t, s, ModuleDef{Name: "E1", Exports: []Export{{"v", KindLet}}})
	mustAdd(t, s, ModuleDef{Name: "U", ReExports: []ReExport{starX("E1"), starX("E1")}})
	mustAdd(t, s, ModuleDef{Name: "W", Imports: []ImportStmt{{Source: "U", Bindings: []string{"v"}}}})
	_ = evalMust(t, s, "W")
	// 最后一次顶层解析为 W 的导入 (U,v)：展开 (W? 不，从 U 开始)
	// 集合含 (U,v) 与 (E1,v)，各恰好一次。
	if len(s.resolutionExpansions) != 2 {
		t.Fatalf("expansions=%v", s.resolutionExpansions)
	}
	for k, n := range s.resolutionExpansions {
		if n != 1 {
			t.Fatalf("pair %v expanded %d times", k, n)
		}
	}
}

package initorder

import (
	"errors"
	"reflect"
	"testing"
)

// mustSession 创建会话，预声明标识符非法时直接失败。
func mustSession(t *testing.T, predeclared ...string) *Session {
	t.Helper()
	s, err := NewSession(predeclared)
	if err != nil {
		t.Fatalf("NewSession(%v) failed: %v", predeclared, err)
	}
	return s
}

// mustRegisterVars 登记变量单元，期望成功。
func mustRegisterVars(t *testing.T, s *Session, vars []string, refs []string) {
	t.Helper()
	if err := s.RegisterVars(vars, refs); err != nil {
		t.Fatalf("RegisterVars(%v, %v) failed: %v", vars, refs, err)
	}
}

// mustRegisterFunc 登记函数声明，期望成功。
func mustRegisterFunc(t *testing.T, s *Session, name string, refs []string) {
	t.Helper()
	if err := s.RegisterFunc(name, refs); err != nil {
		t.Fatalf("RegisterFunc(%q, %v) failed: %v", name, refs, err)
	}
}

// solveUnits 求解并返回初始化次序中的单元下标序列。
func solveUnits(t *testing.T, s *Session) []int {
	t.Helper()
	sol, err := s.Solve()
	if err != nil {
		t.Fatalf("Solve failed: %v", err)
	}
	order := make([]int, 0, len(sol.Order))
	for _, u := range sol.Order {
		order = append(order, u.Unit)
	}
	return order
}

// depsOf 求解并返回指定单元的传递依赖变量集合。
func depsOf(t *testing.T, s *Session, unit int) []string {
	t.Helper()
	sol, err := s.Solve()
	if err != nil {
		t.Fatalf("Solve failed: %v", err)
	}
	for _, u := range sol.Order {
		if u.Unit == unit {
			return u.Deps
		}
	}
	t.Fatalf("unit %d not in solution", unit)
	return nil
}

func TestEmptySessionSolvesEmpty(t *testing.T) {
	s := mustSession(t)
	sol, err := s.Solve()
	if err != nil {
		t.Fatalf("Solve failed: %v", err)
	}
	if len(sol.Order) != 0 {
		t.Fatalf("expected empty order, got %v", sol.Order)
	}
}

// 贪心规则逐字语义：每轮取源码最靠前的可选单元，不是任意拓扑序。
func TestGreedyOrderFollowsSourcePosition(t *testing.T) {
	s := mustSession(t)
	mustRegisterVars(t, s, []string{"y"}, []string{"x"}) // 单元 0：依赖 x
	mustRegisterVars(t, s, []string{"x"}, nil)           // 单元 1
	mustRegisterVars(t, s, []string{"z"}, nil)           // 单元 2
	// 第 1 轮可选 {1, 2}，取 1；第 2 轮可选 {0, 2}，取 0；第 3 轮取 2。
	if got, want := solveUnits(t, s), []int{1, 0, 2}; !reflect.DeepEqual(got, want) {
		t.Fatalf("order = %v, want %v", got, want)
	}
	if got := depsOf(t, s, 0); !reflect.DeepEqual(got, []string{"x"}) {
		t.Fatalf("deps of unit 0 = %v, want [x]", got)
	}
	if got := depsOf(t, s, 1); len(got) != 0 {
		t.Fatalf("deps of unit 1 = %v, want empty", got)
	}
}

// 经多层函数的间接依赖：u1 -> f3 -> f2 -> f1 -> x。
func TestMultiLayerFuncIndirection(t *testing.T) {
	s := mustSession(t, "runtime")
	mustRegisterVars(t, s, []string{"x"}, nil)              // 单元 0
	mustRegisterFunc(t, s, "f1", []string{"x"})             // 直接提到变量
	mustRegisterFunc(t, s, "f2", []string{"f1"})            // 提到函数
	mustRegisterFunc(t, s, "f3", []string{"f2", "runtime"}) // 提到函数与预声明
	mustRegisterVars(t, s, []string{"y"}, []string{"f3"})   // 单元 1
	if got, want := solveUnits(t, s), []int{0, 1}; !reflect.DeepEqual(got, want) {
		t.Fatalf("order = %v, want %v", got, want)
	}
	if got := depsOf(t, s, 1); !reflect.DeepEqual(got, []string{"x"}) {
		t.Fatalf("deps of unit 1 = %v, want [x]", got)
	}
}

// 函数相互递归：递归本身不构成错误。
func TestMutualRecursion(t *testing.T) {
	s := mustSession(t)
	mustRegisterVars(t, s, []string{"a"}, nil)           // 单元 0
	mustRegisterFunc(t, s, "f", []string{"g", "a"})      // f 依赖 g 与变量 a
	mustRegisterFunc(t, s, "g", []string{"f"})           // g 与 f 相互递归
	mustRegisterVars(t, s, []string{"b"}, []string{"g"}) // 单元 1
	if got, want := solveUnits(t, s), []int{0, 1}; !reflect.DeepEqual(got, want) {
		t.Fatalf("order = %v, want %v", got, want)
	}
	if got := depsOf(t, s, 1); !reflect.DeepEqual(got, []string{"a"}) {
		t.Fatalf("deps of unit 1 = %v, want [a]", got)
	}
}

// 纯函数递归环（不触及任何变量）：引用它的单元无依赖。
func TestPureFuncRecursionNoDeps(t *testing.T) {
	s := mustSession(t)
	mustRegisterFunc(t, s, "f", []string{"g"})
	mustRegisterFunc(t, s, "g", []string{"f"})
	mustRegisterVars(t, s, []string{"a"}, []string{"f"}) // 单元 0
	if got, want := solveUnits(t, s), []int{0}; !reflect.DeepEqual(got, want) {
		t.Fatalf("order = %v, want %v", got, want)
	}
	if got := depsOf(t, s, 0); len(got) != 0 {
		t.Fatalf("deps of unit 0 = %v, want empty", got)
	}
}

// 多变量单元与空白标识符：同一单元的变量一同完成；每个 "_" 独立。
func TestMultiVarUnitAndBlank(t *testing.T) {
	s := mustSession(t)
	mustRegisterVars(t, s, []string{"x", "_", "y"}, nil)      // 单元 0：两个真实变量 + 空白
	mustRegisterVars(t, s, []string{"_", "_"}, []string{"x"}) // 单元 1：两个互不相同且不可引用的空白
	mustRegisterVars(t, s, []string{"w"}, []string{"y"})      // 单元 2
	if got, want := solveUnits(t, s), []int{0, 1, 2}; !reflect.DeepEqual(got, want) {
		t.Fatalf("order = %v, want %v", got, want)
	}
	if got := depsOf(t, s, 1); !reflect.DeepEqual(got, []string{"x"}) {
		t.Fatalf("deps of unit 1 = %v, want [x]", got)
	}
	// 空白标识符不出现在任何依赖集合中。
	sol, err := s.Solve()
	if err != nil {
		t.Fatalf("Solve failed: %v", err)
	}
	for _, u := range sol.Order {
		for _, d := range u.Deps {
			if d == "_" {
				t.Fatalf("blank identifier in deps of unit %d", u.Unit)
			}
		}
	}
}

// 前向引用：引用登记时未声明的标识符，求解时才判定。
func TestForwardReference(t *testing.T) {
	s := mustSession(t)
	mustRegisterVars(t, s, []string{"a"}, []string{"f"}) // 单元 0：引用尚未登记的 f
	mustRegisterFunc(t, s, "f", []string{"b"})           // f 引用尚未登记的 b
	mustRegisterVars(t, s, []string{"b"}, nil)           // 单元 1
	// 单元 1 无依赖先完成，单元 0 经 f 传递依赖 b。
	if got, want := solveUnits(t, s), []int{1, 0}; !reflect.DeepEqual(got, want) {
		t.Fatalf("order = %v, want %v", got, want)
	}
	if got := depsOf(t, s, 0); !reflect.DeepEqual(got, []string{"b"}) {
		t.Fatalf("deps of unit 0 = %v, want [b]", got)
	}
}

// 单元直接引用自己的变量：初始化环。
func TestSelfReferenceCycle(t *testing.T) {
	s := mustSession(t)
	mustRegisterVars(t, s, []string{"x"}, []string{"x"})
	_, err := s.Solve()
	var cyc *InitializationCycleError
	if !errors.As(err, &cyc) {
		t.Fatalf("expected InitializationCycleError, got %v", err)
	}
	if !reflect.DeepEqual(cyc.Vars, []string{"x"}) {
		t.Fatalf("cycle vars = %v, want [x]", cyc.Vars)
	}
	if !errors.Is(err, ErrInitializationCycle) {
		t.Fatalf("expected ErrInitializationCycle, got %v", err)
	}
}

// 经函数的环：单元引用函数，函数体又引用该单元的变量。
func TestCycleThroughFunc(t *testing.T) {
	s := mustSession(t)
	mustRegisterVars(t, s, []string{"x"}, []string{"f"}) // 单元 0：x 依赖 f
	mustRegisterFunc(t, s, "f", []string{"x"})           // f 依赖 x
	_, err := s.Solve()
	var cyc *InitializationCycleError
	if !errors.As(err, &cyc) {
		t.Fatalf("expected InitializationCycleError, got %v", err)
	}
	if !reflect.DeepEqual(cyc.Vars, []string{"x"}) {
		t.Fatalf("cycle vars = %v, want [x]", cyc.Vars)
	}
}

// 多单元环：报告无法完成的全部变量，按源码次序排列，不含空白标识符。
func TestMultiUnitCycleReportsAllVars(t *testing.T) {
	s := mustSession(t)
	mustRegisterVars(t, s, []string{"ok"}, nil)               // 单元 0：可完成
	mustRegisterVars(t, s, []string{"a", "_"}, []string{"b"}) // 单元 1：环中，含空白
	mustRegisterVars(t, s, []string{"b"}, []string{"a"})      // 单元 2：环中
	mustRegisterVars(t, s, []string{"c"}, []string{"a"})      // 单元 3：被环拖累
	_, err := s.Solve()
	var cyc *InitializationCycleError
	if !errors.As(err, &cyc) {
		t.Fatalf("expected InitializationCycleError, got %v", err)
	}
	if want := []string{"a", "b", "c"}; !reflect.DeepEqual(cyc.Vars, want) {
		t.Fatalf("cycle vars = %v, want %v", cyc.Vars, want)
	}
}

// 未声明引用：报告登记次序最早的声明与其中字典序最小的未声明标识符。
func TestUndeclaredReference(t *testing.T) {
	s := mustSession(t)
	mustRegisterVars(t, s, []string{"a"}, []string{"zz", "mm"}) // 单元 0：两个未声明
	mustRegisterVars(t, s, []string{"b"}, []string{"aa"})       // 单元 1：登记更晚
	_, err := s.Solve()
	var undecl *UndeclaredReferenceError
	if !errors.As(err, &undecl) {
		t.Fatalf("expected UndeclaredReferenceError, got %v", err)
	}
	if !errors.Is(err, ErrUndeclaredReference) {
		t.Fatalf("expected ErrUndeclaredReference, got %v", err)
	}
	if undecl.DeclIndex != 0 || undecl.Kind != DeclKindVars || undecl.Ident != "mm" {
		t.Fatalf("got %+v, want DeclIndex=0 Kind=vars Ident=mm", undecl)
	}
}

// 未声明引用必须覆盖永远不会被任何单元用到的函数。
func TestUndeclaredInUnusedFunc(t *testing.T) {
	s := mustSession(t)
	mustRegisterVars(t, s, []string{"a"}, nil)           // 单元 0：无依赖
	mustRegisterFunc(t, s, "ghost", []string{"missing"}) // 无人使用的函数含未声明引用
	_, err := s.Solve()
	var undecl *UndeclaredReferenceError
	if !errors.As(err, &undecl) {
		t.Fatalf("expected UndeclaredReferenceError, got %v", err)
	}
	if undecl.Kind != DeclKindFunc || undecl.Name != "ghost" || undecl.Ident != "missing" {
		t.Fatalf("got %+v, want Kind=func Name=ghost Ident=missing", undecl)
	}
}

// 未声明引用优先于初始化环。
func TestUndeclaredPriorityOverCycle(t *testing.T) {
	s := mustSession(t)
	mustRegisterVars(t, s, []string{"x"}, []string{"x"})     // 单元 0：自引用环
	mustRegisterVars(t, s, []string{"y"}, []string{"ghost"}) // 单元 1：未声明引用
	_, err := s.Solve()
	if !errors.Is(err, ErrUndeclaredReference) {
		t.Fatalf("expected ErrUndeclaredReference, got %v", err)
	}
}

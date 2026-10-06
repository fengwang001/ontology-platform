package subtype

import (
	"fmt"
	"sync"
	"testing"
)

// ---- 构造辅助 ----

func pr(name string, t *Type) Prop  { return Prop{Name: name, Type: t} }
func po(name string, t *Type) Prop  { return Prop{Name: name, Type: t, Optional: true} }
func prw(name string, t *Type) Prop { return Prop{Name: name, Type: t, ReadOnly: true} }
func prwo(name string, t *Type) Prop {
	return Prop{Name: name, Type: t, Optional: true, ReadOnly: true}
}

func check(t *testing.T, reg *Registry, s, x *Type, want bool, why string) {
	t.Helper()
	got, err := reg.IsSubtype(s, x)
	if err != nil {
		t.Fatalf("IsSubtype(%s, %s) 出错: %v", s, x, err)
	}
	if got != want {
		t.Errorf("IsSubtype(%s, %s) = %v, 期望 %v；依据: %s", s, x, got, want, why)
	}
	t.Logf("判定 IsSubtype(%s, %s) = %v（期望 %v）；依据: %s", s, x, got, want, why)
}

func expectErr(t *testing.T, err error, kind ErrorKind, name, why string) {
	t.Helper()
	if err == nil {
		t.Fatalf("期望 %v 错误（%s），实际无错误；依据: %s", kind, name, why)
	}
	if !HasKind(err, kind) {
		t.Fatalf("期望错误类别 %v，实际 %v；依据: %s", kind, err, why)
	}
	if name != "" && ErrorName(err) != name {
		t.Fatalf("期望错误名字 %q，实际 %q（%v）；依据: %s", name, ErrorName(err), err, why)
	}
	t.Logf("错误符合预期: %v；依据: %s", err, why)
}

// ---- 基本类型、顶/底、空联合 ----

func TestPrimitiveTopBottom(t *testing.T) {
	reg := NewRegistry()
	check(t, reg, Int(), Float(), true, "int 是 float 的子类型")
	check(t, reg, Float(), Int(), false, "float 不是 int 的子类型")
	check(t, reg, Int(), Int(), true, "自反")
	check(t, reg, Str(), Boolean(), false, "基本类型互不相关")
	check(t, reg, Int(), Str(), false, "基本类型互不相关")
	check(t, reg, Float(), Float(), true, "自反")
	check(t, reg, Obj(pr("a", Int())), Top(), true, "任何类型都是 top 的子类型")
	check(t, reg, Top(), Top(), true, "top 自反")
	check(t, reg, Top(), Int(), false, "top 只是 top 的子类型")
	check(t, reg, Bottom(), Obj(pr("a", Int())), true, "bottom 是任何类型的子类型")
	check(t, reg, Bottom(), Bottom(), true, "bottom 自反")
	check(t, reg, Int(), Bottom(), false, "非底类型不是 bottom 的子类型")
	check(t, reg, Union(), Int(), true, "空联合就是底类型")
	check(t, reg, Int(), Union(), false, "任何类型不是空联合的子类型（除 bottom）")
	check(t, reg, Union(Int(), Union(Float(), Str())), Union(Float(), Str(), Int()), true,
		"联合拍平后两侧成员互相覆盖")
}

// ---- 对象：宽度与深度 ----

func TestObjectWidthDepth(t *testing.T) {
	reg := NewRegistry()
	check(t, reg,
		Obj(pr("a", Int()), pr("b", Str())), Obj(pr("a", Int())), true,
		"宽度：S 可以有 T 没有的额外属性")
	check(t, reg,
		Obj(pr("a", Int())), Obj(pr("a", Int()), pr("b", Str())), false,
		"宽度：S 缺少 T 的必选属性")
	check(t, reg,
		Obj(prw("a", Obj(prw("b", Int())))), Obj(prw("a", Obj(prw("b", Float())))), true,
		"深度：经只读属性的嵌套属性类型协变")
	check(t, reg,
		Obj(prw("a", Obj(prw("b", Float())))), Obj(prw("a", Obj(prw("b", Int())))), false,
		"深度：嵌套属性类型不满足方向")
	check(t, reg,
		Obj(pr("a", Obj(pr("b", Int())))), Obj(pr("a", Obj(pr("b", Float())))), false,
		"深度：可写属性要求互为子类型，int/float 只满足单向")
	check(t, reg, Obj(), Obj(), true, "空对象自反")
	check(t, reg, Obj(pr("a", Int())), Obj(), true, "任何对象都是空对象的子类型")
	check(t, reg, Obj(), Obj(pr("a", Int())), false, "空对象缺少必选属性")
}

// ---- 对象：可选与必选的全部组合 ----

func TestObjectOptionalMatrix(t *testing.T) {
	reg := NewRegistry()
	// T 必选：S 必须存在且必选。
	check(t, reg, Obj(pr("x", Int())), Obj(pr("x", Int())), true, "T 必选 / S 必选")
	check(t, reg, Obj(po("x", Int())), Obj(pr("x", Int())), false, "T 必选 / S 可选")
	check(t, reg, Obj(), Obj(pr("x", Int())), false, "T 必选 / S 缺失")
	// T 可选：S 可缺失、可选或必选。
	check(t, reg, Obj(pr("x", Int())), Obj(po("x", Int())), true, "T 可选 / S 必选")
	check(t, reg, Obj(po("x", Int())), Obj(po("x", Int())), true, "T 可选 / S 可选")
	check(t, reg, Obj(), Obj(po("x", Int())), true, "T 可选 / S 缺失")
	// 类型约束在属性存在时仍然生效。
	check(t, reg, Obj(po("x", Str())), Obj(po("x", Int())), false, "T 可选 / S 可选但类型不符")
	check(t, reg, Obj(pr("x", Int())), Obj(prwo("x", Float())), true,
		"T 可选只读 / S 必选，类型协变即可")
	check(t, reg, Obj(pr("x", Int())), Obj(po("x", Float())), false,
		"T 可选但可写：仍要求互为子类型")
}

// ---- 对象：只读与可写 ----

func TestObjectReadOnly(t *testing.T) {
	reg := NewRegistry()
	// T 只读：S 侧只读性不限，类型只需协变。
	check(t, reg, Obj(pr("x", Int())), Obj(prw("x", Float())), true,
		"T 只读 / S 可写，类型协变即可")
	check(t, reg, Obj(prw("x", Int())), Obj(prw("x", Float())), true,
		"T 只读 / S 只读，类型协变")
	check(t, reg, Obj(pr("x", Float())), Obj(prw("x", Int())), false,
		"T 只读但类型方向不符")
	// 单向放宽：反向不成立。
	check(t, reg, Obj(prw("x", Int())), Obj(pr("x", Float())), false,
		"T 可写 / S 只读，不成立（只读放宽是单向的）")
	// T 可写：S 必须可写且两侧类型互为子类型。
	check(t, reg, Obj(pr("x", Int())), Obj(pr("x", Int())), true, "T 可写 / 类型相同")
	check(t, reg, Obj(pr("x", Int())), Obj(pr("x", Float())), false,
		"T 可写要求互为子类型：int<=float 但 float!<=int")
	check(t, reg, Obj(pr("x", Float())), Obj(pr("x", Int())), false,
		"T 可写要求互为子类型：反向同样不成立")
	// 可选只读组合。
	check(t, reg, Obj(prwo("x", Int())), Obj(prwo("x", Float())), true,
		"T 可选只读 / S 可选只读，类型协变")
	check(t, reg, Obj(), Obj(prwo("x", Float())), true, "T 可选只读 / S 缺失")
}

// ---- 函数：参数逆变、参数个数、返回协变 ----

func TestFunction(t *testing.T) {
	reg := NewRegistry()
	check(t, reg,
		Fn([]*Type{Float()}, Int()), Fn([]*Type{Int()}, Float()), true,
		"参数逆变（T 的 int 是 S 的 float 的子类型）且返回协变")
	check(t, reg,
		Fn([]*Type{Int()}, Int()), Fn([]*Type{Float()}, Int()), false,
		"参数方向相反不成立")
	check(t, reg,
		Fn([]*Type{Int()}, Int()), Fn([]*Type{Int(), Int()}, Int()), true,
		"S 参数个数不多于 T")
	check(t, reg,
		Fn([]*Type{Int(), Int()}, Int()), Fn([]*Type{Int()}, Int()), false,
		"S 参数多于 T 不成立")
	check(t, reg,
		Fn(nil, Int()), Fn(nil, Float()), true, "返回协变")
	check(t, reg,
		Fn(nil, Float()), Fn(nil, Int()), false, "返回方向不符")
	check(t, reg,
		Fn(nil, Int()), Obj(), false, "函数与对象互不相关")
	check(t, reg,
		Fn([]*Type{Int()}, Int()), Fn([]*Type{Int()}, Int()), true, "自反")
}

// ---- 联合：两个方向的非对称 ----

func TestUnionAsymmetry(t *testing.T) {
	reg := NewRegistry()
	check(t, reg, Int(), Union(Int(), Str()), true, "非联合 S 命中联合 T 的某个成员")
	check(t, reg, Boolean(), Union(Int(), Str()), false, "非联合 S 不命中任何成员")
	check(t, reg, Union(Int(), Str()), Int(), false, "联合 S 要求每个成员都是 T 的子类型")
	check(t, reg, Union(Int(), Float()), Float(), true, "联合 S 的每个成员都是 float 的子类型")
	check(t, reg, Union(Int(), Str()), Union(Str(), Int(), Boolean()), true,
		"联合对联合：左侧每个成员命中右侧某成员")
	check(t, reg, Union(Int(), Boolean()), Union(Str(), Int()), false,
		"联合对联合：bool 无归宿")
	check(t, reg, Obj(pr("a", Int())), Union(Obj(pr("a", Int())), Str()), true,
		"对象命中联合成员（成员属性可写但类型相同）")
	check(t, reg, Obj(pr("a", Int())), Union(Obj(prw("a", Float())), Str()), true,
		"对象经只读协变命中联合成员")
	check(t, reg,
		Obj(prw("a", Int()), prw("b", Str())),
		Union(Obj(prw("a", Float()), prw("b", Boolean())), Obj(prw("a", Boolean()), prw("b", Str()))),
		false,
		"明确取舍：a 适配成员一、b 适配成员二，但不把对象拆开分配后再比较")
}

// ---- 递归：相互递归、链式递归、自递归的最大解 ----

func TestMutualRecursion(t *testing.T) {
	reg := NewRegistry()
	mustRegister(t, reg, "A", Obj(prw("next", Ref("B"))))
	mustRegister(t, reg, "B", Obj(prw("next", Ref("A"))))
	mustRegister(t, reg, "C", Obj(prw("next", Ref("D"))))
	mustRegister(t, reg, "D", Obj(prw("next", Ref("C"))))
	check(t, reg, Ref("A"), Ref("C"), true,
		"相互递归最大解：同构结构互为子类型")
	check(t, reg, Ref("A"), Ref("A"), true, "同名引用直接成立")

	mustRegister(t, reg, "E", Obj(prw("next", Ref("F"))))
	mustRegister(t, reg, "F", Obj(prw("next", Ref("E")), pr("extra", Boolean())))
	check(t, reg, Ref("A"), Ref("E"), false,
		"最大解：深层属性缺失沿递归回传，(A,E) 被移出不动点")
	check(t, reg, Ref("E"), Ref("A"), true,
		"宽度方向：E/F 带额外属性，是 A/B 的子类型")
}

func TestChainRecursion(t *testing.T) {
	reg := NewRegistry()
	mustRegister(t, reg, "N1", Obj(pr("next", Ref("N2"))))
	mustRegister(t, reg, "N2", Obj(pr("next", Ref("N3"))))
	mustRegister(t, reg, "N3", Obj(pr("next", Ref("N1"))))
	mustRegister(t, reg, "M1", Obj(pr("next", Ref("M2"))))
	mustRegister(t, reg, "M2", Obj(pr("next", Ref("M3"))))
	mustRegister(t, reg, "M3", Obj(pr("next", Ref("M1"))))
	check(t, reg, Ref("N1"), Ref("M1"), true, "链式递归最大解：三环同构")
	check(t, reg, Ref("N1"), Ref("M2"), true, "同构链任意起点互为子类型")

	mustRegister(t, reg, "Self", Obj(pr("next", Ref("Self"))))
	check(t, reg, Ref("Self"), Ref("N1"), true, "自递归与三环结构同构")
	check(t, reg, Ref("N1"), Ref("Self"), true, "反向亦成立")
}

func TestWritableRecursion(t *testing.T) {
	reg := NewRegistry()
	// 可写属性要求两个方向互为子类型，递归假设同样适用。
	mustRegister(t, reg, "WA", Obj(pr("x", Ref("WB"))))
	mustRegister(t, reg, "WB", Obj(pr("x", Ref("WA"))))
	check(t, reg, Ref("WA"), Ref("WB"), true,
		"可写属性上的递归：互为子类型的两个方向在同一不动点中成立")

	mustRegister(t, reg, "VA", Obj(pr("x", Ref("VB"))))
	mustRegister(t, reg, "VB", Obj(pr("x", Ref("VA")), pr("y", Int())))
	check(t, reg, Ref("WA"), Ref("VA"), false,
		"可写属性不变量：VB 多出必选属性 y，两个方向都被推翻")
	check(t, reg, Ref("VA"), Ref("WA"), false,
		"可写属性不变量：反向同样不成立")

	// 只读属性上的递归只需单向。
	mustRegister(t, reg, "RA", Obj(prw("x", Ref("RB"))))
	mustRegister(t, reg, "RB", Obj(prw("x", Ref("RB")), pr("y", Int())))
	check(t, reg, Ref("RB"), Ref("RA"), true,
		"只读属性递归：更宽的一侧是子类型（x 类型同为 RB，协变成立）")
	check(t, reg, Ref("RA"), Ref("RB"), false,
		"只读属性递归：缺少 y 的方向不成立")
}

func TestRecursionThroughFunc(t *testing.T) {
	reg := NewRegistry()
	mustRegister(t, reg, "F1", Fn([]*Type{Ref("F1")}, Int()))
	mustRegister(t, reg, "F2", Fn([]*Type{Ref("F2")}, Int()))
	check(t, reg, Ref("F1"), Ref("F2"), true,
		"经函数构造子的递归：参数逆变与返回协变的假设在不动点中同时成立")
	mustRegister(t, reg, "F3", Fn([]*Type{Ref("F3")}, Float()))
	check(t, reg, Ref("F1"), Ref("F3"), false,
		"参数逆变使递归方向逐层翻转，返回类型 int/float 无法双向满足")
	check(t, reg, Ref("F3"), Ref("F1"), false,
		"反向同样不成立")
}

func mustRegister(t *testing.T, reg *Registry, name string, def *Type) {
	t.Helper()
	if err := reg.Register(name, def); err != nil {
		t.Fatalf("Register(%q, %s) 意外失败: %v", name, def, err)
	}
}

// ---- 前向引用后补登记 ----

func TestForwardReference(t *testing.T) {
	reg := NewRegistry()
	mustRegister(t, reg, "List", Obj(pr("head", Int()), pr("tail", Ref("List"))))
	mustRegister(t, reg, "Boxed", Obj(pr("value", Ref("Missing"))))

	_, err := reg.Check(Ref("List"), Obj(pr("head", Float())))
	if err != nil {
		t.Fatalf("已定义链上的判定不应报错: %v", err)
	}

	_, err = reg.Check(Ref("Boxed"), Top())
	expectErr(t, err, ErrUndefinedReference, "Missing",
		"前向引用未补登记时判定报错，即使 top 规则本可短路")

	mustRegister(t, reg, "Missing", Int())
	check(t, reg, Ref("Boxed"), Obj(prw("value", Float())), true,
		"补登记后判定正常：int<=float 经只读属性协变")
}

// ---- 未定义引用：静态可达全集 + 字典序最小 ----

func TestUndefinedReference(t *testing.T) {
	reg := NewRegistry()
	mustRegister(t, reg, "D", Obj(pr("x", Int()), pr("y", Ref("Zeta"))))
	// 判定 (int, D) 时 y 分支因 x 已失败而不会被动态走到，
	// 但静态可达检查仍须发现 Zeta 未定义。
	_, err := reg.Check(Int(), Ref("D"))
	expectErr(t, err, ErrUndefinedReference, "Zeta",
		"静态可达的未定义引用即使不在动态判定路径上也要报告")

	mustRegister(t, reg, "E", Obj(pr("a", Ref("Beta")), pr("b", Ref("Alpha"))))
	_, err = reg.Check(Ref("E"), Ref("D"))
	expectErr(t, err, ErrUndefinedReference, "Alpha",
		"多个未定义引用报告字典序最小者")
}

// ---- 无保护循环：直接形态与联合形态 ----

func TestUnguardedCycle(t *testing.T) {
	reg := NewRegistry()
	mustRegister(t, reg, "Loop", Ref("Loop"))
	_, err := reg.Check(Ref("Loop"), Int())
	expectErr(t, err, ErrUnguardedCycle, "Loop", "直接自循环 A = A")

	mustRegister(t, reg, "U", Union(Ref("V"), Int()))
	mustRegister(t, reg, "V", Ref("U"))
	_, err = reg.Check(Ref("U"), Top())
	expectErr(t, err, ErrUnguardedCycle, "U",
		"经联合的间接回环 U = V|int, V = U，即使 top 可短路也报告")

	mustRegister(t, reg, "C1", Ref("C2"))
	mustRegister(t, reg, "C2", Ref("C3"))
	mustRegister(t, reg, "C3", Ref("C1"))
	_, err = reg.Check(Ref("C2"), Int())
	expectErr(t, err, ErrUnguardedCycle, "C1",
		"链式无保护回环报告环上字典序最小名字")

	// 经过对象/函数构造子的循环是受保护的，不是错误。
	reg2 := NewRegistry()
	mustRegister(t, reg2, "G", Obj(pr("self", Ref("G"))))
	check(t, reg2, Ref("G"), Ref("G"), true, "经对象构造子的循环受保护")
	mustRegister(t, reg2, "H", Fn([]*Type{Ref("H")}, Int()))
	check(t, reg2, Ref("H"), Ref("H"), true, "经函数构造子的循环受保护")
	// 联合里只要每个回环都受保护即可。
	mustRegister(t, reg2, "W", Union(Obj(prw("w", Ref("W"))), Int()))
	check(t, reg2, Ref("W"), Union(Obj(prw("w", Top())), Int()), true,
		"联合内经受保护构造子的递归合法")
}

// ---- 错误优先级 ----

func TestErrorPriority(t *testing.T) {
	reg := NewRegistry()
	// 登记：参数非法优先于重复定义。
	mustRegister(t, reg, "X", Int())
	err := reg.Register("X", Obj(pr("a", Int()), pr("a", Int())))
	expectErr(t, err, ErrInvalidArgument, "a",
		"重复名字 + 非法定义时报告参数非法")
	err = reg.Register("", Int())
	expectErr(t, err, ErrInvalidArgument, "", "空名字非法")
	err = reg.Register("Y", nil)
	expectErr(t, err, ErrInvalidArgument, "", "空定义非法")

	// 判定：参数非法优先于未定义引用。
	mustRegister(t, reg, "Cyc", Ref("Cyc"))
	_, err = reg.Check(Obj(pr("a", Int()), pr("a", Int())), Ref("Nope"))
	expectErr(t, err, ErrInvalidArgument, "a",
		"判定参数非法优先于未定义引用与无保护循环")
	// 未定义引用优先于无保护循环。
	_, err = reg.Check(Ref("Cyc"), Ref("Nope"))
	expectErr(t, err, ErrUndefinedReference, "Nope",
		"未定义引用优先于无保护循环")
	// 仅无保护循环。
	_, err = reg.Check(Ref("Cyc"), Int())
	expectErr(t, err, ErrUnguardedCycle, "Cyc", "仅剩无保护循环时报告之")
}

// ---- 被拒绝的登记不改变状态 ----

func TestRejectedRegisterKeepsState(t *testing.T) {
	reg := NewRegistry()
	mustRegister(t, reg, "K", Int())
	before := reg.Snapshot()

	if err := reg.Register("K", Str()); !HasKind(err, ErrDuplicateDefinition) {
		t.Fatalf("期望重复定义错误，实际: %v", err)
	}
	if err := reg.Register("Bad", Obj(pr("a", Int()), pr("a", Int()))); !HasKind(err, ErrInvalidArgument) {
		t.Fatalf("期望参数非法错误，实际: %v", err)
	}
	after := reg.Snapshot()
	if len(before) != len(after) {
		t.Fatalf("被拒绝的登记改变了定义数量: %d -> %d", len(before), len(after))
	}
	for name, def := range before {
		if !Equal(def, after[name]) {
			t.Fatalf("被拒绝的登记改变了定义 %q", name)
		}
	}
	check(t, reg, Ref("K"), Int(), true, "原定义保持不变")
	check(t, reg, Ref("K"), Str(), false, "被拒绝的新定义未生效")
	_, err := reg.Check(Ref("Bad"), Int())
	expectErr(t, err, ErrUndefinedReference, "Bad", "被拒绝的登记不产生定义")
}

// ---- 判定结果一致且不修改已登记定义 ----

func TestDeterminismAndImmutability(t *testing.T) {
	reg := NewRegistry()
	mustRegister(t, reg, "A", Obj(pr("next", Ref("B")), pr("v", Int())))
	mustRegister(t, reg, "B", Obj(pr("next", Ref("A")), pr("v", Int())))
	mustRegister(t, reg, "C", Obj(pr("next", Ref("C")), pr("v", Float())))
	before := reg.Snapshot()

	s := Obj(pr("x", Ref("A")))
	tt := Obj(pr("x", Ref("C")))
	var first bool
	for i := 0; i < 5; i++ {
		res, err := reg.Check(s, tt)
		if err != nil {
			t.Fatalf("第 %d 次判定出错: %v", i, err)
		}
		if i == 0 {
			first = res.Subtype
		} else if res.Subtype != first {
			t.Fatalf("同一对类型反复判定结果不一致: %v vs %v", first, res.Subtype)
		}
	}
	t.Logf("反复判定结果一致: IsSubtype(%s, %s) = %v", s, tt, first)

	after := reg.Snapshot()
	if len(before) != len(after) {
		t.Fatalf("判定改变了定义数量")
	}
	for name, def := range before {
		if !Equal(def, after[name]) {
			t.Fatalf("判定修改了已登记定义 %q", name)
		}
	}
}

// ---- 命名对数量界限：可验证的复杂度证明 ----

func TestPairBound(t *testing.T) {
	const n = 40
	reg := NewRegistry()
	for i := 0; i < n; i++ {
		name := fmt.Sprintf("X%d", i)
		next := fmt.Sprintf("X%d", (i+1)%n)
		mustRegister(t, reg, name, Obj(prw("next", Ref(next)), prw("v", Int())))
	}
	for i := 0; i < n; i++ {
		name := fmt.Sprintf("Y%d", i)
		next := fmt.Sprintf("Y%d", (i+1)%n)
		mustRegister(t, reg, name, Obj(prw("next", Ref(next)), prw("v", Float())))
	}
	res, err := reg.Check(Ref("X0"), Ref("Y0"))
	if err != nil {
		t.Fatalf("判定出错: %v", err)
	}
	if !res.Subtype {
		t.Errorf("同构递归链应互为子类型")
	}
	st := res.Stats
	t.Logf("链长 %d：命名对 %d（上界 %d），类型对 %d（上界 %d），迭代 %d 轮",
		n, st.NamedPairs, st.LeftNames*st.RightNames,
		st.PairsDiscovered, st.LeftNodes*st.RightNodes, st.Iterations)
	if st.NamedPairs > st.LeftNames*st.RightNames {
		t.Errorf("命名对数量越界: %d > %d*%d", st.NamedPairs, st.LeftNames, st.RightNames)
	}
	if st.PairsDiscovered > st.LeftNodes*st.RightNodes {
		t.Errorf("类型对数量越界: %d > %d*%d", st.PairsDiscovered, st.LeftNodes, st.RightNodes)
	}
	// 对链式结构，类型对数量应为线性而非指数。
	if st.PairsDiscovered > 8*n {
		t.Errorf("类型对数量疑似超线性增长: %d > 8*%d", st.PairsDiscovered, n)
	}
}

// ---- 并发：登记与判定等价于某个串行顺序 ----

func TestConcurrency(t *testing.T) {
	reg := NewRegistry()
	mustRegister(t, reg, "A", Obj(pr("x", Int()), pr("y", Str())))
	mustRegister(t, reg, "B", Obj(prw("x", Float())))
	mustRegister(t, reg, "L", Obj(pr("head", Int()), pr("tail", Ref("L"))))

	queries := []struct {
		s, tt *Type
		want  bool
	}{
		{Ref("A"), Ref("B"), true},
		{Ref("B"), Ref("A"), false},
		{Ref("L"), Obj(prw("head", Float())), true},
		{Int(), Float(), true},
		{Float(), Int(), false},
		{Ref("A"), Top(), true},
	}

	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 100; i++ {
				for qi, q := range queries {
					got, err := reg.IsSubtype(q.s, q.tt)
					if err != nil {
						t.Errorf("协程 %d 查询 %d 出错: %v", g, qi, err)
						return
					}
					if got != q.want {
						t.Errorf("协程 %d 查询 %d = %v, 期望 %v", g, qi, got, q.want)
						return
					}
				}
			}
		}(g)
	}
	// 并发登记互不相同的新名字：全部应成功，且最终全部可见。
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			name := fmt.Sprintf("G%d", g)
			if err := reg.Register(name, Obj(pr("id", Int()))); err != nil {
				t.Errorf("并发登记 %q 失败: %v", name, err)
			}
		}(g)
	}
	wg.Wait()

	snap := reg.Snapshot()
	for g := 0; g < 8; g++ {
		name := fmt.Sprintf("G%d", g)
		if _, ok := snap[name]; !ok {
			t.Errorf("并发登记后 %q 不可见", name)
		}
	}
	// 已接受的登记在后续判定中一致可见。
	check(t, reg, Ref("G3"), Obj(prw("id", Float())), true,
		"并发登记的新定义参与后续判定")
}

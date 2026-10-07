package subtype_test

import (
	"errors"
	"sync"
	"testing"

	"ontology/subtype"
)

// expectSubtype 断言 l 是否为 rt 的子类型。
func expectSubtype(t *testing.T, r *subtype.Registry, l, rt subtype.Type, want bool) {
	t.Helper()
	ok, err := r.Check(l, rt)
	if err != nil {
		t.Fatalf("Check(%v, %v) 返回意外错误: %v", l, rt, err)
	}
	if ok != want {
		t.Errorf("Check(%v, %v) = %v, 期望 %v", l, rt, ok, want)
	}
}

func mustRegister(t *testing.T, r *subtype.Registry, name string, def subtype.Type) {
	t.Helper()
	if err := r.Register(name, def); err != nil {
		t.Fatalf("Register(%q, %v) 返回意外错误: %v", name, def, err)
	}
}

func TestPrimitives(t *testing.T) {
	r := subtype.NewRegistry()
	cases := []struct {
		l, rt subtype.Type
		want  bool
	}{
		{subtype.Int{}, subtype.Int{}, true},
		{subtype.Int{}, subtype.Float{}, true}, // 整数是浮点的子类型
		{subtype.Float{}, subtype.Int{}, false},
		{subtype.Float{}, subtype.Float{}, true},
		{subtype.Str{}, subtype.Str{}, true},
		{subtype.Str{}, subtype.Int{}, false}, // 其余基本类型互不相关
		{subtype.Bool{}, subtype.Str{}, false},
		{subtype.Int{}, subtype.Bool{}, false},
		{subtype.Int{}, subtype.Top{}, true}, // 任何类型都是顶类型的子类型
		{subtype.Top{}, subtype.Top{}, true},
		{subtype.Top{}, subtype.Int{}, false},
		{subtype.Bottom{}, subtype.Int{}, true}, // 底类型是任何类型的子类型
		{subtype.Bottom{}, subtype.Bottom{}, true},
		{subtype.Bottom{}, subtype.Top{}, true},
		{subtype.Int{}, subtype.Bottom{}, false},
	}
	for _, c := range cases {
		expectSubtype(t, r, c.l, c.rt, c.want)
	}
}

func TestEmptyUnionIsBottom(t *testing.T) {
	r := subtype.NewRegistry()
	expectSubtype(t, r, subtype.Or(), subtype.Int{}, true)
	expectSubtype(t, r, subtype.Or(), subtype.Or(), true)
	expectSubtype(t, r, subtype.Or(), subtype.Top{}, true)
	expectSubtype(t, r, subtype.Int{}, subtype.Or(), false)
	expectSubtype(t, r, subtype.Bottom{}, subtype.Or(), true)
}

func TestObjectWidthAndDepth(t *testing.T) {
	r := subtype.NewRegistry()
	// 宽度：S 可以有 T 没有的额外属性。
	expectSubtype(t, r,
		subtype.Obj(subtype.P("x", subtype.Int{}), subtype.P("y", subtype.Str{})),
		subtype.Obj(subtype.P("x", subtype.Int{})),
		true)
	expectSubtype(t, r,
		subtype.Obj(subtype.P("x", subtype.Int{})),
		subtype.Obj(subtype.P("x", subtype.Int{}), subtype.P("y", subtype.Str{})),
		false)
	// 空对象是对象意义上的顶类型。
	expectSubtype(t, r, subtype.Obj(subtype.P("x", subtype.Int{})), subtype.Obj(), true)
	// 深度：属性类型协变（只读属性场景下），可写属性要求不变（见只读测试）。
	expectSubtype(t, r,
		subtype.Obj(subtype.RO("p", subtype.Obj(subtype.RO("a", subtype.Int{}), subtype.RO("b", subtype.Str{})))),
		subtype.Obj(subtype.RO("p", subtype.Obj(subtype.RO("a", subtype.Int{})))),
		true)
	expectSubtype(t, r,
		subtype.Obj(subtype.RO("p", subtype.Obj(subtype.RO("a", subtype.Int{})))),
		subtype.Obj(subtype.RO("p", subtype.Obj(subtype.RO("a", subtype.Float{})))),
		true) // int <: float 深度协变
	expectSubtype(t, r,
		subtype.Obj(subtype.RO("p", subtype.Obj(subtype.RO("a", subtype.Float{})))),
		subtype.Obj(subtype.RO("p", subtype.Obj(subtype.RO("a", subtype.Int{})))),
		false)
	// 对象与非对象不相关。
	expectSubtype(t, r, subtype.Obj(), subtype.Int{}, false)
	expectSubtype(t, r, subtype.Int{}, subtype.Obj(), false)
}

func TestOptionalRequiredMatrix(t *testing.T) {
	r := subtype.NewRegistry()
	// T 必选：S 中必须存在且为必选。
	expectSubtype(t, r,
		subtype.Obj(subtype.P("x", subtype.Int{})),
		subtype.Obj(subtype.P("x", subtype.Int{})),
		true) // 必选 -> 必选
	expectSubtype(t, r,
		subtype.Obj(subtype.Opt("x", subtype.Int{})),
		subtype.Obj(subtype.P("x", subtype.Int{})),
		false) // 可选不能满足必选
	expectSubtype(t, r,
		subtype.Obj(),
		subtype.Obj(subtype.P("x", subtype.Int{})),
		false) // 缺失不能满足必选
	// T 可选：S 中可缺失、可选或必选。
	expectSubtype(t, r,
		subtype.Obj(),
		subtype.Obj(subtype.Opt("x", subtype.Int{})),
		true) // 缺失可以满足可选
	expectSubtype(t, r,
		subtype.Obj(subtype.Opt("x", subtype.Int{})),
		subtype.Obj(subtype.Opt("x", subtype.Int{})),
		true) // 可选 -> 可选
	expectSubtype(t, r,
		subtype.Obj(subtype.P("x", subtype.Int{})),
		subtype.Obj(subtype.Opt("x", subtype.Int{})),
		true) // 必选 -> 可选
}

func TestReadOnlyProps(t *testing.T) {
	r := subtype.NewRegistry()
	// T 只读：S 中该属性是否只读不限，类型只需是子类型（单向放宽）。
	expectSubtype(t, r,
		subtype.Obj(subtype.P("x", subtype.Int{})),
		subtype.Obj(subtype.RO("x", subtype.Float{})),
		true) // S 可写也能满足 T 只读，int <: float
	expectSubtype(t, r,
		subtype.Obj(subtype.RO("x", subtype.Int{})),
		subtype.Obj(subtype.RO("x", subtype.Float{})),
		true)
	expectSubtype(t, r,
		subtype.Obj(subtype.RO("x", subtype.Float{})),
		subtype.Obj(subtype.RO("x", subtype.Int{})),
		false)
	// T 可写：S 中该属性必须也可写。
	expectSubtype(t, r,
		subtype.Obj(subtype.RO("x", subtype.Int{})),
		subtype.Obj(subtype.P("x", subtype.Int{})),
		false)
	// T 可写：两侧属性类型必须互为子类型（不变，而非协变）。
	expectSubtype(t, r,
		subtype.Obj(subtype.P("x", subtype.Int{})),
		subtype.Obj(subtype.P("x", subtype.Float{})),
		false) // int <: float 但 float 不 <: int
	expectSubtype(t, r,
		subtype.Obj(subtype.P("x", subtype.Float{})),
		subtype.Obj(subtype.P("x", subtype.Int{})),
		false)
	expectSubtype(t, r,
		subtype.Obj(subtype.P("x", subtype.Int{})),
		subtype.Obj(subtype.P("x", subtype.Int{})),
		true)
	// 可写属性的不变性：宽度子类型在可写属性上不成立。
	expectSubtype(t, r,
		subtype.Obj(subtype.P("p", subtype.Obj(subtype.P("a", subtype.Int{}), subtype.P("b", subtype.Str{})))),
		subtype.Obj(subtype.P("p", subtype.Obj(subtype.P("a", subtype.Int{})))),
		false)
	// 只读与可选组合：T 可选只读，S 中属性可写也算满足。
	expectSubtype(t, r,
		subtype.Obj(subtype.P("x", subtype.Int{})),
		subtype.Obj(subtype.OptRO("x", subtype.Float{})),
		true)
}

func TestFuncSubtyping(t *testing.T) {
	r := subtype.NewRegistry()
	// 参数个数：S 的参数不多于 T 即可。
	expectSubtype(t, r,
		subtype.Fn(subtype.Int{}, subtype.Int{}),
		subtype.Fn(subtype.Int{}, subtype.Int{}, subtype.Int{}),
		true) // S 参数更少，成立
	expectSubtype(t, r,
		subtype.Fn(subtype.Int{}, subtype.Int{}, subtype.Int{}),
		subtype.Fn(subtype.Int{}, subtype.Int{}),
		false) // S 参数更多，不成立
	expectSubtype(t, r,
		subtype.Fn(subtype.Int{}),
		subtype.Fn(subtype.Int{}, subtype.Int{}),
		true) // 无参函数可赋给任意参数列表
	// 参数逆变：T 的参数类型须是 S 对应参数类型的子类型。
	expectSubtype(t, r,
		subtype.Fn(subtype.Int{}, subtype.Float{}),
		subtype.Fn(subtype.Int{}, subtype.Int{}),
		true) // S 接受 float（更宽），T 传 int，成立
	expectSubtype(t, r,
		subtype.Fn(subtype.Int{}, subtype.Int{}),
		subtype.Fn(subtype.Int{}, subtype.Float{}),
		false) // S 只接受 int，T 可能传 float，不成立
	// 返回值协变。
	expectSubtype(t, r,
		subtype.Fn(subtype.Int{}),
		subtype.Fn(subtype.Float{}),
		true)
	expectSubtype(t, r,
		subtype.Fn(subtype.Float{}),
		subtype.Fn(subtype.Int{}),
		false)
	// 函数与非函数不相关。
	expectSubtype(t, r, subtype.Fn(subtype.Int{}), subtype.Int{}, false)
	expectSubtype(t, r, subtype.Fn(subtype.Int{}), subtype.Obj(), false)
}

func TestUnionSubtyping(t *testing.T) {
	r := subtype.NewRegistry()
	// 左侧联合：每个成员都须是右侧的子类型。
	expectSubtype(t, r,
		subtype.Or(subtype.Int{}, subtype.Str{}),
		subtype.Or(subtype.Int{}, subtype.Str{}, subtype.Bool{}),
		true)
	expectSubtype(t, r,
		subtype.Or(subtype.Int{}, subtype.Bool{}),
		subtype.Or(subtype.Int{}, subtype.Str{}),
		false)
	// 右侧联合：存在某个成员即可。
	expectSubtype(t, r, subtype.Int{}, subtype.Or(subtype.Str{}, subtype.Int{}), true)
	expectSubtype(t, r, subtype.Int{}, subtype.Or(subtype.Str{}, subtype.Bool{}), false)
	// 非对称性：(int|float) <: float 成立，但 float 不 <: (int|float) 之外的反向不成立情形。
	expectSubtype(t, r,
		subtype.Or(subtype.Int{}, subtype.Float{}),
		subtype.Float{},
		true) // int <: float 且 float <: float
	expectSubtype(t, r,
		subtype.Float{},
		subtype.Or(subtype.Int{}, subtype.Str{}),
		false)
	// 联合与顶/底组合。
	expectSubtype(t, r, subtype.Or(subtype.Int{}, subtype.Str{}), subtype.Top{}, true)
	expectSubtype(t, r, subtype.Bottom{}, subtype.Or(subtype.Int{}), true)
	// 嵌套联合按规则逐层展开。
	expectSubtype(t, r,
		subtype.Or(subtype.Or(subtype.Int{}, subtype.Str{}), subtype.Bool{}),
		subtype.Or(subtype.Bool{}, subtype.Or(subtype.Int{}, subtype.Str{})),
		true)
	// 明确规定的取舍：不把对象成员拆开分配后再比较。
	// {a: int} | {b: int} 不是 {a?: int, b?: int} 的子类型按成员逐一看：
	// {a: int} <: {a?: int, b?: int} 成立（可选属性可缺失），因此整体成立；
	// 但反向 {a?: int, b?: int} <: {a: int} | {b: int} 不成立，
	// 因为左侧任一成员都不是 {a: int} 或 {b: int} 的子类型。
	expectSubtype(t, r,
		subtype.Or(
			subtype.Obj(subtype.P("a", subtype.Int{})),
			subtype.Obj(subtype.P("b", subtype.Int{}))),
		subtype.Obj(subtype.Opt("a", subtype.Int{}), subtype.Opt("b", subtype.Int{})),
		true)
	expectSubtype(t, r,
		subtype.Obj(subtype.Opt("a", subtype.Int{}), subtype.Opt("b", subtype.Int{})),
		subtype.Or(
			subtype.Obj(subtype.P("a", subtype.Int{})),
			subtype.Obj(subtype.P("b", subtype.Int{}))),
		false)
}

func TestMutualRecursionGreatestFixpoint(t *testing.T) {
	r := subtype.NewRegistry()
	// 相互递归：A = {next: A}，B = {next: B}。
	// 归纳（最小）解会判定不成立，最大解判定成立。
	mustRegister(t, r, "A", subtype.Obj(subtype.P("next", subtype.Ref{Name: "A"})))
	mustRegister(t, r, "B", subtype.Obj(subtype.P("next", subtype.Ref{Name: "B"})))
	expectSubtype(t, r, subtype.Ref{Name: "A"}, subtype.Ref{Name: "B"}, true)
	expectSubtype(t, r, subtype.Ref{Name: "B"}, subtype.Ref{Name: "A"}, true)

	// B2 比 A 多一个必选属性，且 next 为可写：可写属性要求两侧类型互为子类型，
	// 而 A <: B2 因缺少 extra 不成立，故两个方向都不成立（不变性的直接后果）。
	mustRegister(t, r, "B2", subtype.Obj(
		subtype.P("next", subtype.Ref{Name: "B2"}),
		subtype.P("extra", subtype.Int{})))
	expectSubtype(t, r, subtype.Ref{Name: "B2"}, subtype.Ref{Name: "A"}, false)
	expectSubtype(t, r, subtype.Ref{Name: "A"}, subtype.Ref{Name: "B2"}, false)

	// next 改为只读后恢复宽度子类型：RB2（多一个属性） <: RA 成立，反向不成立。
	mustRegister(t, r, "RA", subtype.Obj(subtype.RO("next", subtype.Ref{Name: "RA"})))
	mustRegister(t, r, "RB2", subtype.Obj(
		subtype.RO("next", subtype.Ref{Name: "RB2"}),
		subtype.P("extra", subtype.Int{})))
	expectSubtype(t, r, subtype.Ref{Name: "RB2"}, subtype.Ref{Name: "RA"}, true)
	expectSubtype(t, r, subtype.Ref{Name: "RA"}, subtype.Ref{Name: "RB2"}, false)
}

func TestChainRecursion(t *testing.T) {
	r := subtype.NewRegistry()
	// 链式递归：A -> B -> C -> A，D -> E -> F -> D，结构同构。
	mustRegister(t, r, "A", subtype.Obj(subtype.P("x", subtype.Ref{Name: "B"})))
	mustRegister(t, r, "B", subtype.Obj(subtype.P("x", subtype.Ref{Name: "C"})))
	mustRegister(t, r, "C", subtype.Obj(subtype.P("x", subtype.Ref{Name: "A"})))
	mustRegister(t, r, "D", subtype.Obj(subtype.P("x", subtype.Ref{Name: "E"})))
	mustRegister(t, r, "E", subtype.Obj(subtype.P("x", subtype.Ref{Name: "F"})))
	mustRegister(t, r, "F", subtype.Obj(subtype.P("x", subtype.Ref{Name: "D"})))
	expectSubtype(t, r, subtype.Ref{Name: "A"}, subtype.Ref{Name: "D"}, true)
	expectSubtype(t, r, subtype.Ref{Name: "D"}, subtype.Ref{Name: "A"}, true)

	// 链中一环不同：G -> H -> I -> G，其中 I 的属性类型为 str。
	mustRegister(t, r, "G", subtype.Obj(subtype.P("x", subtype.Ref{Name: "H"})))
	mustRegister(t, r, "H", subtype.Obj(subtype.P("x", subtype.Ref{Name: "I"})))
	mustRegister(t, r, "I", subtype.Obj(subtype.P("x", subtype.Str{})))
	expectSubtype(t, r, subtype.Ref{Name: "A"}, subtype.Ref{Name: "G"}, false)
	expectSubtype(t, r, subtype.Ref{Name: "G"}, subtype.Ref{Name: "A"}, false)
}

func TestRecursionThroughWritableProps(t *testing.T) {
	r := subtype.NewRegistry()
	// 可写属性上的递归：不变性要求两个方向的子类型判断，
	// 两个方向都适用“正在判定的同一对视为成立”规则。
	mustRegister(t, r, "A", subtype.Obj(subtype.P("next", subtype.Ref{Name: "A"})))
	mustRegister(t, r, "B", subtype.Obj(subtype.P("next", subtype.Ref{Name: "B"})))
	expectSubtype(t, r, subtype.Ref{Name: "A"}, subtype.Ref{Name: "B"}, true)

	// 可写属性递归但值类型不同：v 的类型须互为子类型。
	mustRegister(t, r, "W1", subtype.Obj(
		subtype.P("next", subtype.Ref{Name: "W1"}),
		subtype.P("v", subtype.Int{})))
	mustRegister(t, r, "W2", subtype.Obj(
		subtype.P("next", subtype.Ref{Name: "W2"}),
		subtype.P("v", subtype.Float{})))
	expectSubtype(t, r, subtype.Ref{Name: "W1"}, subtype.Ref{Name: "W2"}, false)
	expectSubtype(t, r, subtype.Ref{Name: "W2"}, subtype.Ref{Name: "W1"}, false)

	// 可写递归属性 + 不同的值类型：不变性沿递归链传播，两个方向都不成立。
	mustRegister(t, r, "V1", subtype.Obj(
		subtype.P("next", subtype.Ref{Name: "V1"}),
		subtype.RO("v", subtype.Int{})))
	mustRegister(t, r, "V2", subtype.Obj(
		subtype.P("next", subtype.Ref{Name: "V2"}),
		subtype.RO("v", subtype.Float{})))
	expectSubtype(t, r, subtype.Ref{Name: "V1"}, subtype.Ref{Name: "V2"}, false)
	expectSubtype(t, r, subtype.Ref{Name: "V2"}, subtype.Ref{Name: "V1"}, false)

	// 递归属性换成只读后恢复协变：int <: float 单向成立。
	mustRegister(t, r, "R1", subtype.Obj(
		subtype.RO("next", subtype.Ref{Name: "R1"}),
		subtype.RO("v", subtype.Int{})))
	mustRegister(t, r, "R2", subtype.Obj(
		subtype.RO("next", subtype.Ref{Name: "R2"}),
		subtype.RO("v", subtype.Float{})))
	expectSubtype(t, r, subtype.Ref{Name: "R1"}, subtype.Ref{Name: "R2"}, true)
	expectSubtype(t, r, subtype.Ref{Name: "R2"}, subtype.Ref{Name: "R1"}, false)
}

func TestRecursionThroughFunc(t *testing.T) {
	r := subtype.NewRegistry()
	// 经函数构造子的递归：参数逆变 + 共归纳假设。
	// F = (F) -> int，G = (G) -> int：相互为子类型。
	mustRegister(t, r, "F", subtype.Fn(subtype.Int{}, subtype.Ref{Name: "F"}))
	mustRegister(t, r, "G", subtype.Fn(subtype.Int{}, subtype.Ref{Name: "G"}))
	expectSubtype(t, r, subtype.Ref{Name: "F"}, subtype.Ref{Name: "G"}, true)
	expectSubtype(t, r, subtype.Ref{Name: "G"}, subtype.Ref{Name: "F"}, true)

	// 返回值不同则整链不成立。
	mustRegister(t, r, "H", subtype.Fn(subtype.Str{}, subtype.Ref{Name: "H"}))
	expectSubtype(t, r, subtype.Ref{Name: "F"}, subtype.Ref{Name: "H"}, false)
}

func TestForwardReference(t *testing.T) {
	r := subtype.NewRegistry()
	// 前向引用：登记时允许引用未登记的名字。
	mustRegister(t, r, "A", subtype.Obj(subtype.P("next", subtype.Ref{Name: "B"})))
	// 判定时 B 未定义：报未定义引用。
	_, err := r.Check(subtype.Ref{Name: "A"}, subtype.Ref{Name: "A"})
	if !errors.Is(err, subtype.ErrUndefinedReference) {
		t.Fatalf("期望未定义引用错误，得到 %v", err)
	}
	// 后补登记后判定成功。
	mustRegister(t, r, "B", subtype.Obj(subtype.P("next", subtype.Ref{Name: "A"})))
	expectSubtype(t, r, subtype.Ref{Name: "A"}, subtype.Ref{Name: "B"}, true)
}

// expectErrKind 断言判定返回指定类别的错误，并校验报告的名字。
func expectErrKind(t *testing.T, err error, sentinel error, wantName string) {
	t.Helper()
	if err == nil {
		t.Fatalf("期望错误 %v，得到 nil", sentinel)
	}
	if !errors.Is(err, sentinel) {
		t.Fatalf("期望错误类别 %v，得到 %v", sentinel, err)
	}
	var e *subtype.Error
	if !errors.As(err, &e) {
		t.Fatalf("错误不是 *subtype.Error: %v", err)
	}
	if e.Name != wantName {
		t.Fatalf("报告的名字 = %q，期望 %q（错误: %v）", e.Name, wantName, err)
	}
}

func TestUnguardedCycleDirect(t *testing.T) {
	r := subtype.NewRegistry()
	// 直接自环：A 展开回自身，不经过对象或函数构造子。
	mustRegister(t, r, "A", subtype.Ref{Name: "A"})
	_, err := r.Check(subtype.Ref{Name: "A"}, subtype.Int{})
	expectErrKind(t, err, subtype.ErrUnguardedCycle, "A")

	// 别名链：A = B，B = A。
	r2 := subtype.NewRegistry()
	mustRegister(t, r2, "A", subtype.Ref{Name: "B"})
	mustRegister(t, r2, "B", subtype.Ref{Name: "A"})
	_, err = r2.Check(subtype.Ref{Name: "A"}, subtype.Int{})
	expectErrKind(t, err, subtype.ErrUnguardedCycle, "A") // 字典序最小
}

func TestUnguardedCycleThroughUnion(t *testing.T) {
	r := subtype.NewRegistry()
	// 经联合的间接回环：A = B | int，B = A | str。
	mustRegister(t, r, "A", subtype.Or(subtype.Ref{Name: "B"}, subtype.Int{}))
	mustRegister(t, r, "B", subtype.Or(subtype.Ref{Name: "A"}, subtype.Str{}))
	_, err := r.Check(subtype.Ref{Name: "A"}, subtype.Int{})
	expectErrKind(t, err, subtype.ErrUnguardedCycle, "A")

	// 自联合：A = A | int。
	r2 := subtype.NewRegistry()
	mustRegister(t, r2, "A", subtype.Or(subtype.Ref{Name: "A"}, subtype.Int{}))
	_, err = r2.Check(subtype.Ref{Name: "A"}, subtype.Int{})
	expectErrKind(t, err, subtype.ErrUnguardedCycle, "A")

	// 有保护则合法：A = {self: A} | int 中引用经过对象构造子。
	r3 := subtype.NewRegistry()
	mustRegister(t, r3, "A", subtype.Or(
		subtype.Obj(subtype.P("self", subtype.Ref{Name: "A"})),
		subtype.Int{}))
	expectSubtype(t, r3, subtype.Ref{Name: "A"}, subtype.Top{}, true)

	// 经函数构造子也有保护：A = (A) -> int。
	r4 := subtype.NewRegistry()
	mustRegister(t, r4, "A", subtype.Fn(subtype.Int{}, subtype.Ref{Name: "A"}))
	expectSubtype(t, r4, subtype.Ref{Name: "A"}, subtype.Ref{Name: "A"}, true)
}

func TestUnguardedCycleOnlyWhenReachable(t *testing.T) {
	r := subtype.NewRegistry()
	// A 有无保护循环，但与本次判定无关（不可达），不应报错。
	mustRegister(t, r, "A", subtype.Ref{Name: "A"})
	mustRegister(t, r, "B", subtype.Obj(subtype.P("x", subtype.Int{})))
	expectSubtype(t, r, subtype.Ref{Name: "B"}, subtype.Ref{Name: "B"}, true)
}

func TestUndefinedReference(t *testing.T) {
	r := subtype.NewRegistry()
	// 直接引用未登记名字。
	_, err := r.Check(subtype.Ref{Name: "Z"}, subtype.Int{})
	expectErrKind(t, err, subtype.ErrUndefinedReference, "Z")

	// 静态可达但未定义的名字有多个时，报告字典序最小者。
	mustRegister(t, r, "A", subtype.Obj(
		subtype.P("x", subtype.Ref{Name: "Z"}),
		subtype.P("y", subtype.Ref{Name: "M"})))
	_, err = r.Check(subtype.Ref{Name: "A"}, subtype.Int{})
	expectErrKind(t, err, subtype.ErrUndefinedReference, "M")

	// 未定义引用只出现在右侧同样报错。
	_, err = r.Check(subtype.Int{}, subtype.Ref{Name: "Q"})
	expectErrKind(t, err, subtype.ErrUndefinedReference, "Q")

	// 静态可达的定义链中的未定义引用也要检查（而非只检查实际走到的部分）。
	mustRegister(t, r, "C", subtype.Obj(subtype.P("next", subtype.Ref{Name: "D"})))
	mustRegister(t, r, "D", subtype.Obj(subtype.P("deep", subtype.Ref{Name: "MISSING"})))
	_, err = r.Check(subtype.Ref{Name: "C"}, subtype.Obj())
	expectErrKind(t, err, subtype.ErrUndefinedReference, "MISSING")
}

func TestErrorPriority(t *testing.T) {
	// 登记：参数非法优先于重复定义。
	r := subtype.NewRegistry()
	mustRegister(t, r, "A", subtype.Obj(subtype.P("x", subtype.Int{})))
	err := r.Register("A", subtype.Obj(
		subtype.P("y", subtype.Int{}),
		subtype.P("y", subtype.Str{})))
	if !errors.Is(err, subtype.ErrInvalidArgument) {
		t.Fatalf("重复名字 + 非法定义应报参数非法，得到 %v", err)
	}

	// 判定：参数非法优先于未定义引用。
	dupObj := subtype.Obj(subtype.P("x", subtype.Int{}), subtype.P("x", subtype.Str{}))
	_, err = r.Check(dupObj, subtype.Ref{Name: "UNDEF"})
	if !errors.Is(err, subtype.ErrInvalidArgument) {
		t.Fatalf("非法表达式 + 未定义引用应报参数非法，得到 %v", err)
	}

	// 判定：未定义引用优先于无保护循环。
	r2 := subtype.NewRegistry()
	mustRegister(t, r2, "LOOP", subtype.Ref{Name: "LOOP"})
	mustRegister(t, r2, "WITHUNDEF", subtype.Obj(subtype.P("u", subtype.Ref{Name: "NOPE"})))
	_, err = r2.Check(subtype.Ref{Name: "LOOP"}, subtype.Ref{Name: "WITHUNDEF"})
	expectErrKind(t, err, subtype.ErrUndefinedReference, "NOPE")
}

func TestInvalidArgument(t *testing.T) {
	r := subtype.NewRegistry()
	// 空名字。
	if err := r.Register("", subtype.Int{}); !errors.Is(err, subtype.ErrInvalidArgument) {
		t.Fatalf("空名字应报参数非法，得到 %v", err)
	}
	// 定义内对象属性名重复（含嵌套）。
	dupDef := subtype.Obj(
		subtype.P("x", subtype.Int{}),
		subtype.P("x", subtype.Int{})) // 完全重复也非法
	if err := r.Register("A", dupDef); !errors.Is(err, subtype.ErrInvalidArgument) {
		t.Fatalf("属性名重复应报参数非法，得到 %v", err)
	}
	nestedDup := subtype.Obj(
		subtype.P("nested", subtype.Obj(
			subtype.P("y", subtype.Int{}),
			subtype.P("y", subtype.Str{}))))
	if err := r.Register("B", nestedDup); !errors.Is(err, subtype.ErrInvalidArgument) {
		t.Fatalf("嵌套对象属性名重复应报参数非法，得到 %v", err)
	}
	// nil 定义。
	if err := r.Register("C", nil); !errors.Is(err, subtype.ErrInvalidArgument) {
		t.Fatalf("nil 定义应报参数非法，得到 %v", err)
	}
	// 判定时 nil 类型表达式。
	if _, err := r.Check(nil, subtype.Int{}); !errors.Is(err, subtype.ErrInvalidArgument) {
		t.Fatalf("nil 左类型应报参数非法，得到 %v", err)
	}
	if _, err := r.Check(subtype.Int{}, nil); !errors.Is(err, subtype.ErrInvalidArgument) {
		t.Fatalf("nil 右类型应报参数非法，得到 %v", err)
	}
}

func TestRejectedRegisterKeepsState(t *testing.T) {
	r := subtype.NewRegistry()
	mustRegister(t, r, "A", subtype.Obj(subtype.P("x", subtype.Int{})))
	// 重复登记被拒绝。
	if err := r.Register("A", subtype.Obj(subtype.P("y", subtype.Str{}))); !errors.Is(err, subtype.ErrDuplicateDefinition) {
		t.Fatalf("重复定义应报重复定义错误，得到 %v", err)
	}
	// 非法登记被拒绝。
	_ = r.Register("B", nil)
	// 状态未被改变：A 仍是原定义，B 未登记。
	expectSubtype(t, r, subtype.Ref{Name: "A"}, subtype.Obj(subtype.P("x", subtype.Int{})), true)
	expectSubtype(t, r, subtype.Ref{Name: "A"}, subtype.Obj(subtype.P("y", subtype.Str{})), false)
	_, err := r.Check(subtype.Ref{Name: "B"}, subtype.Int{})
	expectErrKind(t, err, subtype.ErrUndefinedReference, "B")
}

// TestAssumptionInvalidation 验证：在假设 (A,B) 下得出的中间结论 (C,D)=true，
// 在 (A,B) 被推翻后不得被错误复用。
//
//	A = {c: C, i: int}
//	B = {ro c: D, i: str}
//	C = {a: A}
//	D = {ro a: B}
//
// 判定 C <: ({ro a: B} | D)：
//   - 第一分支检查 C <: {ro a: B}，触发 (A,B) 的判定；其中 B 的只读属性 c
//     使 (C,D) 在假设 (A,B) 下被判定为 true 并缓存；随后 B 的属性 i
//     （int 与 str 不互为子类型）推翻 (A,B)，(C,D) 的缓存必须作废。
//   - 第二分支检查 C <: D：正确答案是 false（因为 (A,B) 为 false，
//     而 D 的只读属性 a 要求 A <: B）。若错误复用了缓存的 (C,D)=true，
//     整个判定会错误地返回 true。
func TestAssumptionInvalidation(t *testing.T) {
	r := subtype.NewRegistry()
	mustRegister(t, r, "A", subtype.Obj(
		subtype.P("c", subtype.Ref{Name: "C"}),
		subtype.P("i", subtype.Int{})))
	mustRegister(t, r, "B", subtype.Obj(
		subtype.RO("c", subtype.Ref{Name: "D"}),
		subtype.P("i", subtype.Str{})))
	mustRegister(t, r, "C", subtype.Obj(
		subtype.P("a", subtype.Ref{Name: "A"})))
	mustRegister(t, r, "D", subtype.Obj(
		subtype.RO("a", subtype.Ref{Name: "B"})))

	right := subtype.Or(
		subtype.Obj(subtype.RO("a", subtype.Ref{Name: "B"})),
		subtype.Ref{Name: "D"})
	expectSubtype(t, r, subtype.Ref{Name: "C"}, right, false)
	// 对照：直接判定 C <: D 同样为 false。
	expectSubtype(t, r, subtype.Ref{Name: "C"}, subtype.Ref{Name: "D"}, false)
}

func TestDeterminism(t *testing.T) {
	r := subtype.NewRegistry()
	mustRegister(t, r, "A", subtype.Obj(
		subtype.RO("next", subtype.Ref{Name: "B"}),
		subtype.P("v", subtype.Int{})))
	mustRegister(t, r, "B", subtype.Obj(
		subtype.RO("next", subtype.Ref{Name: "A"}),
		subtype.RO("w", subtype.Float{})))
	first, err := r.Check(subtype.Ref{Name: "A"}, subtype.Ref{Name: "B"})
	if err != nil {
		t.Fatal(err)
	}
	// 同一对类型反复判定结果必须一致。
	for i := 0; i < 100; i++ {
		ok, err := r.Check(subtype.Ref{Name: "A"}, subtype.Ref{Name: "B"})
		if err != nil {
			t.Fatal(err)
		}
		if ok != first {
			t.Fatalf("第 %d 次判定结果 %v 与首次 %v 不一致", i, ok, first)
		}
	}
}

func TestConcurrency(t *testing.T) {
	r := subtype.NewRegistry()
	mustRegister(t, r, "A", subtype.Obj(subtype.RO("next", subtype.Ref{Name: "A"})))
	mustRegister(t, r, "B", subtype.Obj(subtype.RO("next", subtype.Ref{Name: "B"})))

	var wg sync.WaitGroup
	errs := make(chan error, 400)
	// 一半 goroutine 并发判定，一半并发登记新类型。
	for i := 0; i < 100; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			ok, err := r.Check(subtype.Ref{Name: "A"}, subtype.Ref{Name: "B"})
			if err != nil {
				errs <- err
				return
			}
			if !ok {
				errs <- errors.New("A <: B 应为 true")
			}
		}()
		go func(n int) {
			defer wg.Done()
			name := "T" + string(rune('0'+n%10)) + string(rune('0'+n/10))
			def := subtype.Obj(subtype.RO("self", subtype.Ref{Name: name}))
			if err := r.Register(name, def); err != nil &&
				!errors.Is(err, subtype.ErrDuplicateDefinition) {
				errs <- err
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
}

// TestPairBound 验证命名对检查数量的上界：不同命名对数量
// 不得超过两侧可达命名类型并集的两两组合总数。
func TestPairBound(t *testing.T) {
	r := subtype.NewRegistry()
	// 构造一条带可写属性（会触发双向检查）的相互递归链。
	names := []string{"N0", "N1", "N2", "N3"}
	for i, n := range names {
		next := names[(i+1)%len(names)]
		mustRegister(t, r, n, subtype.Obj(
			subtype.P("next", subtype.Ref{Name: next}),
			subtype.RO("v", subtype.Int{})))
	}
	ok, stats, err := r.CheckWithStats(subtype.Ref{Name: "N0"}, subtype.Ref{Name: "N2"})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("判定结果 %v，统计: %+v，上界 %d", ok, stats, stats.PairBound())
	if stats.DistinctPairs > stats.PairBound() {
		t.Fatalf("不同命名对数量 %d 超过上界 %d", stats.DistinctPairs, stats.PairBound())
	}
	if stats.DistinctPairs == 0 {
		t.Fatal("统计信息未记录任何命名对")
	}
}

package constexpr

import "testing"

func TestF64ChainAndTypedString(t *testing.T) {
	// 有类型 f64：每步保留精确分数，最后舍入。
	add := Binary(OpAdd,
		Conv("f64", RatLit(br("1/10"))),
		Conv("f64", RatLit(br("2/10"))))
	v := mustEval(t, add)
	if v.Type() != CTypeF64 || v.Rat().Cmp(br("3/10")) != 0 {
		t.Fatalf("f64 1/10+2/10 精确载荷应为 3/10，得到 %v", v.Rat())
	}
	if v.Float64() != 0.3 {
		t.Fatalf("最终双精度应为 0.3，得到 %v", v.Float64())
	}
	// 有类型字符串相加。
	s := mustEval(t, Binary(OpAdd,
		Conv("string", StringLit("a")),
		Conv("string", StringLit("b"))))
	if s.Type() != CTypeString || s.String() != "ab" {
		t.Fatalf("有类型字符串相加错误: %v", s)
	}
	// 无类型字符串与有类型字符串比较。
	c := mustEval(t, Binary(OpEq, StringLit("ab"),
		Conv("string", StringLit("ab"))))
	if !c.Bool() {
		t.Fatalf("无类型串与有类型串应可比较且相等")
	}
}

func TestRegistryValueFixedAtRegistration(t *testing.T) {
	// 命名常量在登记时求定并固定；登记的值即使表达式节点被复用也不受影响。
	r := NewRegistry()
	if err := r.Register("a", IntLit(bi("10"))); err != nil {
		t.Fatal(err)
	}
	v1, _ := r.Lookup("a")
	if err := r.Register("b", Binary(OpMul, Ref("a"), IntLit(bi("2")))); err != nil {
		t.Fatal(err)
	}
	v2, _ := r.Lookup("b")
	if v2.Int().Int64() != 20 {
		t.Fatalf("b 应为 20，得到 %s", v2.Int())
	}
	// 修改 Lookup 返回的拷贝不影响内部状态。
	v2.Int().SetInt64(999)
	v2again, _ := r.Lookup("b")
	if v2again.Int().Int64() != 20 {
		t.Fatalf("内部状态被外部拷贝污染: %s", v2again.Int())
	}
	_ = v1
}

func TestRegistryChainDepthO1(t *testing.T) {
	// 构造长引用链，引用最深节点仍是一次查找（值在登记时固定）。
	r := NewRegistry()
	if err := r.Register("n0", IntLit(bi("0"))); err != nil {
		t.Fatal(err)
	}
	const depth = 300
	for i := 1; i <= depth; i++ {
		prev := "n" + itoa(i-1)
		cur := "n" + itoa(i)
		if err := r.Register(cur, Binary(OpAdd, Ref(prev), IntLit(bi("1")))); err != nil {
			t.Fatalf("登记 %s: %v", cur, err)
		}
	}
	v := mustEvalOn(t, r.NewEvaluator(), Ref("n"+itoa(depth)))
	if v.Int().Int64() != depth {
		t.Fatalf("n%d 应为 %d，得到 %s", depth, depth, v.Int())
	}
}

func TestRegisterRejectionAtomic(t *testing.T) {
	r := NewRegistry()
	r.Register("ok", IntLit(bi("1")))
	before := r.Len()
	// 引用不存在的名字：拒绝且状态不变。
	err := r.Register("bad", Binary(OpAdd, Ref("ghost"), IntLit(bi("1"))))
	if codeOf(err) != EvalUnknownName {
		t.Fatalf("应报未知名字，得到 %v", err)
	}
	if r.Len() != before {
		t.Fatalf("被拒绝登记改变了状态")
	}
	// 求值错误：拒绝且状态不变。
	err = r.Register("bad2", Binary(OpDiv, IntLit(bi("1")), IntLit(bi("0"))))
	if codeOf(err) != EvalDivideByZero {
		t.Fatalf("应报除零，得到 %v", err)
	}
	if r.Len() != before {
		t.Fatalf("被拒绝登记改变了状态")
	}
}

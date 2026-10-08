package constexpr

import (
	"sync"
	"testing"
)

func TestRegistryBasicAndPriorities(t *testing.T) {
	r := NewRegistry()
	// 参数非法（空名字）优先于重复登记。
	if err := r.Register("", IntLit(bi("1"))); err == nil {
		t.Fatal("空名字应拒绝")
	}
	// 未知类型名是参数非法。
	if err := r.RegisterTyped("x", "i128", IntLit(bi("1"))); err == nil {
		t.Fatal("未知类型名应拒绝")
	}
	// 正常登记。
	if err := r.Register("w", IntLit(bi("40"))); err != nil {
		t.Fatalf("登记 w 失败: %v", err)
	}
	// 重复登记可区分。
	err := r.Register("w", IntLit(bi("1")))
	if ee, ok := err.(*EvalError); !ok || ee.Code != EvalDuplicateName {
		t.Fatalf("重复登记应报 duplicate，实际 %v", err)
	}
	// 引用未登记名字可区分。
	err = r.Register("bad", Binary(OpAdd, Ref("nope"), IntLit(bi("1"))))
	if ee, ok := err.(*EvalError); !ok || ee.Code != EvalUnknownName {
		t.Fatalf("未知名字应报 unknown-name，实际 %v", err)
	}
	// 求值错误优先级低于未知名字。
	err = r.Register("bad2", Binary(OpAdd, Ref("nope"), Binary(OpDiv, IntLit(bi("1")), IntLit(bi("0")))))
	if ee, ok := err.(*EvalError); !ok || ee.Code != EvalUnknownName {
		t.Fatalf("未知名字应先于求值错误，实际 %v", err)
	}
	// 被拒绝的登记不改变状态：Len 仍为 1，且 bad/bad2 不可引用。
	if r.Len() != 1 {
		t.Fatalf("被拒绝登记不应改变状态，Len=%d", r.Len())
	}
	if _, ok := r.Lookup("bad"); ok {
		t.Fatal("bad 不应存在")
	}
	// 命名常量可被后续表达式引用。
	if err := r.Register("answer", Binary(OpAdd, Ref("w"), IntLit(bi("2")))); err != nil {
		t.Fatalf("登记 answer: %v", err)
	}
	v := mustEvalOn(t, r.NewEvaluator(), Ref("answer"))
	if v.Int().Int64() != 42 {
		t.Fatalf("answer 应为 42，得到 %s", v.Int())
	}
}

func TestRegistryTypedAndDefaultConv(t *testing.T) {
	r := NewRegistry()
	// 显式类型登记：值固定为有类型。
	if err := r.RegisterTyped("x", "i8", IntLit(bi("127"))); err != nil {
		t.Fatal(err)
	}
	v, ok := r.Lookup("x")
	if !ok || !v.IsTyped() || v.Type() != CTypeI8 {
		t.Fatalf("x 应为 i8，得到 %v", v)
	}
	// 超界登记被拒。
	if err := r.RegisterTyped("y", "i8", IntLit(bi("128"))); err == nil {
		t.Fatal("128 不应登记为 i8")
	}
	// 无类型命名常量保留种类；默认转换语境取 i64 / f64 / bool / string。
	r.Register("k", IntLit(bi("5")))
	v = mustEvalOn(t, r.NewEvaluator(), DefaultConv(Ref("k")))
	if !v.IsTyped() || v.Type() != CTypeI64 {
		t.Fatalf("整数默认类型应为 i64，得到 %v", v.Type())
	}
	r.Register("q", RatLit(br("5/2")))
	v = mustEvalOn(t, r.NewEvaluator(), DefaultConv(Ref("q")))
	if v.Type() != CTypeF64 {
		t.Fatalf("有理数默认类型应为 f64，得到 %v", v.Type())
	}
	r.Register("flag", BoolLit(true))
	v = mustEvalOn(t, r.NewEvaluator(), DefaultConv(Ref("flag")))
	if v.Type() != CTypeBool {
		t.Fatalf("布尔默认类型应为 bool，得到 %v", v.Type())
	}
}

func TestStructureInvalid(t *testing.T) {
	// 运算符与操作数个数不符。
	wantCode(t, &Expr{Op: OpAdd, Operands: []*Expr{IntLit(bi("1"))}}, EvalInvalidArgument)
	wantCode(t, &Expr{Op: OpNeg, Operands: nil}, EvalInvalidArgument)
	// 未知运算符。
	wantCode(t, &Expr{Op: Op(255)}, EvalInvalidArgument)
	// 引用空名。
	wantCode(t, Ref(""), EvalInvalidArgument)
	// 未知类型名。
	wantCode(t, Conv("i999", IntLit(bi("1"))), EvalInvalidArgument)
	// 空节点。
	wantCode(t, nil, EvalInvalidArgument)
	// 字面量缺种类。
	wantCode(t, &Expr{Op: OpLit}, EvalInvalidArgument)
	// 结构非法优先于未知名字与求值错误。
	r := NewRegistry()
	err := r.Register("z", Binary(OpAdd, Ref("ghost"), &Expr{Op: Op(254)}))
	if ee, ok := err.(*EvalError); !ok || ee.Code != EvalInvalidArgument {
		t.Fatalf("结构非法应最先报告，实际 %v", err)
	}
}

func TestRegistryConcurrent(t *testing.T) {
	r := NewRegistry()
	r.Register("base", IntLit(bi("1")))
	var wg sync.WaitGroup
	for g := 0; g < 16; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				name := ""
				// 一半读，一半尝试写（大部分写因重复被拒，等价于某串行顺序）。
				if i%2 == 0 {
					v, ok := r.Lookup("base")
					if !ok || v.Int().Int64() != 1 {
						t.Errorf("并发读到异常值")
						return
					}
					if _, err := r.NewEvaluator().Eval(Ref("base")); err != nil {
						t.Errorf("并发求值失败: %v", err)
						return
					}
				} else {
					name = "dup"
					_ = r.Register(name, IntLit(bi("1")))
				}
			}
		}(g)
	}
	wg.Wait()
}

package ontology

import "testing"

// 假分支对字面量与原子类型的不对称处理：
// x: number|1|"a"，x !== "a"：原子 number 保持，字面量 "a" 删除，数字字面量 1 保留。
func TestFalseBranchLiteralAsymmetry(t *testing.T) {
	l := newLogger(t, "false-literal-asymmetry")
	defer l.finish()
	p := &Program{
		Decls: map[string]*Type{"x": TUnion(TNumber(), TNumLit(1), TStrLit("a"))},
		Stmts: []*Stmt{
			{ID: "if1", K: StmtIf, Cond: &Cond{K: CondEqLiteral, Var: "x", Lit: TStrLit("a")},
				Else: []*Stmt{{ID: "probe", K: StmtReturn}}},
		},
	}
	l.input(`decl x = number|1|"a"; if (x === "a") {} else return;`)
	r := mustAnalyze(t, p, l.tag)
	expectTypeAt(t, r, "probe", "x", "[0:number]", l)
	l.reason(`为假时只删除相同字面量 "a"，原子 number 不被收窄，数字字面量 1 保留`)
}

// 真分支：字面量成员只留相同字面量，原子字符串收窄为该字面量。
func TestTrueBranchLiteralNarrowing(t *testing.T) {
	l := newLogger(t, "true-literal")
	defer l.finish()
	p := &Program{
		Decls: map[string]*Type{"x": TUnion(TString(), TStrLit("a"), TStrLit("b"))},
		Stmts: []*Stmt{
			{ID: "if1", K: StmtIf, Cond: &Cond{K: CondEqLiteral, Var: "x", Lit: TStrLit("a")},
				Then: []*Stmt{{ID: "probe", K: StmtReturn}}},
		},
	}
	l.input(`decl x = string|"a"|"b"; if (x === "a") return;`)
	r := mustAnalyze(t, p, l.tag)
	expectTypeAt(t, r, "probe", "x", "[7:s:a]", l)
	l.reason(`原子 string 收窄为 "a"，既有 "a" 去重，"b" 删除，单成员联合等同字面量`)
}

// 布尔的拆分：boolean 是 true|false，===true 的假分支留下 false。
func TestBooleanSplit(t *testing.T) {
	l := newLogger(t, "boolean-split")
	defer l.finish()
	p := &Program{
		Decls: map[string]*Type{"b": TBoolean()},
		Stmts: []*Stmt{
			{ID: "if1", K: StmtIf, Cond: &Cond{K: CondEqLiteral, Var: "b", Lit: TTrue()},
				Else: []*Stmt{{ID: "probe", K: StmtReturn}}},
		},
	}
	l.input("decl b = boolean; if (b === true) {} else return;")
	r := mustAnalyze(t, p, l.tag)
	expectTypeAt(t, r, "probe", "b", "[3:false]", l)
	l.reason("布尔由两字面量构成，===true 为假只删去 true，false 保留")
}

// 真值判断对数字与字符串字面量/原子的收窄。
func TestTruthiness(t *testing.T) {
	l := newLogger(t, "truthiness")
	defer l.finish()
	p := &Program{
		Decls: map[string]*Type{
			"n": TUnion(TNumber(), TNumLit(0), TNumLit(1)),
			"s": TUnion(TString(), TStrLit(""), TStrLit("x")),
			"z": TUnion(TNull(), TUndefined(), TFalse(), TTrue(), TNumLit(0), TStrLit(""),
				TObject(map[string]*Type{})),
		},
		Stmts: []*Stmt{
			{ID: "ifT", K: StmtIf, Cond: &Cond{K: CondTruthy, Var: "n"},
				Then: []*Stmt{{ID: "tN", K: StmtReturn}}},
			{ID: "ifF", K: StmtIf, Cond: &Cond{K: CondTruthy, Var: "s"},
				Else: []*Stmt{{ID: "fS", K: StmtReturn}}},
			{ID: "ifZ1", K: StmtIf, Cond: &Cond{K: CondTruthy, Var: "z"},
				Then: []*Stmt{{ID: "tZ", K: StmtReturn}}},
			{ID: "ifZ2", K: StmtIf, Cond: &Cond{K: CondTruthy, Var: "z"},
				Else: []*Stmt{{ID: "fZ", K: StmtReturn}}},
		},
	}
	l.input(`混合 number|0|1、string|""|"x" 与 null/undefined/false/true/object 做真值判断`)
	r := mustAnalyze(t, p, l.tag)
	expectTypeAt(t, r, "tN", "n", "[0:number]", l)
	l.reason("真分支删除字面量 0，原子 number 保留并吸收字面量 1，规范化后为纯 number")
	// 额外验证无原子吸收时字面量 1 确实保留：
	p1b := &Program{
		Decls: map[string]*Type{"n2": TUnion(TNumLit(0), TNumLit(1))},
		Stmts: []*Stmt{{ID: "i", K: StmtIf, Cond: &Cond{K: CondTruthy, Var: "n2"},
			Then: []*Stmt{{ID: "p", K: StmtReturn}}}},
	}
	l.input("补充: n2=0|1 真值判断为真")
	r1b := mustAnalyze(t, p1b, l.tag)
	expectTypeAt(t, r1b, "p", "n2", "[6:n:1]", l)
	expectTypeAt(t, r, "fS", "s", "[7:s:]", l)
	expectTypeAt(t, r, "tZ", "z", "[2:true|8:o:{}]", l)
	expectTypeAt(t, r, "fZ", "z", "[3:false|4:null|5:undefined|6:n:0|7:s:]", l)
	l.reason("真分支删 0/空串/null/undefined/false，原子 number|string 与对象保留；假分支原子 number->0, string->空串，对象删除")
}

// 宽松相等双命中 null/undefined。
func TestLooseEqualityNull(t *testing.T) {
	l := newLogger(t, "loose-null")
	defer l.finish()
	p := &Program{
		Decls: map[string]*Type{"x": TUnion(TNull(), TUndefined(), TNumber(), TNumLit(1))},
		Stmts: []*Stmt{
			{ID: "if1", K: StmtIf, Cond: &Cond{K: CondLooseNull, Var: "x"},
				Then: []*Stmt{{ID: "t", K: StmtReturn}},
				Else: []*Stmt{{ID: "f", K: StmtReturn}}},
		},
	}
	l.input("decl x = null|undefined|number|1; if (x == null) return; else return;")
	r := mustAnalyze(t, p, l.tag)
	expectTypeAt(t, r, "t", "x", "[4:null|5:undefined]", l)
	expectTypeAt(t, r, "f", "x", "[0:number]", l)
	l.reason("宽松相等同时命中 null 与 undefined；假分支两者皆删，原子 number 吸收字面量 1")
}

// 严格相等 null 不命中对象；typeof object 同时保留 null。
func TestStrictNullVsObject(t *testing.T) {
	l := newLogger(t, "strict-null")
	defer l.finish()
	p := &Program{
		Decls: map[string]*Type{"x": TUnion(TNull(), TObject(map[string]*Type{"k": TString()}))},
		Stmts: []*Stmt{
			{ID: "if1", K: StmtIf, Cond: &Cond{K: CondEqNull, Var: "x"},
				Then: []*Stmt{{ID: "t", K: StmtReturn}}},
			{ID: "if2", K: StmtIf, Cond: &Cond{K: CondIsType, Var: "x", Check: CheckObject},
				Then: []*Stmt{{ID: "o", K: StmtReturn}}},
		},
	}
	l.input(`decl x = null|{k:string}; if (x===null) return; if (typeof x === "object") return;`)
	r := mustAnalyze(t, p, l.tag)
	expectTypeAt(t, r, "t", "x", "[4:null]", l)
	expectTypeAt(t, r, "o", "x", `[8:o:{"k":[1:string]}]`, l)
	l.reason(`严格相等只命中 null 且真分支返回；汇合后的存活路径只剩对象，故 typeof object 点只剩对象`)
}

// 判别属性的真假环境。
func TestDiscriminant(t *testing.T) {
	l := newLogger(t, "discriminant")
	defer l.finish()
	objA := TObject(map[string]*Type{"kind": TStrLit("a"), "v": TNumber()})
	objB := TObject(map[string]*Type{"kind": TStrLit("b"), "v": TString()})
	objC := TObject(map[string]*Type{"kind": TUnion(TStrLit("a"), TStrLit("c")), "v": TBoolean()})
	p := &Program{
		Decls: map[string]*Type{"e": TUnion(objA, objB, objC)},
		Stmts: []*Stmt{
			{ID: "if1", K: StmtIf,
				Cond: &Cond{K: CondDiscrim, Var: "e", Prop: "kind", Lit: TStrLit("a")},
				Then: []*Stmt{{ID: "t", K: StmtReturn}},
				Else: []*Stmt{{ID: "f", K: StmtReturn}}},
		},
	}
	l.input(`decl e = {kind:"a"}|{kind:"b"}|{kind:"a"|"c"}; if (e.kind === "a")`)
	r := mustAnalyze(t, p, l.tag)
	expectTypeAt(t, r, "t", "e",
		`[8:o:{"kind":[7:s:a],"v":[0:number]}|8:o:{"kind":[7:s:a|7:s:c],"v":[2:true|3:false]}]`, l)
	expectTypeAt(t, r, "f", "e",
		`[8:o:{"kind":[7:s:a|7:s:c],"v":[2:true|3:false]}|8:o:{"kind":[7:s:b],"v":[1:string]}]`, l)
	l.reason(`真：只留属性类型包含 "a" 的对象；假：只删属性类型恰为单字面量 "a" 的对象，"a"|"c" 保留`)
}

// 短路逻辑与/或的汇合（逐变量并集）。
func TestShortCircuit(t *testing.T) {
	l := newLogger(t, "short-circuit")
	defer l.finish()
	p := &Program{
		Decls: map[string]*Type{
			"a": TUnion(TNumber(), TString(), TNull()),
			"o": TUnion(TNumber(), TString(), TNull()),
		},
		Stmts: []*Stmt{
			{ID: "and", K: StmtIf, Cond: &Cond{
				K:     CondAnd,
				Left:  &Cond{K: CondIsType, Var: "a", Check: CheckNumber},
				Right: &Cond{K: CondEqLiteral, Var: "a", Lit: TNumLit(0)},
			}, Then: []*Stmt{{ID: "at", K: StmtAssign, Var: "a", Value: TNumber()}},
				Else: []*Stmt{{ID: "af", K: StmtAssign, Var: "a", Value: TNull()}}},
			{ID: "or", K: StmtIf, Cond: &Cond{
				K:     CondOr,
				Left:  &Cond{K: CondIsType, Var: "o", Check: CheckString},
				Right: &Cond{K: CondEqNull, Var: "o"},
			}, Then: []*Stmt{{ID: "ot", K: StmtAssign, Var: "o", Value: TNull()}},
				Else: []*Stmt{{ID: "of", K: StmtAssign, Var: "o", Value: TNumber()}}},
		},
	}
	l.input("x=number|string|null; if (typeof x==number && x===0); if (typeof x==string || x===null)")
	r := mustAnalyze(t, p, l.tag)
	expectTypeAt(t, r, "at", "a", "[6:n:0]", l)
	expectTypeAt(t, r, "af", "a", "[0:number|1:string|4:null]", l)
	expectTypeAt(t, r, "ot", "o", "[1:string|4:null]", l)
	expectTypeAt(t, r, "of", "o", "[0:number]", l)
	l.reason("与：真=(左真∩右真)，假=左假∪(左真∩右假)；或对偶；均逐变量并集")
}

// 提前返回后的汇合，以及不可达点。
func TestEarlyReturnMergeAndUnreachable(t *testing.T) {
	l := newLogger(t, "return-merge")
	defer l.finish()
	p := &Program{
		Decls: map[string]*Type{"x": TUnion(TNumber(), TString())},
		Stmts: []*Stmt{
			{ID: "if1", K: StmtIf, Cond: &Cond{K: CondIsType, Var: "x", Check: CheckNumber},
				Then: []*Stmt{{ID: "ret", K: StmtReturn}},
				Else: []*Stmt{{ID: "strAssign", K: StmtAssign, Var: "x", Value: TStrLit("hi")}}},
			{ID: "after", K: StmtAssign, Var: "x", Value: TString()},
			{ID: "if2", K: StmtIf, Cond: &Cond{K: CondIsType, Var: "x", Check: CheckNumber},
				Then: []*Stmt{{ID: "dead", K: StmtReturn}}},
		},
	}
	l.input(`if typeof x==number return; else x="hi"; x=string; if typeof x==number return`)
	r := mustAnalyze(t, p, l.tag)
	expectTypeAt(t, r, "after", "x", "[7:s:hi]", l)
	expectUnreachable(t, r, "dead", l)
	l.reason("then 返回终止，汇合只取 else 出口 \"hi\"；恒假分支真环境为永不，登记为不可达")
}

// 赋值后窄化重置为所赋类型；不可赋值错误。
func TestAssignReset(t *testing.T) {
	l := newLogger(t, "assign-reset")
	defer l.finish()
	p := &Program{
		Decls: map[string]*Type{"x": TUnion(TNumber(), TString())},
		Stmts: []*Stmt{
			{ID: "if1", K: StmtIf, Cond: &Cond{K: CondIsType, Var: "x", Check: CheckNumber},
				Then: []*Stmt{
					{ID: "a1", K: StmtAssign, Var: "x", Value: TNumLit(3)},
					{ID: "probe1", K: StmtReturn},
				}},
			{ID: "after", K: StmtReturn},
		},
	}
	l.input("if typeof x==number { x=3; return } return")
	r := mustAnalyze(t, p, l.tag)
	expectTypeAt(t, r, "probe1", "x", "[6:n:3]", l)
	expectTypeAt(t, r, "after", "x", "[1:string]", l)
	l.reason("then 内赋值把 x 重置为字面量 3，但该路径随后返回；空假分支保留 typeof!=number 即 string")
}

func TestErrors(t *testing.T) {
	l := newLogger(t, "errors")
	defer l.finish()

	// 不可赋值
	p1 := &Program{
		Decls: map[string]*Type{"x": TNumber()},
		Stmts: []*Stmt{{ID: "a", K: StmtAssign, Var: "x", Value: TString()}},
	}
	_, e := Analyze(t.Context(), p1)
	l.input("decl x=number; x=string")
	l.output("错误种类=%d 语句=%s", e.Kind, e.StmtID)
	if e == nil || e.Kind != ErrNotAssignable {
		l.fatalf("期望不可赋值错误，得到 %v", e)
	}
	l.reason("string 不是 number 的归一化成员")

	// 不可访问属性优先于缺少判别属性
	p2 := &Program{
		Decls: map[string]*Type{"e": TUnion(TObject(map[string]*Type{"k": TStrLit("a")}), TString())},
		Stmts: []*Stmt{{ID: "i", K: StmtIf,
			Cond: &Cond{K: CondDiscrim, Var: "e", Prop: "missing", Lit: TStrLit("a")},
			Then: []*Stmt{{ID: "r", K: StmtReturn}}}},
	}
	_, e2 := Analyze(t.Context(), p2)
	if e2 == nil || e2.Kind != ErrPropertyNotAccessible {
		l.fatalf("期望不可访问属性，得到 %v", e2)
	}
	l.reason("存在 string 非对象成员，即使属性缺失也只报不可访问属性")

	// 缺少判别属性
	p3 := &Program{
		Decls: map[string]*Type{"e": TObject(map[string]*Type{"k": TStrLit("a")})},
		Stmts: []*Stmt{{ID: "i", K: StmtIf,
			Cond: &Cond{K: CondDiscrim, Var: "e", Prop: "kind", Lit: TStrLit("a")},
			Then: []*Stmt{{ID: "r", K: StmtReturn}}}},
	}
	_, e3 := Analyze(t.Context(), p3)
	if e3 == nil || e3.Kind != ErrMissingDiscriminant {
		l.fatalf("期望缺少判别属性，得到 %v", e3)
	}

	// 未声明变量（参数非法，优先级最高）
	p4 := &Program{
		Decls: map[string]*Type{},
		Stmts: []*Stmt{{ID: "i", K: StmtIf,
			Cond: &Cond{K: CondIsType, Var: "ghost", Check: CheckNumber},
			Then: []*Stmt{{ID: "r", K: StmtReturn}}}},
	}
	_, e4 := Analyze(t.Context(), p4)
	if e4 == nil || e4.Kind != ErrInvalidArgument {
		l.fatalf("期望参数非法，得到 %v", e4)
	}

	// 重复属性名（参数非法）
	bad := &Type{K: KindObject, Props: map[string]*Type{"k": TString()}, DuplicateProps: []string{"k"}}
	p5 := &Program{
		Decls: map[string]*Type{"e": bad},
		Stmts: []*Stmt{{ID: "r", K: StmtReturn}},
	}
	_, e5 := Analyze(t.Context(), p5)
	if e5 == nil || e5.Kind != ErrInvalidArgument {
		l.fatalf("期望重复属性名报参数非法，得到 %v", e5)
	}
	l.reason("重复属性名无法用映射表达，显式标记后在规范化阶段拒绝")
}

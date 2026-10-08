package constexpr

import "math/big"

// Op 标识表达式节点的运算符。
type Op uint8

const (
	OpLit Op = iota + 1
	OpRef
	OpConv        // 显式类型转换
	OpDefaultConv // 需要具体类型语境时的默认类型转换
	OpAdd
	OpSub
	OpMul
	OpDiv
	OpRem
	OpNeg
	OpBitAnd
	OpBitOr
	OpBitXor
	OpBitNot
	OpShl
	OpShr
	OpEq
	OpNe
	OpLt
	OpLe
	OpGt
	OpGe
	OpLogAnd
	OpLogOr
	OpLogNot
)

// Expr 是结构化常量表达式树节点。
type Expr struct {
	Op       Op
	Operands []*Expr
	TypeName string
	Type     CType
	Name     string
	IntVal   *big.Int
	RatVal   *big.Rat
	BoolVal  bool
	StrVal   string
	LitKind  Kind // 仅 OpLit 使用：字面量种类，用于区分 false/空串
}

// 字面量与表达式构造器。
func IntLit(x *big.Int) *Expr {
	if x == nil {
		panic("constexpr: nil *big.Int")
	}
	return &Expr{Op: OpLit, LitKind: KindInt, IntVal: new(big.Int).Set(x)}
}

// RatLit 接受任意 *big.Rat；即使其值恰为整数，种类仍是有理数。
func RatLit(x *big.Rat) *Expr {
	if x == nil {
		panic("constexpr: nil *big.Rat")
	}
	return &Expr{Op: OpLit, LitKind: KindRat, RatVal: new(big.Rat).Set(x)}
}

func BoolLit(b bool) *Expr { return &Expr{Op: OpLit, LitKind: KindBool, BoolVal: b} }

func StringLit(s string) *Expr { return &Expr{Op: OpLit, LitKind: KindString, StrVal: s} }

func Ref(name string) *Expr { return &Expr{Op: OpRef, Name: name} }

func Unary(op Op, x *Expr) *Expr { return &Expr{Op: op, Operands: []*Expr{x}} }

func Binary(op Op, a, b *Expr) *Expr { return &Expr{Op: op, Operands: []*Expr{a, b}} }

// Conv 构造显式类型转换；类型名在结构校验阶段解析。
func Conv(typeName string, x *Expr) *Expr {
	return &Expr{Op: OpConv, TypeName: typeName, Operands: []*Expr{x}}
}

func DefaultConv(x *Expr) *Expr { return &Expr{Op: OpDefaultConv, Operands: []*Expr{x}} }

// validateStructure 检查整棵树的结构合法性（参数非法），不依赖登记表。
var opArity = map[Op]int{
	OpLit: 0, OpRef: 0,
	OpConv: 1, OpDefaultConv: 1,
	OpNeg: 1, OpBitNot: 1, OpLogNot: 1,
	OpAdd: 2, OpSub: 2, OpMul: 2, OpDiv: 2, OpRem: 2,
	OpBitAnd: 2, OpBitOr: 2, OpBitXor: 2,
	OpShl: 2, OpShr: 2,
	OpEq: 2, OpNe: 2, OpLt: 2, OpLe: 2, OpGt: 2, OpGe: 2,
	OpLogAnd: 2, OpLogOr: 2,
}

// validateStructure 检查整棵树的结构合法性（参数非法），不依赖登记表。
// 它不进行任何求值。
func validateStructure(e *Expr) error {
	if e == nil {
		return errf(EvalInvalidArgument, "表达式节点为空")
	}
	arity, known := opArity[e.Op]
	if !known {
		return errf(EvalInvalidArgument, "未知运算符 %d", e.Op)
	}
	if len(e.Operands) != arity {
		return errf(EvalInvalidArgument, "运算符 %s 需要 %d 个操作数，实际 %d 个",
			opName(e.Op), arity, len(e.Operands))
	}
	for i, sub := range e.Operands {
		if sub == nil {
			return errf(EvalInvalidArgument, "运算符 %s 的第 %d 个操作数为空", opName(e.Op), i+1)
		}
	}
	switch e.Op {
	case OpLit:
		return validateLit(e)
	case OpRef:
		if e.Name == "" {
			return errf(EvalInvalidArgument, "引用的名字为空")
		}
	case OpConv:
		t, ok := ParseCType(e.TypeName)
		if !ok {
			return errf(EvalInvalidArgument, "未知类型名 %q", e.TypeName)
		}
		e.Type = t
	}
	// 递归校验子树（不依赖求值结果）。
	for _, sub := range e.Operands {
		if err := validateStructure(sub); err != nil {
			return err
		}
	}
	return nil
}

func validateLit(e *Expr) error {
	switch e.LitKind {
	case KindInt:
		if e.IntVal == nil {
			return errf(EvalInvalidArgument, "整数种类字面量缺少整数值")
		}
	case KindRat:
		if e.RatVal == nil {
			return errf(EvalInvalidArgument, "有理数种类字面量缺少有理数")
		}
	case KindBool:
	case KindString:
	default:
		return errf(EvalInvalidArgument, "字面量缺少有效种类")
	}
	return nil
}

func opName(op Op) string {
	if s, ok := opNames[op]; ok {
		return s
	}
	return "<unknown-op>"
}

var opNames = map[Op]string{
	OpLit: "lit", OpRef: "ref", OpConv: "conv", OpDefaultConv: "default-conv",
	OpAdd: "+", OpSub: "-", OpMul: "*", OpDiv: "/", OpRem: "%", OpNeg: "neg",
	OpBitAnd: "&", OpBitOr: "|", OpBitXor: "^", OpBitNot: "bit-not",
	OpShl: "<<", OpShr: ">>",
	OpEq: "==", OpNe: "!=", OpLt: "<", OpLe: "<=", OpGt: ">", OpGe: ">=",
	OpLogAnd: "&&", OpLogOr: "||", OpLogNot: "!",
}

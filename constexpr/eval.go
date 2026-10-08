package constexpr

import (
	"math/big"
)

// TraceEvent 描述一次求值过程中的关键判定，供测试日志使用。
type TraceEvent struct {
	Op     Op
	Inputs []*Value
	Output *Value
	Err    error
	Note   string
}

// Tracer 接收求值过程中每个节点的输入、输出与判定依据。
type Tracer func(TraceEvent)

// Option 配置求值器行为。
type Option func(*Evaluator)

// Evaluator 在某个命名常量表快照上对表达式树求值。
type Evaluator struct {
	snap   snapshot
	tracer Tracer
}

// WithTracer 挂载求值轨迹记录器（用于打印每次输入/输出/判定依据）。
func WithTracer(t Tracer) Option {
	return func(ev *Evaluator) { ev.tracer = t }
}

// NewEvaluator 创建求值器。
func NewEvaluator(opts ...Option) *Evaluator {
	ev := &Evaluator{snap: emptySnapshot()}
	for _, o := range opts {
		o(ev)
	}
	return ev
}

// Eval 求值入口：结构校验、名字解析，然后按顺序求值。
func (ev *Evaluator) Eval(e *Expr) (*Value, error) {
	if err := validateStructure(e); err != nil {
		return nil, err
	}
	if err := ev.checkNames(e); err != nil {
		return nil, err
	}
	v, err := ev.eval(e)
	if err != nil {
		return nil, err
	}
	return v, nil
}

// Eval 包级便捷求值（无登记名字可用）。
func Eval(e *Expr) (*Value, error) { return NewEvaluator().Eval(e) }

// checkNames 是求值前的引用解析阶段：未知名字优先于一切求值错误。
// 按先子后父、从左到右遍历。
func (ev *Evaluator) checkNames(e *Expr) error {
	for _, sub := range e.Operands {
		if err := ev.checkNames(sub); err != nil {
			return err
		}
	}
	if e.Op == OpRef {
		if _, ok := ev.snap.lookup(e.Name); !ok {
			return errf(EvalUnknownName, "未登记的命名常量 %q", e.Name)
		}
	}
	return nil
}

func (ev *Evaluator) eval(e *Expr) (*Value, error) {
	// 先求全部子表达式：从左到右，不做短路。
	subs := make([]*Value, len(e.Operands))
	for i, sub := range e.Operands {
		v, err := ev.eval(sub)
		if err != nil {
			return nil, err
		}
		subs[i] = v
	}

	out, err := ev.apply(e, subs)
	if ev.tracer != nil {
		ev.tracer(TraceEvent{Op: e.Op, Inputs: valuesClone(subs), Output: cloneOrNil(out), Err: err, Note: e.Name + e.StrVal + e.TypeName})
	}
	if err != nil {
		return nil, err
	}
	return out, nil
}

func cloneOrNil(v *Value) *Value {
	if v == nil {
		return nil
	}
	return v.Clone()
}

func valuesClone(vs []*Value) []*Value {
	out := make([]*Value, len(vs))
	for i, v := range vs {
		out[i] = v.Clone()
	}
	return out
}

func (ev *Evaluator) apply(e *Expr, subs []*Value) (*Value, error) {
	switch e.Op {
	case OpLit:
		return ev.evalLit(e)
	case OpRef:
		v, _ := ev.snap.lookup(e.Name)
		return v.Clone(), nil
	case OpConv:
		return convertValue(subs[0], e.Type)
	case OpDefaultConv:
		return ev.defaultConv(subs[0])
	case OpNeg, OpBitNot, OpLogNot:
		return ev.evalUnary(e.Op, subs[0])
	case OpLogAnd, OpLogOr:
		return ev.evalLogic(e.Op, subs[0], subs[1])
	case OpShl, OpShr:
		return ev.evalShift(e.Op, subs[0], subs[1])
	case OpEq, OpNe, OpLt, OpLe, OpGt, OpGe:
		return ev.evalCompare(e.Op, subs[0], subs[1])
	default:
		return ev.evalArith(e.Op, subs[0], subs[1])
	}
}

func (ev *Evaluator) evalLit(e *Expr) (*Value, error) {
	switch e.LitKind {
	case KindInt:
		v := untypedInt(new(big.Int).Set(e.IntVal))
		if err := checkUntypedIntBits(v.i); err != nil {
			return nil, err
		}
		return v, nil
	case KindRat:
		return untypedRat(new(big.Rat).Set(e.RatVal)), nil
	case KindBool:
		return untypedBool(e.BoolVal), nil
	case KindString:
		return untypedString(e.StrVal), nil
	}
	return nil, errf(EvalInvalidArgument, "字面量种类非法")
}

// defaultConv: 已经有类型的值原样返回；无类型值转到默认类型。
func (ev *Evaluator) defaultConv(v *Value) (*Value, error) {
	if v.IsTyped() {
		return v.Clone(), nil
	}
	return convertValue(v, defaultType(v.kind))
}

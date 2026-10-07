package dlabel

// CompareOp 是属性原子判定上的比较算子。
type CompareOp int

const (
	OpEq CompareOp = iota + 1
	OpNeq
	OpLt
	OpLte
	OpGt
	OpGte
	OpIsNull
	OpNotNull
)

// LogicOp 是判定表达式的组合算子。
type LogicOp int

const (
	OpAtom LogicOp = iota
	OpNot
	OpAnd
	OpOr
)

// AtomKind 标识原子判定的种类。
type AtomKind int

const (
	AtomAttr  AtomKind = iota // 读取某属性当前取值并比较
	AtomTag                   // 引用另一标签的当前判定结果
	AtomConst                 // 布尔常量
)

// Atom 是判定表达式的叶子节点。
type Atom struct {
	Kind  AtomKind
	Attr  string    // Kind == AtomAttr
	Op    CompareOp // Kind == AtomAttr
	Value Value     // Kind == AtomAttr（IsNull/NotNull 时忽略）
	Tag   string    // Kind == AtomTag
	Const bool      // Kind == AtomConst
}

// Expr 是标签判定规则的布尔表达式树。
type Expr struct {
	Op    LogicOp
	Atom  Atom // Op == Atom
	Terms []Expr
}

// AttrAtom 构造“读取属性取值并比较”的原子判定。
func AttrAtom(attr string, op CompareOp, v Value) Expr {
	return Expr{Op: OpAtom, Atom: Atom{Kind: AtomAttr, Attr: attr, Op: op, Value: v}}
}

// TagAtom 构造“引用另一标签当前判定结果”的原子判定。
func TagAtom(tag string) Expr {
	return Expr{Op: OpAtom, Atom: Atom{Kind: AtomTag, Tag: tag}}
}

// ConstAtom 构造布尔常量原子判定。
func ConstAtom(v bool) Expr {
	return Expr{Op: OpAtom, Atom: Atom{Kind: AtomConst, Const: v}}
}

// Not / And / Or 构造组合判定。
func Not(inner Expr) Expr    { return Expr{Op: OpNot, Terms: []Expr{inner}} }
func And(terms ...Expr) Expr { return Expr{Op: OpAnd, Terms: terms} }
func Or(terms ...Expr) Expr  { return Expr{Op: OpOr, Terms: terms} }

// Rule 是一条敏感标签判定规则：针对某对象类型，输出实例是否携带标签。
type Rule struct {
	ObjectType string
	Tag        string
	Body       Expr
}

// referencedAttrs / referencedTags 收集规则直接依赖，结果排序去重。
func (r Rule) referencedAttrs() []string { return exprAttrs(r.Body) }
func (r Rule) referencedTags() []string  { return exprTags(r.Body) }

func exprAttrs(e Expr) []string {
	seen := map[string]struct{}{}
	var walk func(Expr)
	walk = func(x Expr) {
		switch x.Op {
		case OpAtom:
			if x.Atom.Kind == AtomAttr {
				seen[x.Atom.Attr] = struct{}{}
			}
		default:
			for _, t := range x.Terms {
				walk(t)
			}
		}
	}
	walk(e)
	return sortedKeys(seen)
}

func exprTags(e Expr) []string {
	seen := map[string]struct{}{}
	var walk func(Expr)
	walk = func(x Expr) {
		switch x.Op {
		case OpAtom:
			if x.Atom.Kind == AtomTag {
				seen[x.Atom.Tag] = struct{}{}
			}
		default:
			for _, t := range x.Terms {
				walk(t)
			}
		}
	}
	walk(e)
	return sortedKeys(seen)
}

// validate 对表达式做静态检查。
//
// 错误检查顺序（固定）：结构非法 → 引用不存在的属性 → 引用不存在的标签。
// 属性缺失先于标签缺失，保证三类特权错误中的“缺失属性”优先暴露。
func (r Rule) validate(knownAttrs, knownTags map[string]struct{}) error {
	if err := validateExpr(r.Body); err != nil {
		return err
	}
	for _, attr := range r.referencedAttrs() {
		if _, ok := knownAttrs[attr]; !ok {
			return &Error{Code: CodeMissingAttribute, Msg: "rule of tag " + r.Tag + " references missing attribute: " + attr}
		}
	}
	for _, tag := range r.referencedTags() {
		if _, ok := knownTags[tag]; !ok {
			return &Error{Code: CodeUnknownTag, Msg: "rule of tag " + r.Tag + " references unknown tag: " + tag}
		}
	}
	return nil
}

func validateExpr(e Expr) error {
	switch e.Op {
	case OpAtom:
		switch e.Atom.Kind {
		case AtomAttr:
			if e.Atom.Attr == "" {
				return &Error{Code: CodeInvalidRule, Msg: "attribute atom has empty attribute name"}
			}
			switch e.Atom.Op {
			case OpEq, OpNeq, OpLt, OpLte, OpGt, OpGte, OpIsNull, OpNotNull:
			default:
				return &Error{Code: CodeInvalidRule, Msg: "invalid compare operator"}
			}
		case AtomTag:
			if e.Atom.Tag == "" {
				return &Error{Code: CodeInvalidRule, Msg: "tag atom has empty tag name"}
			}
		case AtomConst:
		default:
			return &Error{Code: CodeInvalidRule, Msg: "unknown atom kind"}
		}
	case OpNot:
		if len(e.Terms) != 1 {
			return &Error{Code: CodeInvalidRule, Msg: "not requires exactly one term"}
		}
		return validateExpr(e.Terms[0])
	case OpAnd, OpOr:
		if len(e.Terms) == 0 {
			return &Error{Code: CodeInvalidRule, Msg: "and/or requires at least one term"}
		}
		for _, t := range e.Terms {
			if err := validateExpr(t); err != nil {
				return err
			}
		}
	default:
		return &Error{Code: CodeInvalidRule, Msg: "unknown logic operator"}
	}
	return nil
}

// evalEnv 为表达式求值提供快照内的属性读取与标签判定能力。
// readAttr 必须返回该属性在快照下的真实取值，并被调用方记账。
type evalEnv struct {
	readAttr func(attr string) Value
	tagValue func(tag string) (bool, error)
}

// evalExpr 按当前快照求值。它始终基于属性真实取值；
// 求值过程不向主体暴露任何信息——信息隔离发生在调用方对结果的裁剪上。
func evalExpr(e Expr, env evalEnv) (bool, error) {
	switch e.Op {
	case OpAtom:
		switch e.Atom.Kind {
		case AtomAttr:
			return compareValues(env.readAttr(e.Atom.Attr), e.Atom.Value, e.Atom.Op), nil
		case AtomTag:
			return env.tagValue(e.Atom.Tag)
		case AtomConst:
			return e.Atom.Const, nil
		}
		return false, &Error{Code: CodeInvalidRule, Msg: "unknown atom kind"}
	case OpNot:
		v, err := evalExpr(e.Terms[0], env)
		return !v, err
	case OpAnd:
		for _, t := range e.Terms {
			v, err := evalExpr(t, env)
			if err != nil {
				return false, err
			}
			if !v {
				return false, nil
			}
		}
		return true, nil
	case OpOr:
		for _, t := range e.Terms {
			v, err := evalExpr(t, env)
			if err != nil {
				return false, err
			}
			if v {
				return true, nil
			}
		}
		return false, nil
	}
	return false, &Error{Code: CodeInvalidRule, Msg: "unknown logic operator"}
}

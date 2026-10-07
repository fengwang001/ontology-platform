package ontology

import "strconv"

// IncompatKind 绑定兼容性结论的分类。
type IncompatKind int

const (
	// Compatible 对应方式满足声明方向要求的唯一性。
	Compatible IncompatKind = iota
	// IncompatFieldDeleted 绑定依据字段已被删除，绑定定义本身失效。
	// 该判定优先于其余三类。
	IncompatFieldDeleted
	// IncompatIncomparableTypes 两侧取值类型在当前版本下无法比较，无法判定。
	IncompatIncomparableTypes
	// IncompatNotBijective 对应方式不满足双向唯一。
	IncompatNotBijective
	// IncompatDeclaredTwoWayOnlyOneWay 声明为双向但实际只单向可对应。
	IncompatDeclaredTwoWayOnlyOneWay
)

func (k IncompatKind) String() string {
	switch k {
	case Compatible:
		return "compatible"
	case IncompatFieldDeleted:
		return "field-deleted"
	case IncompatIncomparableTypes:
		return "incomparable-types"
	case IncompatNotBijective:
		return "not-bidirectionally-unique"
	case IncompatDeclaredTwoWayOnlyOneWay:
		return "declared-two-way-but-only-one-way"
	default:
		return "unknown"
	}
}

// CheckBasis 记录一次核验的对应关系检查依据，供事后核对。
type CheckBasis struct {
	Correspondence Correspondence
	// EffectivePairs 取值空间有限时，合成缺失策略后的实际对应取值对。
	EffectivePairs []ValuePair
	Notes          []string
}

// CheckResult 是一次绑定兼容性核验的结论。
type CheckResult struct {
	Kind          IncompatKind
	LeftToRightOK bool
	RightToLeftOK bool
	Basis         CheckBasis
	Detail        string
}

// Compatible 报告结论是否满足声明方向的兼容性要求。
func (r CheckResult) Compatible(decl Direction) bool {
	if r.Kind != Compatible {
		return false
	}
	switch decl {
	case LeftToRight:
		return r.LeftToRightOK
	case RightToLeft:
		return r.RightToLeftOK
	default:
		return r.LeftToRightOK && r.RightToLeftOK
	}
}

// CheckBinding 对绑定声明做纯函数式兼容性核验。
// left/right 为 nil 表示对应侧字段已被删除。
//
// 判定顺序：字段删除优先；其次类型是否可比较；最后做唯一性检查。
// 缺失取值按声明的 MissingPolicy 合成进对应关系后，与普通取值一样参与唯一性判断。
func CheckBinding(decl BindingDecl, left, right *FieldDef) CheckResult {
	// 1. 绑定依据字段被删除：定义失效，优先于其余一切判定。
	if left == nil || right == nil {
		side := "left"
		if left != nil {
			side = "right"
		}
		return CheckResult{
			Kind:   IncompatFieldDeleted,
			Detail: "binding field deleted on " + side + " side",
		}
	}

	leftDom := left.Domain()
	rightDom := right.Domain()
	basis := CheckBasis{Correspondence: decl.Correspondence}

	// 2. 类型可比较性。
	switch decl.Correspondence.Kind {
	case Identity:
		if left.Type != right.Type {
			return CheckResult{
				Kind:   IncompatIncomparableTypes,
				Basis:  basis,
				Detail: "identity correspondence requires equal value types, got " + string(left.Type) + " vs " + string(right.Type),
			}
		}
	case Explicit:
		// 显式取值对无法在无界取值空间上验证全覆盖，当前版本判定为无法比较。
		if leftDom.Unbounded || rightDom.Unbounded {
			return CheckResult{
				Kind:   IncompatIncomparableTypes,
				Basis:  basis,
				Detail: "explicit correspondence over unbounded domain is undecidable",
			}
		}
	}

	// 3. 唯一性检查。
	var l2rOK, r2lOK bool
	var notes []string
	if !leftDom.Unbounded && !rightDom.Unbounded {
		l2rOK, r2lOK, notes = checkFinite(decl, leftDom, rightDom, &basis)
	} else {
		l2rOK, r2lOK, notes = checkUnboundedIdentity(decl, *left, *right, leftDom, rightDom)
	}
	basis.Notes = notes

	res := CheckResult{
		LeftToRightOK: l2rOK,
		RightToLeftOK: r2lOK,
		Basis:         basis,
	}
	var requiredOK bool
	switch decl.Direction {
	case LeftToRight:
		requiredOK = l2rOK
	case RightToLeft:
		requiredOK = r2lOK
	default:
		requiredOK = l2rOK && r2lOK
	}
	switch {
	case requiredOK:
		res.Kind = Compatible
	case decl.Direction == TwoWay && (l2rOK || r2lOK):
		res.Kind = IncompatDeclaredTwoWayOnlyOneWay
		res.Detail = "declared two-way but only one direction corresponds uniquely"
	default:
		res.Kind = IncompatNotBijective
		res.Detail = "correspondence is not unique in the required direction"
	}
	return res
}

// composePairs 合成实际生效的对应取值对：恒等或显式对应，再叠加缺失策略。
// 恒等对应只生成非缺失取值的取值对；缺失取值的对应完全由缺失策略决定。
func composePairs(decl BindingDecl, leftDom, rightDom Domain) []ValuePair {
	var pairs []ValuePair
	if decl.Correspondence.Kind == Identity {
		for v := range leftDom.Values {
			if v.Missing {
				continue
			}
			pairs = append(pairs, ValuePair{Left: v, Right: v})
		}
	} else {
		pairs = append(pairs, decl.Correspondence.Pairs...)
	}
	missing := MissingValue()
	switch decl.Missing.Kind {
	case MissingForbidden:
		// 缺失取值无对应：剔除涉及缺失的取值对。
		filtered := pairs[:0]
		for _, p := range pairs {
			if p.Left.Missing || p.Right.Missing {
				continue
			}
			filtered = append(filtered, p)
		}
		pairs = filtered
	case MissingToMissing:
		pairs = append(pairs, ValuePair{Left: missing, Right: missing})
	case MissingToValue:
		pairs = append(pairs,
			ValuePair{Left: missing, Right: decl.Missing.LeftMissingTo},
			ValuePair{Left: decl.Missing.RightMissingTo, Right: missing},
		)
	}
	return dedupePairs(pairs)
}

func dedupePairs(pairs []ValuePair) []ValuePair {
	seen := make(map[ValuePair]bool, len(pairs))
	out := pairs[:0]
	for _, p := range pairs {
		if seen[p] {
			continue
		}
		seen[p] = true
		out = append(out, p)
	}
	return out
}

// checkFinite 在两侧取值空间均有限时做完全枚举的唯一性检查。
func checkFinite(decl BindingDecl, leftDom, rightDom Domain, basis *CheckBasis) (l2rOK, r2lOK bool, notes []string) {
	pairs := composePairs(decl, leftDom, rightDom)
	basis.EffectivePairs = pairs

	// 端点落在对应侧取值空间之外的取值对不参与唯一性判断：
	// 对应关系只定义在两侧合法取值之上。
	forward := make(map[Value][]Value)
	backward := make(map[Value][]Value)
	for _, p := range pairs {
		if !leftDom.Contains(p.Left) || !rightDom.Contains(p.Right) {
			continue
		}
		forward[p.Left] = append(forward[p.Left], p.Right)
		backward[p.Right] = append(backward[p.Right], p.Left)
	}

	l2rOK = true
	for v := range leftDom.Values {
		targets := forward[v]
		if len(targets) != 1 || !rightDom.Contains(targets[0]) {
			l2rOK = false
			notes = append(notes, "left value "+formatValue(v)+" has no unique determined right counterpart")
		}
	}
	r2lOK = true
	for v := range rightDom.Values {
		sources := backward[v]
		if len(sources) != 1 || !leftDom.Contains(sources[0]) {
			r2lOK = false
			notes = append(notes, "right value "+formatValue(v)+" has no unique determined left counterpart")
		}
	}
	return l2rOK, r2lOK, notes
}

// checkUnboundedIdentity 处理恒等对应下至少一侧取值空间无界的情形。
func checkUnboundedIdentity(decl BindingDecl, left, right FieldDef, leftDom, rightDom Domain) (l2rOK, r2lOK bool, notes []string) {
	subset := func(from, to Domain) bool {
		if !from.Unbounded {
			for v := range from.Values {
				if v.Missing {
					continue // 缺失取值由缺失策略单独判定
				}
				if !to.Contains(v) {
					return false
				}
			}
			return true
		}
		return to.Unbounded // 无界 -> 只有同样无界才全覆盖
	}

	missingOK := func(nullable bool, policy MissingPolicy, target Domain, targetValue Value) bool {
		if !nullable {
			return true
		}
		switch policy.Kind {
		case MissingForbidden:
			return false
		case MissingToMissing:
			return target.Values[MissingValue()]
		case MissingToValue:
			return target.Contains(targetValue)
		}
		return false
	}

	l2rOK = subset(leftDom, rightDom) && missingOK(left.Nullable, decl.Missing, rightDom, decl.Missing.LeftMissingTo)
	r2lOK = subset(rightDom, leftDom) && missingOK(right.Nullable, decl.Missing, leftDom, decl.Missing.RightMissingTo)

	// MissingToValue 引入的额外前驱/后继会破坏反向唯一性。
	if decl.Missing.Kind == MissingToValue {
		if left.Nullable && leftDom.Contains(decl.Missing.LeftMissingTo) {
			r2lOK = false
			notes = append(notes, "right value "+formatValue(decl.Missing.LeftMissingTo)+" has both itself and left-missing as counterparts")
		}
		if right.Nullable && rightDom.Contains(decl.Missing.RightMissingTo) {
			l2rOK = false
			notes = append(notes, "left value "+formatValue(decl.Missing.RightMissingTo)+" has both itself and right-missing as counterparts")
		}
	}
	if !l2rOK {
		notes = append(notes, "left-to-right correspondence is not total or not unique")
	}
	if !r2lOK {
		notes = append(notes, "right-to-left correspondence is not total or not unique")
	}
	return l2rOK, r2lOK, notes
}

func formatValue(v Value) string {
	if v.Missing {
		return "<missing>"
	}
	switch v.Type {
	case TString:
		return strconv.Quote(v.Str)
	case TInt:
		return strconv.FormatInt(v.Int, 10)
	case TBool:
		return strconv.FormatBool(v.Bool)
	}
	return "<?>"
}

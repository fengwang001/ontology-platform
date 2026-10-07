package ontology

// MergeMode selects allow-overrides vs deny-overrides.
type MergeMode int

const (
	// AllowOverrides: any matching allow yields allow even if denies match.
	AllowOverrides MergeMode = iota + 1
	// DenyOverrides: any matching deny yields deny even if allows match.
	DenyOverrides
)

func (m MergeMode) valid() bool { return m == AllowOverrides || m == DenyOverrides }

// CmpOp is a typed comparison used inside a predicate.
type CmpOp int

const (
	CmpEq CmpOp = iota + 1
	CmpNe
	CmpLt
	CmpLe
	CmpGt
	CmpGe
)

// Predicate is one conjunct evaluated strictly against RAW values.
// An absent raw value never equals a supplied operand, so predicates
// over an unreadable or absent property still evaluate deterministically
// without ever exposing that value to the subject.
type Predicate struct {
	Property string
	Op       CmpOp
	Value    RawValue
}

func (p Predicate) eval(ot *ObjectType, inst *Instance) (bool, *DecisionError) {
	dt, ok := ot.PropertyType(p.Property)
	if !ok {
		return false, &DecisionError{ErrUnknownProperty, "row predicate references unknown property " + p.Property}
	}
	if !dt.CheckValue(p.Value) {
		return false, &DecisionError{ErrInvalidValueType, "row predicate operand for " + p.Property + " violates " + dt.String()}
	}
	cell, has := inst.cells[p.Property]
	if !has || !cell.present {
		// Absence is a distinct internal state; it only satisfies NE.
		return p.Op == CmpNe, nil
	}
	cmp, err := compareRaw(dt, cell.value, p.Value)
	if err != nil {
		return false, err
	}
	switch p.Op {
	case CmpEq:
		return cmp == 0, nil
	case CmpNe:
		return cmp != 0, nil
	case CmpLt:
		return cmp < 0, nil
	case CmpLe:
		return cmp <= 0, nil
	case CmpGt:
		return cmp > 0, nil
	case CmpGe:
		return cmp >= 0, nil
	default:
		return false, &DecisionError{ErrInvalidType, "unknown comparison operator"}
	}
}

func compareRaw(dt DataType, a, b RawValue) (int, *DecisionError) {
	switch dt {
	case TypeInt:
		return cmpOrdered(a.(int64), b.(int64)), nil
	case TypeFloat:
		return cmpOrdered(a.(float64), b.(float64)), nil
	case TypeString:
		return cmpOrdered(a.(string), b.(string)), nil
	case TypeBool:
		// Bools support equality only.
		ab, bb := a.(bool), b.(bool)
		if ab == bb {
			return 0, nil
		}
		if !ab {
			return -1, nil
		}
		return 1, nil
	default:
		return 0, &DecisionError{ErrInvalidType, "uncomparable type"}
	}
}

type ordered interface {
	~int64 | ~float64 | ~string
}

func cmpOrdered[T ordered](a, b T) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	default:
		return 0
	}
}

// Effect is the allow/deny conclusion contributed by a matching rule.
type Effect int

const (
	EffectDeny Effect = iota + 1
	EffectAllow
)

// RowRule is one row-level policy rule. All predicates are conjoined;
// a rule "matches" a (subject, instance) pair when the selector matches
// and every predicate evaluates true on the instance's raw values.
type RowRule struct {
	ID         string
	Selector   SubjectSelector
	Predicates []Predicate
	Effect     Effect
}

// RowPolicySet groups row rules under one merge mode. Defaults apply
// when no rule matches: subject cannot see the instance.
type RowPolicySet struct {
	Mode  MergeMode
	Rules []RowRule
}

func (s RowPolicySet) validate(ot *ObjectType) *DecisionError {
	if !s.Mode.valid() {
		return &DecisionError{ErrInvalidType, "row merge mode must be AllowOverrides or DenyOverrides"}
	}
	seen := map[string]bool{}
	for _, r := range s.Rules {
		if r.ID == "" {
			return &DecisionError{ErrInvalidType, "row rule id must not be empty"}
		}
		if seen[r.ID] {
			return &DecisionError{ErrInvalidType, "duplicate row rule id " + r.ID}
		}
		seen[r.ID] = true
		if r.Effect != EffectAllow && r.Effect != EffectDeny {
			return &DecisionError{ErrInvalidType, "row rule " + r.ID + " has invalid effect"}
		}
		for _, p := range r.Predicates {
			if _, err := p.eval(ot, &Instance{cells: map[string]rawCell{}}); err != nil && err.Kind == ErrUnknownProperty {
				return err
			}
		}
	}
	return nil
}

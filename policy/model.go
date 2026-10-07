package policy

// Domain model types are defined here.

import (
	"fmt"
	"sort"
)

// AttrKind is the declared scalar kind of an attribute.
type AttrKind int

const (
	KindString AttrKind = iota + 1
	KindInt
	KindFloat
	KindBool
)

func (k AttrKind) String() string {
	switch k {
	case KindString:
		return "string"
	case KindInt:
		return "int"
	case KindFloat:
		return "float"
	case KindBool:
		return "bool"
	default:
		return fmt.Sprintf("kind(%d)", int(k))
	}
}

// AttrType declares the kind and value constraints of an attribute.
// For strings MaxLen bounds rune length. For ints/floats Min/Max bound the
// value (inclusive). Allowed enforces an enumeration when non-empty.
type AttrType struct {
	Kind    AttrKind
	MaxLen  int
	Min     *float64
	Max     *float64
	Allowed []any
}

// ObjectType is an immutable schema: a set of typed attributes.
type ObjectType struct {
	Name  string
	Attrs map[string]AttrType
}

// Instance is one concrete object. Values must match the schema's AttrType;
// attributes absent from the map are treated as unset (nil).
type Instance struct {
	Type   string
	ID     string
	Values map[string]any
}

// RuleKind enumerates the deterministic derived-value rules.
type RuleKind int

const (
	// RuleRedact replaces the value with the fixed sentinel "[REDACTED]".
	RuleRedact RuleKind = iota + 1
	// RuleHash replaces the value with a stable, non-reversible digest string.
	RuleHash
	// RuleMask keeps a prefix/pattern of strings; non-strings redact.
	RuleMask
	// RuleConst replaces the value with a caller-supplied constant.
	RuleConst
	// RuleCopyDerived copies the *final presented* value of another attribute.
	RuleCopyDerived
)

// DerivedRule is one masking derivation. ConstVal is used by RuleConst;
// SourceAttr by RuleCopyDerived; KeepRunes by RuleMask.
type DerivedRule struct {
	Kind       RuleKind
	ConstVal   any
	SourceAttr string
	KeepRunes  int
}

// Strength is the declared masking strength: larger means stronger. Merging
// several masking policies always yields the maximum strength.
type Strength int

const (
	StrengthNone   Strength = 0
	StrengthWeak   Strength = 1
	StrengthMedium Strength = 2
	StrengthStrong Strength = 3
)

func (s Strength) String() string {
	switch s {
	case StrengthWeak:
		return "weak"
	case StrengthMedium:
		return "medium"
	case StrengthStrong:
		return "strong"
	default:
		return fmt.Sprintf("strength(%d)", int(s))
	}
}

// VisibilityPolicy yields an allow or deny read decision for one attribute of
// an object type, optionally conditioned on the *raw* value of another
// attribute. Pred is evaluated by Match; a nil Pred always matches.
type VisibilityPolicy struct {
	ID      string
	Object  string
	Subject string
	Attr    string
	Allow   bool
	Pred    *Predicate
}

// Predicate tests the raw value of the referenced condition attribute.
// The condition is always evaluated against raw instance values, never
// derived values. Op is one of "eq","ne","lt","le","gt","ge".
type Predicate struct {
	CondAttr string
	Op       string
	Value    any
}

// MaskingPolicy registers a masking strength and derivation rule for one
// attribute of an object type for a subject.
type MaskingPolicy struct {
	ID       string
	Object   string
	Subject  string
	Attr     string
	Strength Strength
	Rule     DerivedRule
}

// PolicySet is one atomic revision of the policy store: schema plus the
// complete set of visibility and masking policies.
type PolicySet struct {
	Types      []ObjectType
	Visibility []VisibilityPolicy
	Masking    []MaskingPolicy
}

// sortedAttrs returns attribute names in deterministic (lexicographic) order.
func sortedAttrs(attrs map[string]AttrType) []string {
	names := make([]string, 0, len(attrs))
	for name := range attrs {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

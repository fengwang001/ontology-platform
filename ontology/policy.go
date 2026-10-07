package ontology

// Effect is the terminal allow/deny conclusion of a single policy.
type Effect string

const (
	EffectAllow Effect = "allow"
	EffectDeny  Effect = "deny"
)

// MergeMode declares how multiple matched conclusions combine into one.
// DenyOverrides: any deny wins; otherwise an allow yields allow; no matches
// yields the configured default.
// AllowOverrides: any allow wins; otherwise a deny yields deny; no matches
// yields the configured default.
//
// The mode therefore yields a unique verdict for every combination of
// conclusions, including simultaneous allow and deny.
type MergeMode string

const (
	DenyOverrides  MergeMode = "deny_overrides"
	AllowOverrides MergeMode = "allow_overrides"
)

// mergeEffects combines per-policy effects according to mode. matched reports
// whether at least one policy applied; fallback is used when none did.
func mergeEffects(effects []Effect, mode MergeMode, fallback Effect) Effect {
	if len(effects) == 0 {
		return fallback
	}
	var allow, deny bool
	for _, e := range effects {
		switch e {
		case EffectAllow:
			allow = true
		case EffectDeny:
			deny = true
		}
	}
	switch mode {
	case AllowOverrides:
		if allow {
			return EffectAllow
		}
		return EffectDeny
	default: // DenyOverrides
		if deny {
			return EffectDeny
		}
		return EffectAllow
	}
}

// RowPolicy is a row-level visibility policy for one object type. It applies
// to a subject when the subject is selected by Subjects (or Subjects is
// empty, meaning every subject). When its Predicate holds on the instance's
// raw values, the policy emits Effect for row visibility.
type RowPolicy struct {
	ID         string
	ObjectType string
	Subjects   []string
	Effect     Effect
	Predicate  Predicate
}

func (p RowPolicy) selects(subject string) bool {
	if len(p.Subjects) == 0 {
		return true
	}
	for _, s := range p.Subjects {
		if s == subject {
			return true
		}
	}
	return false
}

// MaskFunc computes the derived value presented to a subject. It receives the
// raw value and returns the masked value. Mask functions are pure: they must
// not depend on call order or external mutable state.
type MaskFunc func(raw Value) Value

// PropertyPolicy independently declares read and write conclusions and an
// optional mask for one property. A nil conclusion for Read or Write means
// the policy expresses no opinion on that axis. Property == "*" matches
// every property of the type, but adjudication stays per-property.
type PropertyPolicy struct {
	ID         string
	ObjectType string
	Subjects   []string
	Property   string
	Read       *Effect
	Write      *Effect
	Mask       MaskFunc
	MaskName   string
}

func (p PropertyPolicy) selects(subject string) bool {
	if len(p.Subjects) == 0 {
		return true
	}
	for _, s := range p.Subjects {
		if s == subject {
			return true
		}
	}
	return false
}

// WriteMode controls what happens to fields the subject cannot write.
type WriteMode string

const (
	// WriteReject rejects the whole request if any field is not writable.
	WriteReject WriteMode = "reject"
	// WriteDrop silently drops non-writable fields and writes the rest.
	WriteDrop WriteMode = "drop"
)

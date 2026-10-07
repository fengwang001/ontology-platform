package ontology

// WriteMode selects reject-all vs drop-unwritable semantics.
type WriteMode int

const (
	// WriteRejectAll: one unwritable property rejects the whole write.
	WriteRejectAll WriteMode = iota + 1
	// WriteDrop: unwritable properties are silently dropped and the
	// writable remainder is applied; dropped fields change nothing.
	WriteDrop
)

func (m WriteMode) valid() bool { return m == WriteRejectAll || m == WriteDrop }

// MaskFunc derives the presented value for a read. It receives only
// the subject and the raw value (nil when the raw value is absent),
// never other property values, so masks cannot leak sideways.
type MaskFunc func(sub Subject, raw RawValue) (RawValue, error)

// WriteMaskFunc validates/transforms a written value on a property
// whose policy declares a derived (masked) storage. Its output must
// satisfy the property's declared type contract; a violation surfaces
// as ErrMaskedTypeViolation on the write path.
type WriteMaskFunc func(sub Subject, incoming RawValue) (RawValue, error)

// PropRule is one property-level policy rule for exactly one property.
//
// Read access:
//   - Mask != nil  -> match contributes an ALLOW to read the MASKED
//     derived value;
//   - otherwise the match contributes ALLOW when Readable, DENY when
//     Readable is false (a read-scoped rule).
//
// Write access:
//   - WriteMask != nil -> match contributes an ALLOW with a derived
//     transform (used to detect declared-type contract violations);
//   - Writable set     -> match contributes an explicit ALLOW/DENY
//     write conclusion (a write-scoped rule).
//
// Read and write conclusions are accumulated independently, so a rule
// may address one, the other, or both without cross-interference.
type PropRule struct {
	ID       string
	Property string
	Selector SubjectSelector
	// HasRead / HasWrite scope the rule. Mask implies HasRead and
	// WriteMask implies HasWrite. A rule may scope only one direction.
	HasRead   bool
	HasWrite  bool
	Readable  bool
	Writable  bool
	Mask      MaskFunc
	WriteMask WriteMaskFunc
}

// readRule / writeRule presence flags on a matched rule.
type propAcc struct {
	hasReadAllow, hasReadDeny   bool
	hasWriteAllow, hasWriteDeny bool
	maskRules                   []PropRule // registration order
	writeMaskRules              []PropRule
	readAllowIDs                []string
	readDenyIDs                 []string
	writeAllowIDs               []string
	writeDenyIDs                []string
}

// PropPolicySet groups property rules under one merge mode. The merge
// mode is independent of (and never mixed with) the row-level mode.
type PropPolicySet struct {
	Mode  MergeMode
	Rules []PropRule
}

// PropState is the merged read conclusion for one property.
type PropState int

const (
	// PropNoPolicy: no matching read conclusion; default deny.
	PropNoPolicy PropState = iota
	// PropHidden: matching rules conclusively deny reading.
	PropHidden
	// PropRaw: readable, presented raw value.
	PropRaw
	// PropMasked: readable only via the derived masked value.
	PropMasked
)

func (s PropPolicySet) validate(ot *ObjectType) *DecisionError {
	if !s.Mode.valid() {
		return &DecisionError{ErrInvalidType, "property merge mode must be AllowOverrides or DenyOverrides"}
	}
	seen := map[string]bool{}
	for _, r := range s.Rules {
		if r.ID == "" {
			return &DecisionError{ErrInvalidType, "property rule id must not be empty"}
		}
		if seen[r.ID] {
			return &DecisionError{ErrInvalidType, "duplicate property rule id " + r.ID}
		}
		seen[r.ID] = true
		if !ot.Has(r.Property) {
			return &DecisionError{ErrUnknownProperty, "property rule " + r.ID + " references unknown property " + r.Property}
		}
	}
	return nil
}

// propVerdict is the deterministic per-property conclusion.
type propVerdict struct {
	state        PropState
	mask         MaskFunc
	writeMask    WriteMaskFunc
	writable     bool
	readAllowIDs []string
	readDenyIDs  []string
	maskRuleID   string
	writeRuleID  string
	writeAllowID []string
	writeDenyID  []string
}

// mergeProps evaluates the matching rules for one subject and produces
// a deterministic per-property verdict. Plain read/write conclusions
// are reduced independently under the declared merge mode. Whenever at
// least one masked-read rule matches, masked presentation wins over
// plain raw reading, and among several matching mask rules the rule
// with the smallest ID wins; the same applies to write-mask rules.
// This makes the verdict independent of registration/iteration order.
func (s PropPolicySet) mergeProps(sub Subject, candidates []int) map[string]propVerdict {
	accs := map[string]*propAcc{}
	for _, i := range candidates {
		r := s.Rules[i]
		if !r.Selector.matches(sub) {
			continue
		}
		a := accs[r.Property]
		if a == nil {
			a = &propAcc{}
			accs[r.Property] = a
		}
		hasRead := r.HasRead || r.Mask != nil
		hasWrite := r.HasWrite || r.WriteMask != nil
		if r.Mask != nil {
			a.maskRules = append(a.maskRules, r)
		} else if hasRead {
			if r.Readable {
				a.hasReadAllow = true
				a.readAllowIDs = append(a.readAllowIDs, r.ID)
			} else {
				a.hasReadDeny = true
				a.readDenyIDs = append(a.readDenyIDs, r.ID)
			}
		}
		if r.WriteMask != nil {
			a.writeMaskRules = append(a.writeMaskRules, r)
		} else if hasWrite {
			if r.Writable {
				a.hasWriteAllow = true
				a.writeAllowIDs = append(a.writeAllowIDs, r.ID)
			} else {
				a.hasWriteDeny = true
				a.writeDenyIDs = append(a.writeDenyIDs, r.ID)
			}
		}
	}
	out := map[string]propVerdict{}
	for prop, a := range accs {
		v := propVerdict{}
		if len(a.maskRules) > 0 {
			winner := smallestIDRule(a.maskRules)
			v.state = PropMasked
			v.mask = winner.Mask
			v.maskRuleID = winner.ID
		} else {
			v.state = mergeReadState(s.Mode, a.hasReadAllow, a.hasReadDeny)
			v.readAllowIDs = append(v.readAllowIDs, a.readAllowIDs...)
			v.readDenyIDs = append(v.readDenyIDs, a.readDenyIDs...)
		}
		if len(a.writeMaskRules) > 0 {
			winner := smallestIDRule(a.writeMaskRules)
			v.writeMask = winner.WriteMask
			v.writable = true
			v.writeRuleID = winner.ID
		} else {
			v.writable = mergeWrite(s.Mode, a.hasWriteAllow, a.hasWriteDeny)
			v.writeAllowID = append(v.writeAllowID, a.writeAllowIDs...)
			v.writeDenyID = append(v.writeDenyID, a.writeDenyIDs...)
		}
		out[prop] = v
	}
	return out
}

func smallestIDRule(rules []PropRule) PropRule {
	winner := rules[0]
	for _, r := range rules[1:] {
		if r.ID < winner.ID {
			winner = r
		}
	}
	return winner
}

func mergeReadState(mode MergeMode, allow, deny bool) PropState {
	switch {
	case allow && deny:
		if mode == AllowOverrides {
			return PropRaw
		}
		return PropHidden
	case allow:
		return PropRaw
	case deny:
		return PropHidden
	default:
		return PropNoPolicy
	}
}

func mergeWrite(mode MergeMode, allow, deny bool) bool {
	if allow && deny {
		return mode == AllowOverrides
	}
	return allow
}

// ReadResult is one adjudicated read response.
type ReadResult struct {
	// InstanceID and Visible mirror the request outcome. When Visible
	// is false the result carries no property data whatsoever.
	InstanceID string
	Visible    bool
	// View is the externally presented instance. It maps exactly the
	// declared properties that are both readable and have a raw value
	// to their presented (raw or masked) value.
	View map[string]RawValue
	// Redacted are declared properties that are present but unreadable.
	// Their mere presence (existence) is shown, never their value.
	Redacted []string
	// Absent are declared, readable properties that have NO raw value
	// (raw NULL), kept distinct from a present zero value, which
	// appears in View, and from an undeclared property, which appears
	// nowhere and is rejected on direct query.
	Absent []string
	// Conflict reports a masking/declared-type contract violation. When
	// non-nil, View is nil: a contract-breaking view is never returned,
	// and the conflict is scoped to the offending property only.
	Conflict *DecisionError
	// Trace records the policy basis for this verdict.
	Trace DecisionTrace
}

// WriteResult is one adjudicated write response.
type WriteResult struct {
	InstanceID string
	// Applied lists properties actually changed, in declaration order.
	Applied []string
	// Dropped lists properties silently skipped under WriteDrop.
	Dropped []string
	Version int64
	Trace   DecisionTrace
}

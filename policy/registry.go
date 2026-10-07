package policy

// Registry holds the immutable policy set and serves atomic snapshots.

import (
	"fmt"
	"sort"
	"sync"
	"sync/atomic"
)

type attrKey struct{ object, subject, attr string }
type osKey struct{ object, subject string }

// snapshot is one immutable revision of the policy store. Every render takes
// a pointer to one snapshot, so an in-flight request can never observe a mix
// of old and new policies.
type snapshot struct {
	revision  int64
	types     map[string]ObjectType
	visByKey  map[attrKey][]*VisibilityPolicy
	maskByKey map[attrKey][]*MaskingPolicy
	visByOS   map[osKey][]*VisibilityPolicy
	maskByOS  map[osKey][]*MaskingPolicy
	visTotal  int
	maskTot   int
}

// Registry is the concurrency-safe entry point. All mutators serialize on a
// single write lock; all renders take a read lock only to fetch the current
// snapshot pointer, then evaluate lock-free. This gives strict serializability:
// every concurrent history is equivalent to some sequential ordering.
type Registry struct {
	mu    sync.RWMutex
	snap  *snapshot
	audit *AuditLog

	revision atomic.Int64

	// Cumulative counters of policies actually touched while serving render
	// requests, making per-request cost observable without implementation access.
	visExamined  atomic.Int64
	maskExamined atomic.Int64
}

// NewRegistry creates an empty registry with an attached audit log.
func NewRegistry() *Registry {
	r := &Registry{audit: NewAuditLog()}
	r.snap = &snapshot{
		types:     map[string]ObjectType{},
		visByKey:  map[attrKey][]*VisibilityPolicy{},
		maskByKey: map[attrKey][]*MaskingPolicy{},
		visByOS:   map[osKey][]*VisibilityPolicy{},
		maskByOS:  map[osKey][]*MaskingPolicy{},
	}
	return r
}

// Audit returns the audit log (only committed, non-denied renders are recorded).
func (r *Registry) Audit() *AuditLog { return r.audit }

// Revision returns the current policy-set revision number.
func (r *Registry) Revision() int64 { return r.revision.Load() }

// TotalVisibilityPolicies / TotalMaskingPolicies report store-wide totals used
// by the cost-independence observability checks.
func (r *Registry) TotalVisibilityPolicies() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.snap.visTotal
}

func (r *Registry) TotalMaskingPolicies() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.snap.maskTot
}

// ExaminedVisibilityPolicies / ExaminedMaskingPolicies are cumulative counters
// of policies actually touched while serving render requests.
func (r *Registry) ExaminedVisibilityPolicies() int64 { return r.visExamined.Load() }
func (r *Registry) ExaminedMaskingPolicies() int64    { return r.maskExamined.Load() }

// Apply atomically replaces the complete policy set. The input is deep-copied
// via buildSnapshot, so later caller mutation cannot affect stored state.
// Structural validation (duplicate IDs, malformed rules) fails the whole
// replacement; semantic references to unknown objects/attributes are validated
// lazily per request and reported as MissingReference at evaluation time.
func (r *Registry) Apply(set PolicySet) error {
	next, err := buildSnapshot(set)
	if err != nil {
		return err
	}
	r.mu.Lock()
	next.revision = r.revision.Load() + 1
	r.snap = next
	r.mu.Unlock()
	r.revision.Store(next.revision)
	return nil
}

func buildSnapshot(set PolicySet) (*snapshot, error) {
	s := &snapshot{
		types:     make(map[string]ObjectType, len(set.Types)),
		visByKey:  map[attrKey][]*VisibilityPolicy{},
		maskByKey: map[attrKey][]*MaskingPolicy{},
		visByOS:   map[osKey][]*VisibilityPolicy{},
		maskByOS:  map[osKey][]*MaskingPolicy{},
	}
	for _, t := range set.Types {
		if _, dup := s.types[t.Name]; dup {
			return nil, fmt.Errorf("policy: duplicate object type %q", t.Name)
		}
		attrs := make(map[string]AttrType, len(t.Attrs))
		for name, ty := range t.Attrs {
			if err := validateAttrType(name, ty); err != nil {
				return nil, err
			}
			attrs[name] = ty
		}
		s.types[t.Name] = ObjectType{Name: t.Name, Attrs: attrs}
	}

	seenIDs := map[string]bool{}
	for i := range set.Visibility {
		p := set.Visibility[i]
		if p.ID == "" {
			return nil, fmt.Errorf("policy: visibility policy with empty id")
		}
		if seenIDs[p.ID] {
			return nil, fmt.Errorf("policy: duplicate policy id %q", p.ID)
		}
		seenIDs[p.ID] = true
		if p.Pred != nil {
			if p.Pred.CondAttr == "" {
				return nil, fmt.Errorf("policy: visibility policy %q has predicate without condition attribute", p.ID)
			}
			if _, ok := predOps[p.Pred.Op]; !ok {
				return nil, fmt.Errorf("policy: visibility policy %q has unknown predicate op %q", p.ID, p.Pred.Op)
			}
		}
		cp := p
		s.visByKey[attrKey{p.Object, p.Subject, p.Attr}] = append(s.visByKey[attrKey{p.Object, p.Subject, p.Attr}], &cp)
		ok2 := osKey{p.Object, p.Subject}
		s.visByOS[ok2] = append(s.visByOS[ok2], &cp)
		s.visTotal++
	}
	for i := range set.Masking {
		p := set.Masking[i]
		if p.ID == "" {
			return nil, fmt.Errorf("policy: masking policy with empty id")
		}
		if seenIDs[p.ID] {
			return nil, fmt.Errorf("policy: duplicate policy id %q", p.ID)
		}
		seenIDs[p.ID] = true
		if p.Strength <= StrengthNone {
			return nil, fmt.Errorf("policy: masking policy %q has no strength", p.ID)
		}
		if err := validateRule(p.ID, p.Rule); err != nil {
			return nil, err
		}
		cp := p
		s.maskByKey[attrKey{p.Object, p.Subject, p.Attr}] = append(s.maskByKey[attrKey{p.Object, p.Subject, p.Attr}], &cp)
		ok2 := osKey{p.Object, p.Subject}
		s.maskByOS[ok2] = append(s.maskByOS[ok2], &cp)
		s.maskTot++
	}
	// Index slices are kept sorted by policy id so every tie-break and cycle
	// report is independent of registration order.
	for _, list := range s.visByKey {
		sortVis(list)
	}
	for _, list := range s.maskByKey {
		sortMask(list)
	}
	for k, list := range s.visByOS {
		sortVis(list)
		s.visByOS[k] = list
	}
	for k, list := range s.maskByOS {
		sortMask(list)
		s.maskByOS[k] = list
	}
	return s, nil
}

func validateRule(id string, rule DerivedRule) error {
	switch rule.Kind {
	case RuleRedact, RuleHash, RuleMask:
		return nil
	case RuleConst:
		if rule.ConstVal == nil {
			return fmt.Errorf("policy: masking policy %q const rule needs ConstVal", id)
		}
	case RuleCopyDerived:
		if rule.SourceAttr == "" {
			return fmt.Errorf("policy: masking policy %q copy-derived rule needs SourceAttr", id)
		}
	default:
		return fmt.Errorf("policy: masking policy %q has unknown rule kind %d", id, rule.Kind)
	}
	return nil
}

func validateAttrType(name string, ty AttrType) error {
	switch ty.Kind {
	case KindString, KindInt, KindFloat, KindBool:
	default:
		return fmt.Errorf("policy: attribute %q has unknown kind %d", name, ty.Kind)
	}
	if ty.Min != nil && ty.Max != nil && *ty.Min > *ty.Max {
		return fmt.Errorf("policy: attribute %q has min greater than max", name)
	}
	return nil
}

func sortVis(list []*VisibilityPolicy) {
	sort.Slice(list, func(i, j int) bool { return list[i].ID < list[j].ID })
}

func sortMask(list []*MaskingPolicy) {
	sort.Slice(list, func(i, j int) bool { return list[i].ID < list[j].ID })
}

// currentSnap fetches the immutable snapshot under a read lock.
func (r *Registry) currentSnap() *snapshot {
	r.mu.RLock()
	s := r.snap
	r.mu.RUnlock()
	return s
}

// visOS / maskOS return the only policies relevant to one render: those
// registered for the exact (object type, subject) pair. The returned slices
// must not be mutated by callers.
func (s *snapshot) visOS(object, subject string) []*VisibilityPolicy {
	return s.visByOS[osKey{object, subject}]
}

func (s *snapshot) maskOS(object, subject string) []*MaskingPolicy {
	return s.maskByOS[osKey{object, subject}]
}

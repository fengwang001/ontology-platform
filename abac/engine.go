package abac

import (
	"errors"
	"fmt"
	"log/slog"
	"reflect"
	"sort"
	"sync"
)

// Engine evaluates access requests against a registered policy set.
// It is safe for concurrent use.
type Engine struct {
	mu       sync.RWMutex
	policies []Policy // kept sorted by ID for deterministic evaluation
	logger   *slog.Logger
}

// NewEngine returns an engine logging to logger (nil discards logs).
func NewEngine(logger *slog.Logger) *Engine {
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	return &Engine{logger: logger}
}

// RegisterPolicy validates and adds a policy. On validation error the
// policy set is left unchanged. Registration order does not affect
// decisions: policies are stored sorted by ID.
func (e *Engine) RegisterPolicy(p Policy) error {
	if err := validatePolicy(p); err != nil {
		e.logger.Warn("abac: policy registration rejected",
			"policy", p.ID, "error", err)
		return err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, existing := range e.policies {
		if existing.ID == p.ID {
			err := fmt.Errorf("abac: duplicate policy id %q", p.ID)
			e.logger.Warn("abac: policy registration rejected",
				"policy", p.ID, "error", err)
			return err
		}
	}
	e.policies = append(e.policies, p)
	sort.Slice(e.policies, func(i, j int) bool {
		return e.policies[i].ID < e.policies[j].ID
	})
	e.logger.Info("abac: policy registered",
		"policy", p.ID, "effect", p.Effect.String(),
		"conditions", len(p.Conditions))
	return nil
}

// Policies returns a copy of the registered policies, sorted by ID.
func (e *Engine) Policies() []Policy {
	e.mu.RLock()
	defer e.mu.RUnlock()
	out := make([]Policy, len(e.policies))
	copy(out, e.policies)
	return out
}

// Evaluate computes the decision for req. It never mutates the policy
// set and never reveals whether the resource exists.
func (e *Engine) Evaluate(req Request) Decision {
	e.mu.RLock()
	policies := make([]Policy, len(e.policies))
	copy(policies, e.policies)
	e.mu.RUnlock()

	// A missing resource yields the same generic denial as "no permit
	// policy matched", so callers cannot probe resource existence.
	if req.Resource == nil {
		dec := Decision{Reason: ReasonNoApplicablePermit}
		e.logDecision(req, dec, nil)
		return dec
	}

	var matched []string
	deny := false
	permit := false
	for _, p := range policies {
		if !policyMatches(p, req) {
			continue
		}
		matched = append(matched, p.ID)
		if p.Effect == EffectDeny {
			deny = true
		} else {
			permit = true
		}
	}

	dec := Decision{MatchedPolicies: matched}
	switch {
	case deny:
		// Deny-overrides: any matching deny beats every permit.
		dec.Reason = ReasonDeniedByPolicy
	case permit:
		dec.Allowed = true
		dec.Reason = ReasonPermitted
	default:
		dec.Reason = ReasonNoApplicablePermit
	}
	e.logDecision(req, dec, policies)
	return dec
}

func (e *Engine) logDecision(req Request, dec Decision, policies []Policy) {
	e.logger.Info("abac: decision",
		"subject", map[string]any(req.Subject),
		"resource", map[string]any(req.Resource),
		"environment", map[string]any(req.Environment),
		"action", req.Action,
		"allowed", dec.Allowed,
		"reason", dec.Reason.String(),
		"matchedPolicies", dec.MatchedPolicies,
		"policyCount", len(policies),
	)
}

func validatePolicy(p Policy) error {
	if p.ID == "" {
		return errors.New("abac: policy id must not be empty")
	}
	if p.Effect != EffectPermit && p.Effect != EffectDeny {
		return fmt.Errorf("abac: policy %q has invalid effect", p.ID)
	}
	if len(p.Conditions) == 0 {
		return fmt.Errorf("abac: policy %q must have at least one condition", p.ID)
	}
	for _, c := range p.Conditions {
		if c.Key == "" {
			return fmt.Errorf("abac: policy %q has condition with empty key", p.ID)
		}
		if c.Scope < ScopeSubject || c.Scope > ScopeEnvironment {
			return fmt.Errorf("abac: policy %q has condition with invalid scope", p.ID)
		}
		if c.Op < OpEq || c.Op > OpIn {
			return fmt.Errorf("abac: policy %q has condition with invalid operator", p.ID)
		}
	}
	return nil
}

// policyMatches reports whether every condition of p is satisfied.
// A condition referencing a missing attribute is unsatisfied, so the
// policy is simply not applicable — the attribute is never coerced
// to a zero/false value.
func policyMatches(p Policy, req Request) bool {
	for _, c := range p.Conditions {
		if !conditionMatches(c, req) {
			return false
		}
	}
	return true
}

func conditionMatches(c Condition, req Request) bool {
	var attrs Attributes
	switch c.Scope {
	case ScopeSubject:
		attrs = req.Subject
	case ScopeResource:
		attrs = req.Resource
	case ScopeEnvironment:
		attrs = req.Environment
	}
	val, ok := attrs[c.Key]
	if !ok {
		return false
	}
	return compare(c.Op, val, c.Value)
}

func compare(op Operator, attr, want any) bool {
	switch op {
	case OpEq:
		return reflect.DeepEqual(attr, want)
	case OpNeq:
		return !reflect.DeepEqual(attr, want)
	case OpGt, OpGte, OpLt, OpLte:
		ord, ok := compareOrdered(attr, want)
		if !ok {
			return false
		}
		switch op {
		case OpGt:
			return ord > 0
		case OpGte:
			return ord >= 0
		case OpLt:
			return ord < 0
		default:
			return ord <= 0
		}
	case OpIn:
		return inList(attr, want)
	default:
		return false
	}
}

// compareOrdered compares numbers and strings. It returns ok=false for
// mismatched or unordered types, which makes the condition unsatisfied.
func compareOrdered(a, b any) (ord int, ok bool) {
	if af, aok := toFloat(a); aok {
		bf, bok := toFloat(b)
		if !bok {
			return 0, false
		}
		switch {
		case af < bf:
			return -1, true
		case af > bf:
			return 1, true
		default:
			return 0, true
		}
	}
	as, aok := a.(string)
	bs, bok := b.(string)
	if !aok || !bok {
		return 0, false
	}
	switch {
	case as < bs:
		return -1, true
	case as > bs:
		return 1, true
	default:
		return 0, true
	}
}

func toFloat(v any) (float64, bool) {
	switch n := v.(type) {
	case int:
		return float64(n), true
	case int8:
		return float64(n), true
	case int16:
		return float64(n), true
	case int32:
		return float64(n), true
	case int64:
		return float64(n), true
	case uint:
		return float64(n), true
	case uint8:
		return float64(n), true
	case uint16:
		return float64(n), true
	case uint32:
		return float64(n), true
	case uint64:
		return float64(n), true
	case float32:
		return float64(n), true
	case float64:
		return n, true
	default:
		return 0, false
	}
}

// inList reports whether attr equals any element of want, where want
// must be a slice or array.
func inList(attr, want any) bool {
	rv := reflect.ValueOf(want)
	if rv.Kind() != reflect.Slice && rv.Kind() != reflect.Array {
		return false
	}
	for i := 0; i < rv.Len(); i++ {
		if reflect.DeepEqual(attr, rv.Index(i).Interface()) {
			return true
		}
	}
	return false
}

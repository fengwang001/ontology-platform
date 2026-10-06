package mesh

import (
	"sync"
	"sync/atomic"
)

// snapshot is an immutable published generation. Routing holds a pointer to
// one snapshot for the whole call and therefore can never observe a
// half-published configuration.
type snapshot struct {
	cfg     *Config
	idx     *pathIndex
	version int
}

func cloneIntPtr(p *int) *int {
	if p == nil {
		return nil
	}
	v := *p
	return &v
}

func clonePolicy(p Policy) Policy {
	return Policy{
		Timeout:        cloneIntPtr(p.Timeout),
		Retries:        cloneIntPtr(p.Retries),
		PerAttemptTime: cloneIntPtr(p.PerAttemptTime),
	}
}

// cloneConfig deep-codes a caller config so later mutation of the input can
// never affect published state, and so returned configs are independent.
func cloneConfig(c *Config) *Config {
	if c == nil {
		return nil
	}
	cp := &Config{Default: clonePolicy(c.Default)}
	cp.Rules = make([]Rule, len(c.Rules))
	for ri := range c.Rules {
		src := &c.Rules[ri]
		dst := &cp.Rules[ri]
		dst.Matchers = make([]Matcher, len(src.Matchers))
		for mi := range src.Matchers {
			dst.Matchers[mi].Path = src.Matchers[mi].Path
			if src.Matchers[mi].Path != nil {
				p := *src.Matchers[mi].Path
				dst.Matchers[mi].Path = &p
			}
			dst.Matchers[mi].Headers = append([]HeaderCondition(nil), src.Matchers[mi].Headers...)
		}
		dst.Targets = append([]Target(nil), src.Targets...)
		if src.Policy != nil {
			p := clonePolicy(*src.Policy)
			dst.Policy = &p
		}
	}
	cp.Fallback = append([]Target(nil), c.Fallback...)
	return cp
}

// Service is one target service: its routing configurations, current
// version and its subset registry.
type Service struct {
	pubMu sync.Mutex
	snap  atomic.Pointer[snapshot]

	// Registry is exposed for subset registration and readiness changes.
	Registry *Registry
}

// NewService creates a service with no published config (version 0).
func NewService() *Service {
	return &Service{Registry: NewRegistry()}
}

// CurrentVersion returns the version of the live configuration (0 before
// the first successful publication).
func (s *Service) CurrentVersion() int {
	if snap := s.snap.Load(); snap != nil {
		return snap.version
	}
	return 0
}

// Publish atomically validates and replaces the live configuration.
// expectedVersion must equal the current version; the new version is
// current+1. A rejected publication changes neither config nor version.
func (s *Service) Publish(cfg *Config, expectedVersion int) (int, *Error) {
	s.pubMu.Lock()
	defer s.pubMu.Unlock()

	cur := 0
	if snap := s.snap.Load(); snap != nil {
		cur = snap.version
	}
	if expectedVersion != cur {
		return cur, &Error{Kind: KindVersionConflict,
			Message: "expected version " + itoa(expectedVersion) + ", current " + itoa(cur)}
	}

	owned := cloneConfig(cfg)
	if err := validateConfig(owned); err != nil {
		return cur, err
	}
	next := &snapshot{
		cfg:     owned,
		idx:     buildPathIndex(owned),
		version: cur + 1,
	}
	s.snap.Store(next)
	return next.version, nil
}

// Config returns an independent deep copy of the live configuration.
func (s *Service) Config() *Config {
	snap := s.snap.Load()
	if snap == nil {
		return nil
	}
	return cloneConfig(snap.cfg)
}

// pickTarget maps a bucket in [0,9999] onto a target by declaration order:
// a target of weight w owns w*100 consecutive bucket values; weight-zero
// targets own none.
func pickTarget(targets []Target, bucket int) int {
	cut := 0
	for i := range targets {
		cut += targets[i].Weight * 100
		if bucket < cut {
			return i
		}
	}
	return -1
}

func effectiveToResult(p EffectivePolicy) EffectivePolicy {
	return EffectivePolicy{
		Timeout:        cloneIntPtr(p.Timeout),
		Retries:        cloneIntPtr(p.Retries),
		PerAttemptTime: cloneIntPtr(p.PerAttemptTime),
	}
}

// Route resolves one request against the live configuration. Error kinds:
// KindInvalidArgument (bad request), KindNoRoute, KindNoEndpoint.
func (s *Service) Route(req Request) (*RouteResult, *Error) {
	if req.Bucket < 0 || req.Bucket > 9999 {
		return nil, &Error{Kind: KindInvalidArgument, Message: "bucket out of range [0,9999]"}
	}
	snap := s.snap.Load()
	if snap == nil {
		return nil, &Error{Kind: KindNoRoute, Message: "no published config"}
	}

	stripped := stripQuery(req.Path)
	headers := canonicalizeHeaders(req.Headers)

	var targets []Target
	var ruleIdx int
	var effPolicy EffectivePolicy
	fromFallback := false

	winningRule := -1
	for _, c := range snap.idx.candidatesFor(stripped) {
		m := snap.cfg.Rules[c.ruleIdx].Matchers[c.matchIdx]
		if matcherMatches(m, stripped, headers) {
			winningRule = c.ruleIdx
			break // candidates are ordered by (rule, matcher)
		}
	}

	if winningRule >= 0 {
		ruleIdx = winningRule
		targets = snap.cfg.Rules[winningRule].Targets
		effPolicy = mergePolicy(snap.cfg.Default, snap.cfg.Rules[winningRule].Policy)
	} else if len(snap.cfg.Fallback) > 0 {
		ruleIdx = FallbackRuleIndex
		targets = snap.cfg.Fallback
		effPolicy = mergePolicy(snap.cfg.Default, nil)
		fromFallback = true
	} else {
		return nil, &Error{Kind: KindNoRoute, Message: "no rule matched and no fallback"}
	}

	targetIdx := pickTarget(targets, req.Bucket)
	if targetIdx < 0 {
		// Unreachable for a validated config; kept defensive.
		return nil, &Error{Kind: KindNoEndpoint, RuleIndex: ruleIdx, Message: "bucket past weights"}
	}
	subset := targets[targetIdx].Subset
	if !s.Registry.hasReadyEndpoint(subset) {
		return nil, &Error{Kind: KindNoEndpoint, RuleIndex: ruleIdx,
			Message: "no ready endpoint in subset: " + subset}
	}
	return &RouteResult{
		Subset:       subset,
		RuleIndex:    ruleIdx,
		TargetIdx:    targetIdx,
		FromFallback: fromFallback,
		Policy:       effectiveToResult(effPolicy),
	}, nil
}

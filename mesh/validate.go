package mesh

// validateStructural checks malformed parameters (highest priority error
// class). It is independent of weight/policy/shadow validation.
func validateStructural(cfg *Config) *Error {
	if cfg == nil {
		return &Error{Kind: KindInvalidArgument, Message: "nil config"}
	}
	checkPolicy := func(ruleIdx int, p *Policy) *Error {
		if p == nil {
			return nil
		}
		if p.Timeout != nil && *p.Timeout < 0 {
			return &Error{Kind: KindInvalidArgument, RuleIndex: ruleIdx, Message: "negative timeout"}
		}
		if p.Retries != nil && *p.Retries < 0 {
			return &Error{Kind: KindInvalidArgument, RuleIndex: ruleIdx, Message: "negative retries"}
		}
		if p.PerAttemptTime != nil && *p.PerAttemptTime < 0 {
			return &Error{Kind: KindInvalidArgument, RuleIndex: ruleIdx, Message: "negative per-attempt timeout"}
		}
		return nil
	}
	if err := checkPolicy(FallbackRuleIndex, &cfg.Default); err != nil {
		return err
	}
	validateTargetsShape := func(ruleIdx int, targets []Target) *Error {
		for i := range targets {
			if targets[i].Subset == "" {
				return &Error{Kind: KindWeightInvalid, RuleIndex: ruleIdx,
					Message: "empty subset name in target[" + itoa(i) + "]"}
			}
		}
		return nil
	}
	for ri := range cfg.Rules {
		r := &cfg.Rules[ri]
		if len(r.Matchers) == 0 {
			return &Error{Kind: KindInvalidArgument, RuleIndex: ri, Message: "rule has no matcher"}
		}
		if len(r.Targets) == 0 {
			return &Error{Kind: KindInvalidArgument, RuleIndex: ri, Message: "rule has no targets"}
		}
		for mi := range r.Matchers {
			m := &r.Matchers[mi]
			if m.Path != nil {
				pc := m.Path
				if pc.Kind != PathExact && pc.Kind != PathPrefix {
					return &Error{Kind: KindInvalidArgument, RuleIndex: ri, Message: "unknown path kind"}
				}
				if pc.Value == "" || pc.Value[0] != '/' {
					return &Error{Kind: KindInvalidArgument, RuleIndex: ri, Message: "path must start with '/'"}
				}
			}
			for hi := range m.Headers {
				hc := &m.Headers[hi]
				if hc.Name == "" {
					return &Error{Kind: KindInvalidArgument, RuleIndex: ri, Message: "empty header name"}
				}
				if hc.Op != HeaderExact && hc.Op != HeaderPrefix && hc.Op != HeaderExists {
					return &Error{Kind: KindInvalidArgument, RuleIndex: ri, Message: "unknown header op"}
				}
			}
		}
		if err := checkPolicy(ri, r.Policy); err != nil {
			return err
		}
		if err := validateTargetsShape(ri, r.Targets); err != nil {
			return err
		}
	}
	if err := validateTargetsShape(FallbackRuleIndex, cfg.Fallback); err != nil {
		return err
	}
	return nil
}

// validateWeights checks range and sum for every target list, reporting the
// smallest rule index first (fallback last).
func validateWeights(cfg *Config) *Error {
	check := func(ruleIdx int, targets []Target) *Error {
		if len(targets) == 0 {
			return nil
		}
		sum := 0
		for i := range targets {
			w := targets[i].Weight
			if w < 0 || w > 100 {
				return &Error{Kind: KindWeightInvalid, RuleIndex: ruleIdx,
					Message: "weight out of range [0,100] at target[" + itoa(i) + "]"}
			}
			sum += w
		}
		if sum != 100 {
			return &Error{Kind: KindWeightInvalid, RuleIndex: ruleIdx,
				Message: "weights sum to " + itoa(sum) + ", want 100"}
		}
		return nil
	}
	for ri := range cfg.Rules {
		if err := check(ri, cfg.Rules[ri].Targets); err != nil {
			return err
		}
	}
	return check(FallbackRuleIndex, cfg.Fallback)
}

// validatePolicies checks the effective policy of each rule and the service
// default policy used by the fallback.
func validatePolicies(cfg *Config) *Error {
	for ri := range cfg.Rules {
		eff := mergePolicy(cfg.Default, cfg.Rules[ri].Policy)
		if err := validateEffectivePolicy(eff); err != nil {
			err.RuleIndex = ri
			return err
		}
	}
	eff := EffectivePolicy{
		Timeout:        cfg.Default.Timeout,
		Retries:        cfg.Default.Retries,
		PerAttemptTime: cfg.Default.PerAttemptTime,
	}
	if err := validateEffectivePolicy(eff); err != nil {
		err.RuleIndex = FallbackRuleIndex
		return err
	}
	return nil
}

// validateShadows rejects a rule when EVERY one of its matchers is fully
// covered by some matcher of any earlier rule. The covering matcher may
// differ per covered matcher; the covering matcher is always chosen from a
// rule that precedes the checked rule. The smallest rule index is reported.
func validateShadows(cfg *Config) *Error {
	for i := 1; i < len(cfg.Rules); i++ {
		allCovered := true
		for mi := range cfg.Rules[i].Matchers {
			later := cfg.Rules[i].Matchers[mi]
			covered := false
			for j := 0; j < i; j++ {
				for mj := range cfg.Rules[j].Matchers {
					if matcherCovers(cfg.Rules[j].Matchers[mj], later) {
						covered = true
						break
					}
				}
				if covered {
					break
				}
			}
			if !covered {
				allCovered = false
				break
			}
		}
		if allCovered {
			return &Error{Kind: KindShadowedRule, RuleIndex: i,
				Message: "rule is fully shadowed by earlier rules"}
		}
	}
	return nil
}

// validateConfig runs the four publish-time phases in fixed priority order:
// structural (invalid argument), weights, policies, shadows.
func validateConfig(cfg *Config) *Error {
	if err := validateStructural(cfg); err != nil {
		return err
	}
	if err := validateWeights(cfg); err != nil {
		return err
	}
	if err := validatePolicies(cfg); err != nil {
		return err
	}
	return validateShadows(cfg)
}

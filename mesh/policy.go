package mesh

// mergePolicy resolves the effective policy for a rule by taking each field
// from the rule override when present and otherwise from the service
// default; an absent field stays nil (unset).
func mergePolicy(def Policy, override *Policy) EffectivePolicy {
	eff := EffectivePolicy{
		Timeout:        def.Timeout,
		Retries:        def.Retries,
		PerAttemptTime: def.PerAttemptTime,
	}
	if override != nil {
		if override.Timeout != nil {
			eff.Timeout = override.Timeout
		}
		if override.Retries != nil {
			eff.Retries = override.Retries
		}
		if override.PerAttemptTime != nil {
			eff.PerAttemptTime = override.PerAttemptTime
		}
	}
	return eff
}

// validateEffectivePolicy enforces:
//   - per-attempt timeout must not exceed the overall timeout (both set);
//   - retries * per-attempt-timeout must not exceed the timeout (all set).
func validateEffectivePolicy(p EffectivePolicy) *Error {
	if p.PerAttemptTime != nil && p.Timeout != nil && *p.PerAttemptTime > *p.Timeout {
		return &Error{Kind: KindPolicyInvalid, Message: "per-attempt timeout exceeds timeout"}
	}
	if p.Retries != nil && p.PerAttemptTime != nil && p.Timeout != nil &&
		*p.Retries**p.PerAttemptTime > *p.Timeout {
		return &Error{Kind: KindPolicyInvalid, Message: "retries * per-attempt-timeout exceeds timeout"}
	}
	return nil
}

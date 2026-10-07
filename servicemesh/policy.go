package servicemesh

import "time"

// mergePolicy 逐字段继承：override 缺省的字段取 def。
func mergePolicy(override, def *Policy) Policy {
	out := Policy{}
	if override != nil && override.Timeout != nil {
		out.Timeout = override.Timeout
	} else if def.Timeout != nil {
		out.Timeout = def.Timeout
	}
	if override != nil && override.PerAttemptTimeout != nil {
		out.PerAttemptTimeout = override.PerAttemptTimeout
	} else if def.PerAttemptTimeout != nil {
		out.PerAttemptTimeout = def.PerAttemptTimeout
	}
	if override != nil && override.MaxRetries != nil {
		out.MaxRetries = override.MaxRetries
	} else if def.MaxRetries != nil {
		out.MaxRetries = def.MaxRetries
	}
	return out
}

// validatePolicy 校验生效策略：每次尝试超时<=超时；重试*每次尝试超时<=超时。
// ruleIdx<0 表示兜底所用的服务默认策略。
func validatePolicy(p Policy, ruleIdx int) *RouteError {
	if p.PerAttemptTimeout != nil && p.Timeout != nil {
		if *p.PerAttemptTimeout > *p.Timeout {
			return errValidation(ValPolicy, ruleIdx,
				"per-attempt timeout %s exceeds timeout %s", *p.PerAttemptTimeout, *p.Timeout)
		}
	}
	if p.MaxRetries != nil && p.PerAttemptTimeout != nil && p.Timeout != nil {
		if time.Duration(*p.MaxRetries)**p.PerAttemptTimeout > *p.Timeout {
			return errValidation(ValPolicy, ruleIdx,
				"maxRetries(%d)*perAttemptTimeout(%s) exceeds timeout %s",
				*p.MaxRetries, *p.PerAttemptTimeout, *p.Timeout)
		}
	}
	return nil
}

package servicemesh

func clonePolicy(p Policy) Policy {
	c := Policy{}
	if p.Timeout != nil {
		v := *p.Timeout
		c.Timeout = &v
	}
	if p.PerAttemptTimeout != nil {
		v := *p.PerAttemptTimeout
		c.PerAttemptTimeout = &v
	}
	if p.MaxRetries != nil {
		v := *p.MaxRetries
		c.MaxRetries = &v
	}
	return c
}

func cloneTargets(ts []Target) []Target {
	if ts == nil {
		return nil
	}
	out := make([]Target, len(ts))
	copy(out, ts)
	return out
}

func cloneConfig(in *ServiceConfig) ServiceConfig {
	out := ServiceConfig{Fallbacks: cloneTargets(in.Fallbacks), Default: clonePolicy(in.Default)}
	for _, r := range in.Rules {
		cr := Rule{Targets: cloneTargets(r.Targets)}
		if r.Override != nil {
			p := clonePolicy(*r.Override)
			cr.Override = &p
		}
		for _, mi := range r.Matches {
			ci := MatchItem{Path: mi.Path}
			if mi.Headers != nil {
				ci.Headers = append([]HeaderMatch(nil), mi.Headers...)
			}
			cr.Matches = append(cr.Matches, ci)
		}
		out.Rules = append(out.Rules, cr)
	}
	return out
}

// toServiceConfig 将编译产物转回对外配置（深拷贝），供 GetConfig 使用。
func (c *compiledConfig) toServiceConfig() ServiceConfig {
	out := ServiceConfig{
		Fallbacks: cloneTargets(c.fallbacks),
		Default:   clonePolicy(c.def),
		Rules:     make([]Rule, len(c.rules)),
	}
	for ri, cr := range c.rules {
		p := clonePolicy(cr.policies)
		out.Rules[ri] = Rule{
			Matches:  cloneMatchItems(c.matches[ri]),
			Targets:  cloneTargets(cr.targets),
			Override: &p,
		}
	}
	return out
}

func cloneMatchItems(in []MatchItem) []MatchItem {
	out := make([]MatchItem, len(in))
	for i, m := range in {
		out[i] = MatchItem{Path: m.Path}
		if m.Headers != nil {
			out[i].Headers = append([]HeaderMatch(nil), m.Headers...)
		}
	}
	return out
}

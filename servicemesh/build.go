package servicemesh

func validPathShape(p PathMatch) bool {
	if p.Path == "" || p.Path[0] != '/' {
		return false
	}
	if p.Path == "/" {
		return true
	}
	if p.Path[len(p.Path)-1] == '/' {
		return false
	}
	for i := 1; i < len(p.Path); i++ {
		if p.Path[i] == '/' && p.Path[i-1] == '/' {
			return false
		}
	}
	return true
}

func validatePolicyShape(p Policy) *RouteError {
	if p.Timeout != nil && *p.Timeout < 0 {
		return errInvalid("negative timeout")
	}
	if p.PerAttemptTimeout != nil && *p.PerAttemptTimeout <= 0 {
		return errInvalid("non-positive per-attempt timeout")
	}
	if p.MaxRetries != nil && *p.MaxRetries < 0 {
		return errInvalid("negative max retries")
	}
	return nil
}

func validateWeights(targets []Target, ruleIdx int) *RouteError {
	sum := 0
	for _, t := range targets {
		if t.Subset == "" {
			return errValidation(ValWeight, ruleIdx, "empty subset name")
		}
		if t.Weight < 0 || t.Weight > 100 {
			return errValidation(ValWeight, ruleIdx,
				"weight %d out of range [0,100] for subset %q", t.Weight, t.Subset)
		}
		sum += t.Weight
	}
	if sum != 100 {
		return errValidation(ValWeight, ruleIdx, "weight sum %d != 100", sum)
	}
	return nil
}

// detectShadow 拒绝每个匹配项都被前面某条规则的某个匹配项完全覆盖的规则。
func detectShadow(rules []Rule) *RouteError {
	for ri := 1; ri < len(rules); ri++ {
		allCovered := true
		for _, cur := range rules[ri].Matches {
			covered := false
			for pj := 0; pj < ri; pj++ {
				for _, prev := range rules[pj].Matches {
					if itemCovers(prev, cur) {
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
			return errValidation(ValShadowed, ri, "rule %d is fully shadowed by earlier rules", ri)
		}
	}
	return nil
}

// compile 深拷贝入参并构建索引；结构问题=参数非法，语义问题=校验失败。
func compile(in *ServiceConfig) (*compiledConfig, *RouteError) {
	if in == nil {
		return nil, errInvalid("nil service config")
	}
	// 第一阶段：结构合法性（参数非法）。
	for ri, r := range in.Rules {
		if len(r.Matches) == 0 {
			return nil, errInvalid("rule %d has no match items", ri)
		}
		if len(r.Targets) == 0 {
			return nil, errInvalid("rule %d has no targets", ri)
		}
		for mi, m := range r.Matches {
			if !validPathShape(m.Path) {
				return nil, errInvalid("rule %d match %d has invalid path %q", ri, mi, m.Path.Path)
			}
			for _, h := range m.Headers {
				if headerName(h.Name) == "" {
					return nil, errInvalid("rule %d match %d has empty header name", ri, mi)
				}
				if h.Op < HeaderExact || h.Op > HeaderPresent {
					return nil, errInvalid("rule %d match %d has invalid header op", ri, mi)
				}
			}
		}
		if r.Override != nil {
			if e := validatePolicyShape(*r.Override); e != nil {
				return nil, e
			}
		}
	}
	if e := validatePolicyShape(in.Default); e != nil {
		return nil, e
	}

	// 之后只操作深拷贝副本。
	cfg := cloneConfig(in)

	// 第二阶段 A：分权重类（规则先于兜底，同类取最小序号）。
	for ri, r := range cfg.Rules {
		if e := validateWeights(r.Targets, ri); e != nil {
			return nil, e
		}
	}
	if len(cfg.Fallbacks) > 0 {
		if e := validateWeights(cfg.Fallbacks, -1); e != nil {
			return nil, e
		}
	}

	// 第二阶段 B：策略类。
	compiledRules := make([]compiledRule, len(cfg.Rules))
	for ri, r := range cfg.Rules {
		eff := mergePolicy(r.Override, &cfg.Default)
		if e := validatePolicy(eff, ri); e != nil {
			return nil, e
		}
		compiledRules[ri] = compiledRule{targets: r.Targets, policies: eff}
	}
	if len(cfg.Fallbacks) > 0 {
		if e := validatePolicy(cfg.Default, -1); e != nil {
			return nil, e
		}
	}

	// 第二阶段 C：遮蔽类。
	if e := detectShadow(cfg.Rules); e != nil {
		return nil, e
	}

	// 第三阶段：构建索引。
	idx := newIndex()
	rawMatches := make([][]MatchItem, len(cfg.Rules))
	for ri, r := range cfg.Rules {
		rawMatches[ri] = make([]MatchItem, len(r.Matches))
		for mi, m := range r.Matches {
			hm := make([]HeaderMatch, len(m.Headers))
			for hi, h := range m.Headers {
				hm[hi] = HeaderMatch{Name: headerName(h.Name), Op: h.Op, Value: h.Value}
			}
			rawMatches[ri][mi] = MatchItem{Path: m.Path, Headers: append([]HeaderMatch(nil), hm...)}
			mt := matcher{ruleIdx: ri, matchIdx: mi, path: m.Path, headers: hm}
			chooseDriver(&mt)
			idx.add(mt)
		}
	}
	return &compiledConfig{
		rules:     compiledRules,
		fallbacks: cfg.Fallbacks,
		def:       cfg.Default,
		index:     idx,
		matches:   rawMatches,
	}, nil
}

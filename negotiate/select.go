package negotiate

// Select 按 HTTP Accept 头语义从 offers 中选出最合适的媒体类型。
// 返回选中的 offer 原文与命中的 Accept 规则原文。
// offers 按服务端优先级从高到低排列；q 相同则排前者胜出。
func Select(accept string, offers []string) (string, string, error) {
	rules, err := parseRules(accept)
	if err != nil {
		return "", "", err
	}
	if len(offers) == 0 {
		return "", "", ErrNotAcceptable
	}
	best, bestQ, bestRule := -1, -1.0, ""
	for i, raw := range offers {
		ruleIdx, q := bestRuleFor(rules, parseOffer(raw))
		if ruleIdx < 0 || q == 0 {
			continue // 无匹配规则，或被 q=0 明确拒绝
		}
		if q > bestQ {
			best, bestQ, bestRule = i, q, rules[ruleIdx].raw
		}
	}
	if best < 0 {
		return "", "", ErrNotAcceptable
	}
	return offers[best], bestRule, nil
}

// bestRuleFor 返回匹配该 offer 的最具体规则下标及其 q 值；
// 具体度相同取 Accept 头中先出现者；无匹配返回 -1。
func bestRuleFor(rules []rule, o offer) (int, float64) {
	best, bestSpec := -1, -1
	for i, r := range rules {
		spec, ok := r.match(o)
		if ok && spec > bestSpec {
			best, bestSpec = i, spec
		}
	}
	if best < 0 {
		return -1, 0
	}
	return best, rules[best].q
}

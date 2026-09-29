package featureflag

import "sort"

// validate 对整份规则集做发布前校验。
//
// 多类错误同时存在时，严格按以下顺序只报第一类：
//  1. 变体引用不存在（引用了开关 Variants 中未声明的变体）
//  2. 权重为负
//  3. 权重之和不为 10000
//  4. 前置开关不存在
//  5. 前置依赖成环
//
// 同类错误按开关名字典序、声明顺序确定，保证报错可复现。
func validate(rules RuleSet) error {
	names := make([]string, 0, len(rules))
	for name := range rules {
		names = append(names, name)
	}
	sort.Strings(names)

	// Pass 1：所有变体引用必须在所属开关的 Variants 中声明。
	for _, name := range names {
		sw := rules[name]
		declared := make(map[string]bool, len(sw.Variants)+1)
		for _, v := range sw.Variants {
			declared[v] = true
		}
		declared[sw.OffVariant] = true

		local := func(variant, where string) error {
			if !declared[variant] {
				return &PublishError{Cause: ErrUnknownVariant, Flag: name, Detail: where + " variant=" + variant}
			}
			return nil
		}

		// 前置要求的变体必须在「前置开关」中声明。
		for _, p := range sw.Prerequisites {
			if pre, ok := rules[p.Flag]; ok {
				preDeclared := map[string]bool{pre.OffVariant: true}
				for _, v := range pre.Variants {
					preDeclared[v] = true
				}
				if !preDeclared[p.RequiredVariant] {
					return &PublishError{Cause: ErrUnknownVariant, Flag: name,
						Detail: "prerequisite=" + p.Flag + " variant=" + p.RequiredVariant}
				}
			}
		}

		for i := range sw.Targeting {
			rule := &sw.Targeting[i]
			if rule.Variant != "" {
				if err := local(rule.Variant, "rule="+rule.Name); err != nil {
					return err
				}
			}
			for _, w := range rule.Rollout.Weights {
				if err := local(w.Variant, "rule="+rule.Name+" rollout"); err != nil {
					return err
				}
			}
		}
		for _, w := range sw.DefaultRollout.Weights {
			if err := local(w.Variant, "default_rollout"); err != nil {
				return err
			}
		}
	}

	// Pass 2：权重不得为负。
	for _, name := range names {
		if err := checkWeights(name, rules[name], false); err != nil {
			return err
		}
	}
	// Pass 3：放量权重之和必须为 10000。与 Pass 2 分开遍历，
	// 保证「负权重 + 和不为 10000」时报负权重。
	for _, name := range names {
		if err := checkWeights(name, rules[name], true); err != nil {
			return err
		}
	}

	// Pass 4：前置开关必须存在。
	for _, name := range names {
		for _, p := range rules[name].Prerequisites {
			if _, ok := rules[p.Flag]; !ok {
				return &PublishError{Cause: ErrUnknownPrerequisite, Flag: name, Detail: "prerequisite=" + p.Flag}
			}
		}
	}

	// Pass 5：前置依赖不得成环（DFS 三色标记）。
	const (
		white = 0
		gray  = 1
		black = 2
	)
	color := make(map[string]int, len(rules))
	var stack []string
	var dfs func(string) error
	dfs = func(cur string) error {
		switch color[cur] {
		case gray:
			return &PublishError{Cause: ErrPrerequisiteCycle, Flag: cur, Detail: "cycle=" + joinCycle(stack, cur)}
		case black:
			return nil
		}
		color[cur] = gray
		stack = append(stack, cur)
		for _, p := range rules[cur].Prerequisites {
			if err := dfs(p.Flag); err != nil {
				return err
			}
		}
		stack = stack[:len(stack)-1]
		color[cur] = black
		return nil
	}
	for _, name := range names {
		if color[name] == white {
			if err := dfs(name); err != nil {
				return err
			}
		}
	}
	return nil
}

func checkWeights(flagName string, sw *SwitchDef, checkSum bool) error {
	examine := func(ruleName string, r Rollout) error {
		sum := 0
		for _, w := range r.Weights {
			if !checkSum && w.Weight < 0 {
				d := "variant=" + w.Variant
				if ruleName != "" {
					d = "rule=" + ruleName + " " + d
				}
				return &PublishError{Cause: ErrNegativeWeight, Flag: flagName, Detail: d}
			}
			sum += w.Weight
		}
		// 空放量（和为 0）同样不满足「之和为 10000」，
		// 这样求值路径上的每个放量都保证能命中某个变体。
		if checkSum && sum != bucketCount {
			d := "sum=" + strconvItoa(sum)
			if ruleName != "" {
				d = "rule=" + ruleName + " " + d
			}
			return &PublishError{Cause: ErrBadWeightSum, Flag: flagName, Detail: d}
		}
		return nil
	}
	for i := range sw.Targeting {
		rule := &sw.Targeting[i]
		if rule.Variant == "" {
			if err := examine(rule.Name, rule.Rollout); err != nil {
				return err
			}
		}
	}
	return examine("", sw.DefaultRollout)
}

func joinCycle(stack []string, repeat string) string {
	out := ""
	for i, n := range append(append([]string(nil), stack...), repeat) {
		if i > 0 {
			out += " -> "
		}
		out += n
	}
	return out
}

func strconvItoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [24]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

// cloneRules 深拷贝规则集，使已发布快照不受调用方后续修改影响。
func cloneRules(in RuleSet) RuleSet {
	out := make(RuleSet, len(in))
	for name, sw := range in {
		cp := *sw
		cp.Variants = append([]string(nil), sw.Variants...)
		cp.Prerequisites = append([]Prerequisite(nil), sw.Prerequisites...)
		cp.Targeting = make([]Rule, len(sw.Targeting))
		for i, rule := range sw.Targeting {
			r := rule
			r.When = make([]Condition, len(rule.When))
			for j, c := range rule.When {
				r.When[j] = c
				r.When[j].Values = append([]string(nil), c.Values...)
			}
			r.Rollout.Weights = append([]Weight(nil), rule.Rollout.Weights...)
			cp.Targeting[i] = r
		}
		cp.DefaultRollout.Weights = append([]Weight(nil), sw.DefaultRollout.Weights...)
		out[name] = &cp
	}
	return out
}

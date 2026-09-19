package projection

import "strings"

// decision 是对某条路径的匹配裁决。
type decision struct {
	effect Effect
	rule   string // 决定性规则原文；无规则命中时为空
}

// decide 返回路径的裁决：更具体的模式优先，同等具体度下拒绝优先。
func (rs *RuleSet) decide(path []string) decision {
	best := decision{effect: Default}
	bestSpec := -1
	for _, r := range rs.rules {
		if !r.pattern.matches(path) {
			continue
		}
		spec := r.pattern.specificity()
		if spec > bestSpec || (spec == bestSpec && r.effect == Deny && best.effect != Deny) {
			best = decision{effect: r.effect, rule: r.pattern.raw}
			bestSpec = spec
		}
	}
	return best
}

// Explanation 解释某字段为何可见或不可见。
type Explanation struct {
	Path         string
	Visible      bool
	Effect       Effect // 决定性规则的效果；被祖先覆盖时为 Deny
	Rule         string // 决定性规则原文；无规则命中时为空
	Covered      bool   // 是否被祖先规则覆盖
	AncestorPath string // 覆盖它的祖先路径
}

// Explain 查询字段的可见性及其原因。
// 若任一祖先被拒绝，后代一律不可见，即使后代被显式允许，
// 此时 Covered 为 true，Rule 给出覆盖它的祖先规则原文。
func (rs *RuleSet) Explain(path string) Explanation {
	segments := strings.Split(path, ".")
	for i := 1; i < len(segments); i++ {
		d := rs.decide(segments[:i])
		if d.effect == Deny {
			return Explanation{
				Path:         path,
				Visible:      false,
				Effect:       Deny,
				Rule:         d.rule,
				Covered:      true,
				AncestorPath: strings.Join(segments[:i], "."),
			}
		}
	}
	d := rs.decide(segments)
	return Explanation{
		Path:    path,
		Visible: d.effect != Deny,
		Effect:  d.effect,
		Rule:    d.rule,
	}
}

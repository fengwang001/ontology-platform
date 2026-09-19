package ontology

import (
	"fmt"
	"strings"
)

// Reason 解释一条字段可见性判定的由来。
type Reason int

const (
	ReasonAllowRule    Reason = iota // 命中允许规则
	ReasonDenyRule                   // 命中拒绝规则
	ReasonAncestorDeny               // 被祖先路径上的拒绝规则覆盖
	ReasonDefaultAllow               // 未命中任何规则，默认可见（无允许规则时）
	ReasonDefaultDeny                // 未命中任何规则，默认不可见（存在允许规则时）
)

func (r Reason) String() string {
	switch r {
	case ReasonAllowRule:
		return "matched allow rule"
	case ReasonDenyRule:
		return "matched deny rule"
	case ReasonAncestorDeny:
		return "covered by ancestor deny rule"
	case ReasonDefaultAllow:
		return "no rule matched, visible by default"
	case ReasonDefaultDeny:
		return "no rule matched, hidden by default"
	}
	return "unknown"
}

// Explanation 描述某字段路径为何可见或不可见，含命中的规则原文。
type Explanation struct {
	Path    string
	Visible bool
	Rule    string // 决定结果的规则原文；默认判定时为空
	Kind    string // "allow"、"deny"，默认判定时为空
	Reason  Reason
}

// PatternError 报告编译期发现的无效模式，含所在集合、序号与原文。
type PatternError struct {
	Set   string // "allow" 或 "deny"
	Index int    // 在该集合中的下标（从 0 计）
	Raw   string
	Err   error
}

func (e *PatternError) Error() string {
	return fmt.Sprintf("invalid %s pattern #%d %q: %v", e.Set, e.Index+1, e.Raw, e.Err)
}

func (e *PatternError) Unwrap() error { return e.Err }

// ConflictError 报告同一模式同时出现在允许与拒绝集合中的配置冲突。
type ConflictError struct {
	Pattern string
}

func (e *ConflictError) Error() string {
	return fmt.Sprintf("conflicting pattern %q appears in both allow and deny sets", e.Pattern)
}

// RuleSet 是预编译、不可变的规则集，可并发复用。
type RuleSet struct {
	allows []pattern
	denies []pattern
}

// Compile 校验并编译规则集。任一模式无效或允许/拒绝出现完全相同
// 的模式时返回错误；返回的 RuleSet 不可变，可安全并发使用。
func Compile(allow, deny []string) (*RuleSet, error) {
	rs := &RuleSet{}
	seen := make(map[string]bool, len(allow))
	for i, raw := range allow {
		p, err := parsePattern(raw)
		if err != nil {
			return nil, &PatternError{Set: "allow", Index: i, Raw: raw, Err: err}
		}
		rs.allows = append(rs.allows, p)
		seen[p.raw] = true
	}
	for i, raw := range deny {
		p, err := parsePattern(raw)
		if err != nil {
			return nil, &PatternError{Set: "deny", Index: i, Raw: raw, Err: err}
		}
		if seen[p.raw] {
			return nil, &ConflictError{Pattern: p.raw}
		}
		rs.denies = append(rs.denies, p)
	}
	return rs, nil
}

// bestMatch 返回匹配 path 的最具体模式；具体度相同按原文升序取，保证确定。
func bestMatch(pats []pattern, path []string) (pattern, bool) {
	var best pattern
	found := false
	for _, p := range pats {
		if !p.matches(path) {
			continue
		}
		if !found || moreSpecific(p, best) ||
			(sameSpecificity(p, best) && p.raw < best.raw) {
			best, found = p, true
		}
	}
	return best, found
}

// own 只考虑命中 path 自身的规则（不看祖先）：更具体者优先，
// 同等具体度下拒绝优先。matched 为 false 表示没有任何规则命中。
func (rs *RuleSet) own(path []string) (visible bool, rule pattern, matched bool) {
	deny, hasDeny := bestMatch(rs.denies, path)
	allow, hasAllow := bestMatch(rs.allows, path)
	switch {
	case hasAllow && (!hasDeny || moreSpecific(allow, deny)):
		return true, allow, true
	case hasDeny:
		return false, deny, true
	}
	return false, pattern{}, false
}

// Explain 返回字段路径 path（点分形式，如 "addr.geo.lat"）的可见性解释。
// 若任一严格祖先被其自身命中的拒绝规则隐藏，则该字段一律不可见，
// 即使它被显式允许，此时 Reason 为 ReasonAncestorDeny。
func (rs *RuleSet) Explain(path string) Explanation {
	segs := strings.Split(path, ".")
	for i := 1; i < len(segs); i++ {
		if vis, rule, matched := rs.own(segs[:i]); matched && !vis {
			return Explanation{Path: path, Visible: false, Rule: rule.raw,
				Kind: "deny", Reason: ReasonAncestorDeny}
		}
	}
	if vis, rule, matched := rs.own(segs); matched {
		exp := Explanation{Path: path, Visible: vis, Rule: rule.raw, Reason: ReasonAllowRule}
		if vis {
			exp.Kind = "allow"
		} else {
			exp.Kind = "deny"
			exp.Reason = ReasonDenyRule
		}
		return exp
	}
	if len(rs.allows) > 0 {
		return Explanation{Path: path, Visible: false, Reason: ReasonDefaultDeny}
	}
	return Explanation{Path: path, Visible: true, Reason: ReasonDefaultAllow}
}

// Visible 是 Explain(path).Visible 的便捷形式。
func (rs *RuleSet) Visible(path string) bool {
	return rs.Explain(path).Visible
}

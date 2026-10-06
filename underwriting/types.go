package underwriting

import (
	"fmt"
	"sort"
	"strings"
)

// ActionKind 裁定动作类型，一条规则只带一种动作。
type ActionKind int

const (
	ActionStandard  ActionKind = iota // 标准承保
	ActionLoading                     // 加费（百分比，正整数）
	ActionExclusion                   // 附加除外责任（一个除外编码）
	ActionPostpone                    // 延期至某时刻
	ActionReject                      // 拒保
)

// Action 规则的裁定动作。
type Action struct {
	Kind          ActionKind
	Percent       int    // ActionLoading 专用，正整数百分比
	ExclusionCode string // ActionExclusion 专用，除外编码
	PostponeUntil int64  // ActionPostpone 专用，延期至该时刻（秒）
}

// Condition 规则匹配条件；三部分同时成立才算命中，各部分缺省表示不限。
type Condition struct {
	AgeLo       *int            // 年龄下限（含），nil 表示不限
	AgeHi       *int            // 年龄上限（含），nil 表示不限
	Occupations map[int]bool    // 职业类别集合，nil 表示不限
	HealthCodes map[string]bool // 健康告知项集合（任一交集即命中），nil 表示不限
}

// matchesAgeOcc 年龄与职业部分是否命中（与告知无关，供索引预筛）。
func (c Condition) matchesAgeOcc(age, occupation int) bool {
	if c.AgeLo != nil && age < *c.AgeLo {
		return false
	}
	if c.AgeHi != nil && age > *c.AgeHi {
		return false
	}
	if c.Occupations != nil && !c.Occupations[occupation] {
		return false
	}
	return true
}

// matchesHealth 健康告知部分是否命中。
func (c Condition) matchesHealth(disclosures map[string]bool) bool {
	if c.HealthCodes == nil {
		return true
	}
	for code := range disclosures {
		if c.HealthCodes[code] {
			return true
		}
	}
	return false
}

// Rule 核保规则。生效区间为 [Start, End)，左含右不含。
type Rule struct {
	ID    string
	Cond  Condition
	Act   Action
	Start int64
	End   int64
}

func (r Rule) effectiveAt(t int64) bool {
	return r.Start <= t && t < r.End
}

// clone 深拷贝，供快照使用。
func (r Rule) clone() Rule {
	out := r
	if r.Cond.Occupations != nil {
		out.Cond.Occupations = make(map[int]bool, len(r.Cond.Occupations))
		for k, v := range r.Cond.Occupations {
			out.Cond.Occupations[k] = v
		}
	}
	if r.Cond.HealthCodes != nil {
		out.Cond.HealthCodes = make(map[string]bool, len(r.Cond.HealthCodes))
		for k, v := range r.Cond.HealthCodes {
			out.Cond.HealthCodes[k] = v
		}
	}
	if r.Cond.AgeLo != nil {
		v := *r.Cond.AgeLo
		out.Cond.AgeLo = &v
	}
	if r.Cond.AgeHi != nil {
		v := *r.Cond.AgeHi
		out.Cond.AgeHi = &v
	}
	return out
}

// Application 投保单。
type Application struct {
	ID          string   // 唯一编号
	Age         int      // 被保人年龄（非负整数岁）
	Occupation  int      // 职业类别（1 到 6）
	HealthCodes []string // 健康告知项编码集合（可为空）
	Amount      int64    // 申请保额（正整数，分）
	ApplyTime   int64    // 申请时刻（非负整数秒）
}

// DecisionKind 裁定结论类型。
type DecisionKind int

const (
	DecAccepted         DecisionKind = iota // 承保
	DecNeedExam                             // 需体检
	DecPostponed                            // 延期
	DecRuleRejected                         // 规则拒保（终态）
	DecOverloadRejected                     // 加费超限拒保（终态）
)

func (k DecisionKind) String() string {
	switch k {
	case DecAccepted:
		return "承保"
	case DecNeedExam:
		return "需体检"
	case DecPostponed:
		return "延期"
	case DecRuleRejected:
		return "规则拒保"
	case DecOverloadRejected:
		return "加费超限拒保"
	}
	return "未知结论"
}

// Decision 一次裁定的结论。
type Decision struct {
	Kind           DecisionKind
	LoadingPercent int      // DecAccepted 时的合计加费百分比
	Exclusions     []string // DecAccepted 时的除外责任编码并集（排序去重）
	PostponeUntil  int64    // DecPostponed 时的延期时刻（命中规则中最晚者）
}

func (d Decision) terminal() bool {
	return d.Kind == DecRuleRejected || d.Kind == DecOverloadRejected
}

func (d Decision) String() string {
	switch d.Kind {
	case DecAccepted:
		return fmt.Sprintf("承保(加费=%d%%, 除外=[%s])", d.LoadingPercent, strings.Join(d.Exclusions, ","))
	case DecPostponed:
		return fmt.Sprintf("延期(至=%d)", d.PostponeUntil)
	default:
		return d.Kind.String()
	}
}

// equal 供测试与对照模型比较结论一致性。
func (d Decision) equal(o Decision) bool {
	if d.Kind != o.Kind || d.LoadingPercent != o.LoadingPercent || d.PostponeUntil != o.PostponeUntil {
		return false
	}
	if len(d.Exclusions) != len(o.Exclusions) {
		return false
	}
	for i := range d.Exclusions {
		if d.Exclusions[i] != o.Exclusions[i] {
			return false
		}
	}
	return true
}

// Config 保单约定：加费上限与各职业类别免体检限额。
type Config struct {
	LoadingCap    int      // 加费上限（百分比合计，恰等于不算超限）
	ExamFreeLimit [7]int64 // 各职业类别免体检限额（分），下标 1..6，恰等于不需体检
}

// mergeDecision 按固定次序合并所有命中规则的裁定动作。
// rules 必须已按年龄、职业与生效区间预筛；本函数再按告知集合过滤。
// examLimit 为该投保单当前适用的免体检限额（体检通过后为“视同无限制”）。
func mergeDecision(rules []Rule, disclosures map[string]bool, app Application, cfg Config, examLimit int64) Decision {
	rejected := false
	loading := 0
	hasPostpone := false
	var postponeUntil int64
	exclusions := map[string]bool{}
	for _, r := range rules {
		if !r.Cond.matchesHealth(disclosures) {
			continue
		}
		switch r.Act.Kind {
		case ActionReject:
			rejected = true
		case ActionLoading:
			loading += r.Act.Percent
		case ActionExclusion:
			exclusions[r.Act.ExclusionCode] = true
		case ActionPostpone:
			if !hasPostpone || r.Act.PostponeUntil > postponeUntil {
				postponeUntil = r.Act.PostponeUntil
			}
			hasPostpone = true
		case ActionStandard:
		}
	}
	if rejected {
		return Decision{Kind: DecRuleRejected}
	}
	if loading > cfg.LoadingCap {
		return Decision{Kind: DecOverloadRejected}
	}
	if hasPostpone {
		return Decision{Kind: DecPostponed, PostponeUntil: postponeUntil}
	}
	if app.Amount > examLimit {
		return Decision{Kind: DecNeedExam}
	}
	codes := make([]string, 0, len(exclusions))
	for c := range exclusions {
		codes = append(codes, c)
	}
	sort.Strings(codes)
	return Decision{Kind: DecAccepted, LoadingPercent: loading, Exclusions: codes}
}

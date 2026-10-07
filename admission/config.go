package admission

import (
	"sort"
)

// MatchConditions 为规则匹配条件；每个集合为空表示该项不限，
// 非空表示请求对应取值必须属于该集合，三项之间为逻辑与。
type MatchConditions struct {
	UserGroups []string
	Verbs      []string
	Resources  []string
}

// FlowDistinguish 定义流的区分方式。
type FlowDistinguish int

const (
	FlowByUser      FlowDistinguish = iota // 按用户区分流
	FlowByNamespace                        // 按命名空间区分流
)

// Rule 是一条流分类规则。
type Rule struct {
	Name        string
	Priority    int64
	Match       MatchConditions
	TargetLevel string
	Distinguish FlowDistinguish
}

// LevelKind 区分豁免与受限级别。
type LevelKind int

const (
	LevelExempt LevelKind = iota
	LevelLimited
)

// LevelConfig 是级别配置。豁免级别忽略 Share/QueueLimit/Timeout。
type LevelConfig struct {
	Name       string
	Kind       LevelKind
	Share      int64 // 份额（正整数，仅受限级别有效）
	QueueLimit int64 // 队列长度上限（非负，仅受限级别有效）
	Timeout    int64 // 排队超时（正整数，仅受限级别有效）
}

// Config 是准入控制器的整份配置。
type Config struct {
	Rules      []Rule
	Levels     []LevelConfig
	TotalSeats int64
}

type compiledRule struct {
	rule        Rule
	groups      map[string]struct{}
	verbs       map[string]struct{}
	resources   map[string]struct{}
	limited     bool
	distinguish FlowDistinguish
}

type compiledConfig struct {
	rules   []compiledRule
	exempt  map[string]bool
	limited map[string]*LevelConfig
	nominal map[string]int64
}

func compileConfig(cfg *Config) (*compiledConfig, error) {
	if cfg == nil {
		return nil, newError(ClassInvalidArgument, "config is nil")
	}
	if cfg.TotalSeats < 0 {
		return nil, newError(ClassInvalidArgument, "total seats must be non-negative")
	}

	out := &compiledConfig{
		exempt:  map[string]bool{},
		limited: map[string]*LevelConfig{},
		nominal: map[string]int64{},
	}
	levelKinds := map[string]LevelKind{}
	shares := map[string]int64{}
	limitedNames := make([]string, 0, len(cfg.Levels))

	seenLevel := map[string]struct{}{}
	for i := range cfg.Levels {
		lv := &cfg.Levels[i]
		if lv.Name == "" {
			return nil, newError(ClassInvalidArgument, "level name must be non-empty")
		}
		if _, dup := seenLevel[lv.Name]; dup {
			return nil, newError(ClassInvalidArgument, "duplicate level name: "+lv.Name)
		}
		seenLevel[lv.Name] = struct{}{}
		switch lv.Kind {
		case LevelExempt:
			out.exempt[lv.Name] = true
			levelKinds[lv.Name] = LevelExempt
		case LevelLimited:
			if lv.Share <= 0 {
				return nil, newError(ClassInvalidArgument, "limited level "+lv.Name+" share must be positive")
			}
			if lv.QueueLimit < 0 {
				return nil, newError(ClassInvalidArgument, "limited level "+lv.Name+" queue limit must be non-negative")
			}
			if lv.Timeout <= 0 {
				return nil, newError(ClassInvalidArgument, "limited level "+lv.Name+" timeout must be positive")
			}
			cp := *lv
			out.limited[lv.Name] = &cp
			shares[lv.Name] = lv.Share
			levelKinds[lv.Name] = LevelLimited
			limitedNames = append(limitedNames, lv.Name)
		default:
			return nil, newError(ClassInvalidArgument, "unknown level kind for "+lv.Name)
		}
	}

	seenRule := map[string]struct{}{}
	for _, r := range cfg.Rules {
		if r.Name == "" {
			return nil, newError(ClassInvalidArgument, "rule name must be non-empty")
		}
		if _, dup := seenRule[r.Name]; dup {
			return nil, newError(ClassInvalidArgument, "duplicate rule name: "+r.Name)
		}
		seenRule[r.Name] = struct{}{}
		if r.TargetLevel == "" {
			return nil, newError(ClassInvalidArgument, "rule "+r.Name+" target level must be non-empty")
		}
		kind, ok := levelKinds[r.TargetLevel]
		if !ok {
			return nil, newError(ClassInvalidArgument, "rule "+r.Name+" targets unknown level "+r.TargetLevel)
		}
		if r.Distinguish != FlowByUser && r.Distinguish != FlowByNamespace {
			return nil, newError(ClassInvalidArgument, "rule "+r.Name+" has unknown flow distinguish kind")
		}
		out.rules = append(out.rules, compiledRule{
			rule:        r,
			groups:      toSet(r.Match.UserGroups),
			verbs:       toSet(r.Match.Verbs),
			resources:   toSet(r.Match.Resources),
			limited:     kind == LevelLimited,
			distinguish: r.Distinguish,
		})
	}

	// 规则按优先级数值升序，数值相同按名称升序；请求依次匹配第一条命中规则。
	sort.SliceStable(out.rules, func(i, j int) bool {
		if out.rules[i].rule.Priority != out.rules[j].rule.Priority {
			return out.rules[i].rule.Priority < out.rules[j].rule.Priority
		}
		return out.rules[i].rule.Name < out.rules[j].rule.Name
	})

	limitedLevels := make([]LevelConfig, 0, len(limitedNames))
	for _, name := range limitedNames {
		limitedLevels = append(limitedLevels, *out.limited[name])
	}
	out.nominal = allocateSeats(cfg.TotalSeats, limitedLevels, shares)
	return out, nil
}

// allocateSeats 按份额比例向下取整分配总席位，
// 余量按份额降序、份额相同按名称升序逐一补一席。
func allocateSeats(total int64, levels []LevelConfig, shares map[string]int64) map[string]int64 {
	nominal := make(map[string]int64, len(levels))
	if len(levels) == 0 {
		return nominal
	}
	var sumShares int64
	for _, lv := range levels {
		sumShares += shares[lv.Name]
	}
	var distributed int64
	// 名义席位 = floor(total * share / sumShares)。
	for _, lv := range levels {
		n := total * shares[lv.Name] / sumShares
		nominal[lv.Name] = n
		distributed += n
	}
	remaining := total - distributed
	order := make([]LevelConfig, len(levels))
	copy(order, levels)
	sort.SliceStable(order, func(i, j int) bool {
		if shares[order[i].Name] != shares[order[j].Name] {
			return shares[order[i].Name] > shares[order[j].Name]
		}
		return order[i].Name < order[j].Name
	})
	// 标准取整余量严格小于级别数，一轮即可分完；用循环保持对任意输入成立。
	for remaining > 0 {
		progressed := false
		for _, lv := range order {
			if remaining == 0 {
				break
			}
			nominal[lv.Name]++
			remaining--
			progressed = true
		}
		if !progressed {
			break
		}
	}
	return nominal
}

func toSet(items []string) map[string]struct{} {
	if len(items) == 0 {
		return nil
	}
	m := make(map[string]struct{}, len(items))
	for _, item := range items {
		m[item] = struct{}{}
	}
	return m
}

func (cr *compiledRule) matches(req *Request) bool {
	if cr.groups != nil {
		if _, ok := cr.groups[req.UserGroup]; !ok {
			return false
		}
	}
	if cr.verbs != nil {
		if _, ok := cr.verbs[req.Verb]; !ok {
			return false
		}
	}
	if cr.resources != nil {
		if _, ok := cr.resources[req.Resource]; !ok {
			return false
		}
	}
	return true
}

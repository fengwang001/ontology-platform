package projection

// Effect 是规则的效果。
type Effect int

const (
	// Default 表示没有任何规则命中，字段默认可见。
	Default Effect = iota
	// Allow 表示命中允许规则。
	Allow
	// Deny 表示命中拒绝规则。
	Deny
)

func (e Effect) String() string {
	switch e {
	case Allow:
		return "allow"
	case Deny:
		return "deny"
	default:
		return "default"
	}
}

// Dependency 声明 Field 由 Sources 计算而来。
type Dependency struct {
	Field   string
	Sources []string
}

// SourceHiddenPolicy 决定来源不可见而结果可见时的处理方式。
type SourceHiddenPolicy int

const (
	// HideResult 一并隐藏计算结果。
	HideResult SourceHiddenPolicy = iota
	// FailOnHiddenSource 返回 DependencyError。
	FailOnHiddenSource
)

// Config 是规则集的编译输入。
type Config struct {
	Allow          []string
	Deny           []string
	Required       []string
	Dependencies   []Dependency
	OnSourceHidden SourceHiddenPolicy
}

// rule 是一条编译后的规则。
type rule struct {
	pattern pattern
	effect  Effect
}

// RuleSet 是预编译、不可变的规则集，可并发地应用到多个对象上。
type RuleSet struct {
	rules        []rule
	required     []string
	dependencies []Dependency
	onHidden     SourceHiddenPolicy
}

// Compile 校验并编译规则集。同一模式同时出现在允许与拒绝中时报冲突。
func Compile(cfg Config) (*RuleSet, error) {
	var rules []rule
	seen := make(map[string]Effect)
	add := func(set string, effect Effect, patterns []string) error {
		for i, raw := range patterns {
			p, err := parsePattern(raw)
			if err != nil {
				return &PatternError{Set: set, Index: i, Pattern: raw, Reason: err.Error()}
			}
			if prev, ok := seen[raw]; ok {
				if prev != effect {
					return &ConflictError{Pattern: raw}
				}
				continue
			}
			seen[raw] = effect
			rules = append(rules, rule{pattern: p, effect: effect})
		}
		return nil
	}
	if err := add("allow", Allow, cfg.Allow); err != nil {
		return nil, err
	}
	if err := add("deny", Deny, cfg.Deny); err != nil {
		return nil, err
	}
	return &RuleSet{
		rules:        rules,
		required:     append([]string(nil), cfg.Required...),
		dependencies: append([]Dependency(nil), cfg.Dependencies...),
		onHidden:     cfg.OnSourceHidden,
	}, nil
}

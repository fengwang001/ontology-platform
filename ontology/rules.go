package ontology

// TagRule 声明一个敏感标签的判定规则：对 objectType 的实例，
// 以 Expr 对当前属性取值（及必要的其他标签状态）求值，
// 结果为 true 表示该实例当前携带该标签。
type TagRule struct {
	Tag        string
	ObjectType string
	Expr       Expr
}

// ruleSet 按对象类型组织标签规则。标签携带状态永远按需重算，不落盘。
type ruleSet struct {
	byType map[string]map[string]TagRule // objectType -> tag -> rule
}

func newRuleSet() *ruleSet {
	return &ruleSet{byType: make(map[string]map[string]TagRule)}
}

func (rs *ruleSet) rulesOf(typeName string) map[string]TagRule {
	return rs.byType[typeName]
}

// validate 校验一组规则（与现有规则合并后的全量视图），
// 按固定优先级汇报错误：未知属性 > 循环依赖。
// 校验不通过时不产生任何副作用。
func (rs *ruleSet) validate(st *store, typeName string, rules map[string]TagRule) error {
	ot := st.types[typeName]
	if ot == nil {
		return newError(ErrKindUnknownAttribute, "对象类型 %q 未登记", typeName)
	}
	// 第一优先级：引用了不存在的属性。
	for _, rule := range rules {
		attrs, _ := collectRuleRefs(rule.Expr)
		for name := range attrs {
			if !ot.HasAttr(name) {
				return newError(ErrKindUnknownAttribute,
					"标签 %q 的判定规则引用了不存在的属性 %q", rule.Tag, name)
			}
		}
	}
	// 第二优先级：TagRef 构成的循环依赖。
	if tag := findCycle(rules); tag != "" {
		return newError(ErrKindCyclicDependency,
			"标签 %q 的判定规则构成循环依赖", tag)
	}
	return nil
}

func collectRuleRefs(e Expr) (attrs, tags map[string]struct{}) {
	attrs = make(map[string]struct{})
	tags = make(map[string]struct{})
	collectRefs(e, attrs, tags)
	return attrs, tags
}

// findCycle 在 TagRef 依赖图上做确定性的环检测，返回环上字典序最小的标签名。
func findCycle(rules map[string]TagRule) string {
	const (
		white = 0 // 未访问
		gray  = 1 // 在栈上
		black = 2 // 已完成
	)
	color := make(map[string]int, len(rules))
	var cycleAt string
	var visit func(tag string) bool
	visit = func(tag string) bool {
		color[tag] = gray
		_, deps := collectRuleRefs(rules[tag].Expr)
		for dep := range deps {
			if _, ok := rules[dep]; !ok {
				continue // 引用未登记的标签：视为常量 false，不构成依赖边
			}
			switch color[dep] {
			case gray:
				if cycleAt == "" || dep < cycleAt {
					cycleAt = dep
				}
				return true
			case white:
				if visit(dep) {
					return true
				}
			}
		}
		color[tag] = black
		return false
	}
	// 按字典序遍历起点，保证结果与 map 迭代顺序无关。
	for _, tag := range sortedKeys(rules) {
		if color[tag] == white {
			if visit(tag) {
				return cycleAt
			}
		}
	}
	return ""
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	for i := 1; i < len(keys); i++ {
		for j := i; j > 0 && keys[j] < keys[j-1]; j-- {
			keys[j], keys[j-1] = keys[j-1], keys[j]
		}
	}
	return keys
}

// upsert 在全量校验通过后原子地替换某对象类型的规则集合。
func (rs *ruleSet) upsert(typeName string, rules map[string]TagRule) {
	cp := make(map[string]TagRule, len(rules))
	for k, v := range rules {
		cp[k] = v
	}
	rs.byType[typeName] = cp
}

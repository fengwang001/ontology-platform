package ontology

// MergeRule 是允许自动合并的属性所声明的合并规则。
// 所有规则的合结果都只依赖写入内容本身（声明新值、声明的逻辑时钟与
// 写入标识）以及写入声明的基线版本，不依赖写入到达的物理时刻；
// 对同一集合的并发写入，任意到达顺序下最终合并结果完全相同。
type MergeRule int

const (
	// RuleNone 表示该属性不允许自动合并（默认值）。
	RuleNone MergeRule = iota
	// RuleMax 取并集语义的最大值（int），交换/结合/幂等。
	RuleMax
	// RuleMin 取并集语义的最小值（int），交换/结合/幂等。
	RuleMin
	// RuleSetUnion 字符串集合并集，交换/结合/幂等。
	RuleSetUnion
	// RuleAdd 计数器：合并增量 = 声明新值 - 声明基线版本处的值。
	// 增量只依赖写入内容与不可变的基线历史值，求和与到达顺序无关。
	RuleAdd
	// RuleLWW 按写入内容中声明的逻辑时钟做 last-writer-wins，
	// 时钟相同则以 WriteID 字典序决胜，构成与到达顺序无关的全序。
	RuleLWW
)

func (r MergeRule) String() string {
	switch r {
	case RuleNone:
		return "none"
	case RuleMax:
		return "max"
	case RuleMin:
		return "min"
	case RuleSetUnion:
		return "set-union"
	case RuleAdd:
		return "add"
	case RuleLWW:
		return "lww-logical"
	}
	return "unknown"
}

// compatibleWith 报告合并规则是否适用于给定值类型。
func (r MergeRule) compatibleWith(k ValueKind) bool {
	switch r {
	case RuleMax, RuleMin, RuleAdd:
		return k == KindInt
	case RuleSetUnion:
		return k == KindStringSet
	case RuleLWW:
		return true
	}
	return false
}

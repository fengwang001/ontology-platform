package dlabel

// Effect 是授权结论：允许或拒绝。
type Effect int

const (
	EffectDeny Effect = iota
	EffectAllow
)

// Grant 按标签为主体声明可读/可写属性集合与可见范围，与具体实例无关。
// Attrs 中的 "*" 表示该对象类型的全部属性。
type Grant struct {
	Subject    string
	Tag        string
	Read       Effect // 对 Attrs 的可读结论
	Write      Effect
	Visibility Effect // 实例可见性结论
	Attrs      []string
}

// Decision 是单个属性合并后的最终权限结论。
type Decision struct {
	Allowed bool
}

// mergeEffects 合并若干标签对同一主体在同一属性上的结论。
//
// 合并规则（确定性、与规则求值顺序无关）：
//   - 没有任何携带标签给出结论（sawAny == false）时：默认拒绝
//     （fail-closed，权限只可能来自显式授权）；
//   - 至少一条结论：任一拒绝即拒绝（deny-overrides），
//     否则全部允许才允许。
//
// 该函数只接收聚合后的集合 {deny, allow}（布尔位），
// 因而结果不可能依赖标签的求值顺序。
func mergeEffects(sawAny, sawDeny, sawAllow bool) Decision {
	if !sawAny {
		return Decision{Allowed: false}
	}
	return Decision{Allowed: !sawDeny && sawAllow}
}

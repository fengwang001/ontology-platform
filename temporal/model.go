package temporal

// Tick 是逻辑时钟上的一个时刻。提交时刻（Committed）与生效时刻
// （Effective）都取自同一根全序的 Tick 轴；调用方负责提供 Tick 值，
// 引擎本身不推进时钟，因此所有判定都是纯函数式的、可对任意时刻复算。
type Tick int64

// Subject 标识权限主体（如用户、角色）。
type Subject string

// Label 标识被访问的本体对象标签（如对象类型、动作）。
type Label string

// Kind 是一条权限规则变更的种类。
type Kind int

const (
	// Grant 将 (主体, 标签) 的访问判定置为允许。
	Grant Kind = iota + 1
	// Deny 将 (主体, 标签) 的访问判定置为拒绝。
	Deny
	// Revoke 撤销目标变更在其生效时刻之后确立的规则效果。
	Revoke
)

func (k Kind) String() string {
	switch k {
	case Grant:
		return "grant"
	case Deny:
		return "deny"
	case Revoke:
		return "revoke"
	default:
		return "unknown"
	}
}

// Change 是一条权限规则变更的不可变记录。
//
// ID 是变更固有的唯一标识：同一 (生效时刻, 提交时刻) 上的叠加顺序
// 最终以 ID 的字典序确定，因此与提交内容无关、对相同输入必然可重复。
// DependsOn 非空时，表示本变更的效果以前置变更仍然成立为前提；
// 前置变更被撤销后，本变更在新基线下失效（除非被新的变更重新确立）。
// Target 仅在 Kind == Revoke 时有意义，指向被撤销的变更 ID。
type Change struct {
	ID        string
	Subject   Subject
	Label     Label
	Kind      Kind
	Committed Tick
	Effective Tick
	DependsOn string
	Target    string
}

// OrderKey 是叠加顺序键：先按生效时刻，再按提交时刻，最后按固有 ID。
type OrderKey struct {
	Effective Tick
	Committed Tick
	ID        string
}

func orderKeyOf(c Change) OrderKey {
	return OrderKey{Effective: c.Effective, Committed: c.Committed, ID: c.ID}
}

func (k OrderKey) less(o OrderKey) bool {
	if k.Effective != o.Effective {
		return k.Effective < o.Effective
	}
	if k.Committed != o.Committed {
		return k.Committed < o.Committed
	}
	return k.ID < o.ID
}

// Reassessment 是一条后续变更在基线被追溯撤销后的重新考察结论。
type Reassessment struct {
	ChangeID      string `json:"change_id"`
	StillValid    bool   `json:"still_valid"`
	EffectChanged bool   `json:"effect_changed"`
	Reason        string `json:"reason"`
}

// SubmitResult 是一次提交的结果，含被新基线牵动的全部后续变更的确定结论。
type SubmitResult struct {
	Change       Change         `json:"change"`
	Reassessment []Reassessment `json:"reassessment"`
}

// Decision 是某主体对某标签在某时刻的访问判定，以及据以裁决的依据。
type Decision struct {
	At       Tick     `json:"at"`
	Subject  Subject  `json:"subject"`
	Label    Label    `json:"label"`
	Allowed  bool     `json:"allowed"`
	Default  bool     `json:"default"`
	Basis    []string `json:"basis"`
	Examined int      `json:"examined"`
}

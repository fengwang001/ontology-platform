// Package authz 提供权限变更的生效时序管理：
// 每条变更携带提交时刻与生效时刻两条独立轴线，系统支持
// 任意时刻的访问判定查询、排队变更撤回以及已生效变更的
// 追溯撤销与后续变更重估。
package authz

// Time 为逻辑时刻，由调用方提供（单调语义由 Timeline 维护）。
type Time int64

// ChangeID 是变更的固有唯一标识，由系统在受理提交时按
// 串行顺序分配，与提交内容无关，用于同提交时刻变更之间的
// 确定性排序。
type ChangeID uint64

// Op 为变更操作类型。
type Op int

const (
	// OpSet 将 (Subject, Label) 的权限规则设置为 Value。
	OpSet Op = iota
	// OpRevoke 撤销 Target 指定的已生效变更。
	OpRevoke
)

// ChangeInput 为提交变更时的输入。
type ChangeInput struct {
	Subject string
	Label   string
	Op      Op
	// Value 为 OpSet 的判定值（如 "allow"/"deny"）。
	Value string
	// Target 为 OpRevoke 要撤销的变更 ID。
	Target ChangeID
	// DependsOn 声明本变更的内容所依赖的前序变更；
	// 任一依赖被撤销时，本变更在折叠判定中视为无效。
	DependsOn   []ChangeID
	SubmittedAt Time
	EffectiveAt Time
}

// Change 为系统内记录的变更（含分配后的 ID 与状态）。
type Change struct {
	ChangeInput
	ID ChangeID
}

// lessOrder 定义确定性叠加顺序的比较键：
// 先生效时刻，再提交时刻，最后固有唯一 ID。
func lessOrder(a, b Change) bool {
	if a.EffectiveAt != b.EffectiveAt {
		return a.EffectiveAt < b.EffectiveAt
	}
	if a.SubmittedAt != b.SubmittedAt {
		return a.SubmittedAt < b.SubmittedAt
	}
	return a.ID < b.ID
}

// Decision 为一次访问判定的结果。
type Decision struct {
	// Found 表示该时刻是否存在已生效的规则。
	Found bool
	// Value 为最后一条适用 OpSet 的值。
	Value string
	// Basis 为据以裁决的生效时序依据：参与折叠且实际
	// 生效的变更 ID（按叠加顺序）。
	Basis []ChangeID
	// Examined 为本次查询考察的变更记录数量，是可观测的
	// 开销证明：仅与截至查询时刻对该主体与标签生效过的
	// 变更数量相关。
	Examined int
}

// ReevalItem 描述一条后续变更在新基线下的重估结论。
type ReevalItem struct {
	ChangeID     ChangeID
	StillValid   bool
	EffectChange bool // 重估前后该变更对判定结果的影响是否改变
}

// Reevaluation 为一条 OpRevoke 生效后对后续依赖变更的
// 重估报告。
type Reevaluation struct {
	RevokedBy ChangeID
	Revoked   ChangeID
	Items     []ReevalItem
}

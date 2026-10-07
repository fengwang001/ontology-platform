// Package ontology 实现本体平台中链接类型的基数管理：
// 运行期基数上限下调时的确定性超额标记、待处理（pending）状态、
// 级联决议（保留/删除）、派生状态不可信标注以及孤儿清理。
package ontology

import "errors"

// Direction 表示链接类型的一个方向。
// DirectionOut 以源对象为主体（每个源对象最多登记 SourceLimit 条链接），
// DirectionIn 以目标对象为主体（每个目标对象最多登记 TargetLimit 条链接）。
type Direction int

const (
	DirectionOut Direction = iota
	DirectionIn
)

func (d Direction) String() string {
	if d == DirectionOut {
		return "out"
	}
	return "in"
}

// Unlimited 表示该方向不设基数上限。
const Unlimited = -1

// LinkStatus 是链接的生命周期状态。
type LinkStatus int

const (
	// StatusActive 有效链接，参与基数判断。
	StatusActive LinkStatus = iota
	// StatusPending 待处理（超额待决议）：对查询可见、计入历史统计，
	// 但不作为有效链接参与任何基数判断。
	StatusPending
	// StatusDeleted 已最终删除（物理移除出登记集合，仅留审计痕迹）。
	StatusDeleted
)

func (s LinkStatus) String() string {
	switch s {
	case StatusActive:
		return "active"
	case StatusPending:
		return "pending"
	case StatusDeleted:
		return "deleted"
	}
	return "unknown"
}

// Resolution 是待处理链接的显式后续处理动作。
type Resolution int

const (
	// ResolveKeep 转为保留：链接恢复有效，并相应提升该方向该对象的有效上限。
	ResolveKeep Resolution = iota
	// ResolveDelete 转为删除：链接被最终删除，关联派生状态被清理。
	ResolveDelete
)

// 错误定义。
var (
	ErrLinkTypeNotFound = errors.New("ontology: link type not found")
	ErrLinkTypeExists   = errors.New("ontology: link type already defined")
	ErrLinkNotFound     = errors.New("ontology: link not found")
	ErrLinkExists       = errors.New("ontology: link already exists")
	ErrObjectNotFound   = errors.New("ontology: object not registered")
	// ErrLimitFull 新建请求因当前有效上限已满被拒绝。
	ErrLimitFull = errors.New("ontology: cardinality limit full")
	// ErrNotPending 链接不处于待处理状态。
	ErrNotPending = errors.New("ontology: link is not pending")
	// ErrObjectRevoked 待处理链接所依赖的对象已被撤销，只能转为删除。
	ErrObjectRevoked = errors.New("ontology: endpoint object revoked, keep not allowed")
	// ErrInvalidLimit 非法的基数上限。
	ErrInvalidLimit = errors.New("ontology: invalid cardinality limit")
	// ErrDerivedNotFound 派生状态不存在（或已被清理）。
	ErrDerivedNotFound = errors.New("ontology: derived state not found")
)

// Link 是一条已登记的链接。
type Link struct {
	ID     string
	TypeID string
	Source string
	Target string
	// Seq 是单调递增的登记序号，与 ID 一起构成确定性的排序键 (Seq, ID)。
	Seq uint64
	// MarkBasis 记录最近一次被标记为超额时使用的确定性依据。
	MarkBasis string
}

// LinkView 是链接对外的查询视图。
type LinkView struct {
	Link
	Status LinkStatus
}

// GroupStats 是某个 (链接类型, 方向, 对象) 登记组的统计信息。
// Pending 链接计入 TotalRegistered（历史统计），但不计入 Active。
type GroupStats struct {
	Active          int
	Pending         int
	TotalRegistered int // 历史累计登记数（含已删除），只增不减
	EffectiveLimit  int // 基础上限 + 保留动作提升的额度
	Adjustments     int // 历史上发生过的基数调整次数（用于复杂度验证）
}

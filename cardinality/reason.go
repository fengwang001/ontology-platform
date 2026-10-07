package cardinality

// Reason 以独立原因码区分四类（及其他）需要分别暴露的情形。
type Reason string

const (
	// ReasonNone 无特殊原因。
	ReasonNone Reason = ""
	// ReasonMarkedExcess 基数下调触发的标记为超额。
	ReasonMarkedExcess Reason = "marked_excess_by_limit_downgrade"
	// ReasonCreateRejected 新建请求因当前有效上限已满被拒绝。
	ReasonCreateRejected Reason = "create_rejected_limit_full"
	// ReasonObjectRevoked 待处理链接依赖的对象已被撤销，
	// 只能转为删除；该判定优先于一般保留/删除默认路径。
	ReasonObjectRevoked Reason = "pending_target_revoked_force_delete"
	// ReasonPendingRestored 待处理期间上限又被上调导致的整体恢复。
	ReasonPendingRestored Reason = "pending_restored_by_limit_upgrade"
	// ReasonFinalDelete 显式处置：最终删除。
	ReasonFinalDelete Reason = "final_delete"
	// ReasonFinalRetain 显式处置：保留并提升有效上限。
	ReasonFinalRetain Reason = "final_retain_limit_effectively_raised"
	// ReasonDerivedSuspended 派生状态被标记为暂时不可信。
	ReasonDerivedSuspended Reason = "derived_state_suspect"
	// ReasonDerivedRestored 派生状态恢复可信。
	ReasonDerivedRestored Reason = "derived_state_restored"
	// ReasonDerivedCleared 派生状态随链接最终删除而清理。
	ReasonDerivedCleared Reason = "derived_state_cleared"
)

// EventKind 标识审计记录对应的处理种类。
type EventKind string

const (
	EventLimitChanged  EventKind = "limit_changed"
	EventLinkCreated   EventKind = "link_created"
	EventCreateDenied  EventKind = "create_denied"
	EventMarked        EventKind = "marked_excess"
	EventRestored      EventKind = "pending_restored"
	EventFinalized     EventKind = "pending_finalized"
	EventDerivedChange EventKind = "derived_state_change"
	EventObjectRevoked EventKind = "object_revoked"
)

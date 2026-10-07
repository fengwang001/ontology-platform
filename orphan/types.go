// Package orphan 提供本体平台的事件溯源重建子系统：
// 以事件流重建对象-链接网络在任意历史时刻的状态，并对对象做
// 追溯孤儿判定（依据声明的级联清理规则版本，而非事件流表面记录）。
package orphan

// Time 是逻辑历史时刻，单调语义由调用方保证。
type Time int64

type EventID string
type ObjectID string
type ObjectTypeID string
type LinkID string
type LinkTypeID string

// EventKind 枚举事件流支持的事件类型。
type EventKind int

const (
	EvLinkTypeCreated EventKind = iota
	EvObjectCreated
	EvPropertySet
	EvLinkCreated
	EvLinkRevoked
	EvObjectMarkedOrphan // 级联清理流程留下的显式孤儿标记
)

func (k EventKind) String() string {
	switch k {
	case EvLinkTypeCreated:
		return "LinkTypeCreated"
	case EvObjectCreated:
		return "ObjectCreated"
	case EvPropertySet:
		return "PropertySet"
	case EvLinkCreated:
		return "LinkCreated"
	case EvLinkRevoked:
		return "LinkRevoked"
	case EvObjectMarkedOrphan:
		return "ObjectMarkedOrphan"
	}
	return "Unknown"
}

// Event 是事件流中的一条记录。Seq 由存储在追加时分配，
// 同一 Time 内的并列记录必须依靠 After 链确定先后，否则触发 ErrAmbiguousOrder。
type Event struct {
	ID    EventID
	Kind  EventKind
	Time  Time
	After EventID // 可选因果前驱，用于同刻事件的排序
	Seq   uint64  // 存储分配的追加序号

	Object     ObjectID     // EvObjectCreated / EvPropertySet / EvObjectMarkedOrphan
	ObjectType ObjectTypeID // EvObjectCreated
	Key        string       // EvPropertySet
	Value      string       // EvPropertySet
	Link       LinkID       // EvLinkCreated / EvLinkRevoked
	LinkType   LinkTypeID   // EvLinkTypeCreated / EvLinkCreated
	From       ObjectID     // EvLinkCreated
	To         ObjectID     // EvLinkCreated
}

// RuleSpec 是某一级联清理规则版本的内容。
type RuleSpec struct {
	// RequiredLinkTypes 按对象类型声明其必需链接类型。
	RequiredLinkTypes map[ObjectTypeID][]LinkTypeID
	// GracePeriod 表示全部必需链接被撤销后，对象应在此宽限内被标记为孤儿。
	GracePeriod Time
}

// Requires 判断某对象类型是否将某链接类型视为必需。
func (s RuleSpec) Requires(objType ObjectTypeID, lt LinkTypeID) bool {
	for _, t := range s.RequiredLinkTypes[objType] {
		if t == lt {
			return true
		}
	}
	return false
}

// RuleVersion 是规则账本中的一个版本。
type RuleVersion struct {
	Version       int
	Spec          RuleSpec
	Retroactive   bool // true 表示追溯适用于 EffectiveFrom 之前的全部历史
	EffectiveFrom Time
	CreateSeq     uint64 // 账本写入时的全局操作序号（线性化点）
}

// GoverningVersion 返回治理历史时刻 t 的规则版本：
// 满足 (EffectiveFrom <= t 或 Retroactive) 的最新版本。
// 该规定对所有历史时刻保持同一方向，与请求发起时间无关。
func GoverningVersion(ledger []RuleVersion, t Time) (RuleVersion, bool) {
	best := -1
	for i, v := range ledger {
		if v.EffectiveFrom <= t || v.Retroactive {
			best = i
		}
	}
	if best < 0 {
		return RuleVersion{}, false
	}
	return ledger[best], true
}

// Status 是孤儿判定的结论类别。
type Status int

const (
	StatusNotOrphan Status = iota
	// StatusOrphanConfirmed 表示规则要求成为孤儿，且事件流中存在明确的级联标记事件。
	StatusOrphanConfirmed
	// StatusRetroOrphanDormant 表示追溯判定本应成为孤儿，事件流无级联标记，此后也无正常活动。
	StatusRetroOrphanDormant
	// StatusRetroOrphanWithActivity 表示追溯判定本应成为孤儿，事件流无级联标记，
	// 但在本应成孤之后仍发生属性赋值或新链接建立等正常事件。
	StatusRetroOrphanWithActivity
)

func (s Status) String() string {
	switch s {
	case StatusNotOrphan:
		return "NotOrphan"
	case StatusOrphanConfirmed:
		return "OrphanConfirmed"
	case StatusRetroOrphanDormant:
		return "RetroOrphanDormant"
	case StatusRetroOrphanWithActivity:
		return "RetroOrphanWithActivity"
	}
	return "Unknown"
}

// Activity 描述本应成孤之后仍发生的一条正常活动。
type Activity struct {
	Time Time
	Kind EventKind
	Seq  uint64
}

// Query 是一次孤儿追溯判定的输入。
type Query struct {
	Object  ObjectID
	At      Time
	Version int // 声明依据的规则版本；不等于治理版本时触发 ErrRuleVersionVoided
}

// Determination 是一次判定的完整结论。
type Determination struct {
	Query         Query
	Version       int // 实际用于判定的治理版本
	Status        Status
	ZeroSince     Time // 全部必需链接归零的起始时刻（Status 非 NotOrphan 时有效）
	OrphanDue     Time // 本应被标记为孤儿的时刻 = ZeroSince + GracePeriod
	ConfirmedAt   Time // 事件流中级联标记的时刻（StatusOrphanConfirmed 时有效）
	Activities    []Activity
	EventsScanned int // 判定实际检视的事件条数，用于复杂度独立验证
}

// ErrorCode 区分四类互不相同的重建错误。
type ErrorCode int

const (
	ErrNone ErrorCode = iota
	// ErrRuleVersionVoided 判定依据的规则版本已被新的版本调整作废。
	ErrRuleVersionVoided
	// ErrAmbiguousOrder 事件流存在无法确定先后顺序的并列记录。
	ErrAmbiguousOrder
	// ErrDanglingReference 事件流引用了尚未创建的链接类型或对象。
	ErrDanglingReference
	// ErrTimeBeforeFirstAppearance 请求的历史时刻早于对象首次出现的时刻。
	ErrTimeBeforeFirstAppearance
)

// 错误报告优先级（数值越小越优先）：
// ErrRuleVersionVoided > ErrAmbiguousOrder > ErrDanglingReference > ErrTimeBeforeFirstAppearance。
// 同一请求同时具备多类错误条件时只报告优先级最高者。
func (c ErrorCode) String() string {
	switch c {
	case ErrRuleVersionVoided:
		return "RuleVersionVoided"
	case ErrAmbiguousOrder:
		return "AmbiguousOrder"
	case ErrDanglingReference:
		return "DanglingReference"
	case ErrTimeBeforeFirstAppearance:
		return "TimeBeforeFirstAppearance"
	}
	return "None"
}

// Error 是重建/判定失败的结构化错误。
type Error struct {
	Code   ErrorCode
	Detail string
}

func (e *Error) Error() string { return "orphan." + e.Code.String() + ": " + e.Detail }

// AuditRecord 记录一次判定的输入、依据版本与结论，供事后核查。
type AuditRecord struct {
	Seq           uint64 // 判定的线性化序号
	Query         Query
	Declared      int
	Governing     int // 判定时治理 Query.At 的版本，-1 表示无
	Status        Status
	Err           ErrorCode // ErrNone 表示成功
	EventsScanned int
}

// ObjectState 是网络在某一历史时刻的单个对象状态。
type ObjectState struct {
	Type         ObjectTypeID
	CreatedAt    Time
	Properties   map[string]string
	MarkedOrphan bool
}

// LinkState 是网络在某一历史时刻的单条链接状态。
type LinkState struct {
	Type      LinkTypeID
	From, To  ObjectID
	CreatedAt Time
	Active    bool
}

// NetworkState 是对象-链接网络在某一历史时刻的重建结果。
type NetworkState struct {
	At      Time
	Objects map[ObjectID]ObjectState
	Links   map[LinkID]LinkState
}

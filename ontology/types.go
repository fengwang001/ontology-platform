// Package ontology 实现本体平台中逻辑删除对象的溯源审计与复活协调器。
//
// 对象的生命周期由若干段存活区间（Interval）构成：创建或复活开启一段区间，
// 删除结束当前区间。每段区间拥有全局唯一的区间标识，审计事件通过该标识
// 精确归属到某一段生命周期。
package ontology

import "errors"

// 标识类型。
type (
	ObjectID   string
	LinkID     string
	IntervalID string
	EventID    string
	GrantID    string
	Actor      string
)

// LogicalTime 是协调器内部的全局逻辑时钟。
//
// 所有被接受的状态变更操作在同一把互斥锁内按序获得单调递增的逻辑时间，
// 因此逻辑时间的全序关系即系统对外可观察的全局串行顺序。不同事件的逻辑
// 时间绝不相同，这使得“链接失效时点恰好等于某次删除时点”的判断是 O(1)
// 的整数相等比较，且不存在并发歧义。
type LogicalTime uint64

// Action 是权限条目授权的操作类别。
type Action string

const (
	ActionRead   Action = "read"
	ActionWrite  Action = "write"
	ActionDelete Action = "delete"
	ActionRevive Action = "revive"
)

// LinkPolicy 声明链接类型在对象删除时的行为。
type LinkPolicy int

const (
	// CascadeInvalidate 表示链接随任一端对象的删除一并失效：
	// 链接记录带上失效时点进入不可用状态。
	CascadeInvalidate LinkPolicy = iota
	// Independent 表示链接独立于对象存活状态继续存在。
	Independent
)

// LinkType 是一类链接的声明。
type LinkType struct {
	Name   string
	Policy LinkPolicy
}

// Grant 是一条权限条目。删除/复活操作必须引用一条有效权限条目；
// 审计事件保存该条目在操作发生时刻的内容快照，条目之后被吊销不影响
// 历史审计记录的可读性。
type Grant struct {
	ID      GrantID
	Actor   Actor
	Action  Action
	Object  ObjectID // 空串表示通配全部对象
	Revoked bool
}

// EventKind 是审计事件的类别。
type EventKind int

const (
	EventCreate EventKind = iota
	EventDelete
	EventRevive
	EventRead
	EventWrite
)

// Event 是一条审计事件。Interval 字段给出其归属的存活区间标识。
type Event struct {
	ID       EventID
	Kind     EventKind
	Object   ObjectID
	Interval IntervalID
	Actor    Actor
	Time     LogicalTime
	Grant    Grant // 操作发生时刻的权限条目快照

	// 仅复活事件使用。
	ResumesDeletion EventID  // 本复活延续的那一次删除事件
	RestoreLinks    bool     // 是否声明尝试恢复删除前持有的链接
	RestoredLinks   []LinkID // 实际恢复的链接

	// 仅写事件使用。
	Payload string
}

// Interval 是一段存活区间。OpenedBy 是开启本区间的创建/复活事件，
// ClosedBy 是结束本区间的删除事件；区间仍存活时 ClosedBy 为空。
type Interval struct {
	ID       IntervalID
	Object   ObjectID
	Index    int // 从 1 开始的区间序号
	OpenedBy EventID
	ClosedBy EventID
	Events   []Event // 本区间内发生的全部事件，按逻辑时间升序
}

// Open 报告区间是否仍处于存活状态。
func (iv *Interval) Open() bool { return iv.ClosedBy == "" }

// Link 是对象之间的一条链接关系记录。
type Link struct {
	ID       LinkID
	Type     string
	From, To ObjectID
	// InvalidatedAt 为 0 表示链接可用；否则为链接进入不可用状态的失效时点。
	// 失效时点一旦写入不再被后续事件覆盖（首次失效生效）。
	InvalidatedAt LogicalTime
}

// Available 报告链接当前是否可用。
func (l *Link) Available() bool { return l.InvalidatedAt == 0 }

// ObjectHistory 是审计查询的返回结果：全部存活区间及区间内事件。
type ObjectHistory struct {
	Object    ObjectID
	Alive     bool
	Intervals []Interval
}

// 可区分的错误类别。调用方应使用 errors.Is 判定。
var (
	// ErrPermissionDenied 权限不足：权限条目不存在、已吊销、
	// 或与操作的主体/动作/对象不匹配。
	ErrPermissionDenied = errors.New("ontology: permission denied")
	// ErrObjectDeleted 对象当前处于删除状态，请求的操作不被接受。
	ErrObjectDeleted = errors.New("ontology: object is deleted")
	// ErrObjectNotDeleted 对象当前处于存活状态，复活请求不被接受。
	ErrObjectNotDeleted = errors.New("ontology: object is not deleted")
	// ErrStaleDeletion 复活指向的删除事件并非该对象最近一次删除。
	ErrStaleDeletion = errors.New("ontology: revive does not resume the latest deletion")
	// ErrLinkRestore 链接恢复请求指向了不满足
	// “失效时点恰好等于本次删除时点”条件的链接记录。
	ErrLinkRestore = errors.New("ontology: link does not satisfy restore condition")
	// ErrNotFound 引用的对象、链接或权限条目不存在。
	ErrNotFound = errors.New("ontology: not found")
	// ErrAlreadyExists 创建的对象或链接标识已被占用。
	ErrAlreadyExists = errors.New("ontology: already exists")
)

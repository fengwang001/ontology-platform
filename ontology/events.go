package ontology

// EventKind 区分事件类型。
type EventKind int

const (
	// EventCreate 建立实例并指定初始对象类型，必须是实例的首个事件。
	EventCreate EventKind = iota
	// EventSetProperty 普通属性赋值。
	EventSetProperty
	// EventEvolveType 类型演变：演变为直接子类型或退回直接父类型。
	EventEvolveType
)

// Event 是一条追加的事件记录。Seq 由事件存储在追加时分配，
// Time 为客户端给出的逻辑时刻；同一实例内 Time 必须互不相同，
// 否则重建时无法确定先后顺序。
type Event struct {
	Seq        uint64
	InstanceID string
	Time       int64
	Kind       EventKind
	// TypeID 在 EventCreate 下为初始类型，在 EventEvolveType 下为目标类型。
	TypeID string
	// Property 与 Value 仅在 EventSetProperty 下有意义。
	Property string
	Value    Value
}

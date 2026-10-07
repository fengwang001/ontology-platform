package ontology

// EventKind 区分事件类型。
type EventKind int

const (
	// EvCreated 实例创建，指定初始对象类型。必须是一条事件流的首个事件。
	EvCreated EventKind = iota
	// EvPropertySet 属性赋值。
	EvPropertySet
	// EvTypeEvolve 类型演变：演化为更细化的子类型或退回更一般的父类型。
	EvTypeEvolve
)

// Event 是一条追加式事件记录。
//
// Seq 由事件存储在追加时分配，定义同一实例内事件的权威顺序；
// Time 是客户端提供的逻辑时刻，同一实例内必须严格递增，用于
// 截止时刻过滤与规则版本选择。
type Event struct {
	Seq    int
	Time   int64
	Kind   EventKind
	TypeID string // EvCreated / EvTypeEvolve 的目标类型
	Prop   string // EvPropertySet 的属性名
	Val    Value  // EvPropertySet 的取值
}

// Created 构造实例创建事件。
func Created(t int64, typeID string) Event {
	return Event{Time: t, Kind: EvCreated, TypeID: typeID}
}

// Set 构造属性赋值事件。
func Set(t int64, prop string, val Value) Event {
	return Event{Time: t, Kind: EvPropertySet, Prop: prop, Val: val}
}

// Evolve 构造类型演变事件。
func Evolve(t int64, targetTypeID string) Event {
	return Event{Time: t, Kind: EvTypeEvolve, TypeID: targetTypeID}
}

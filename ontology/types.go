package ontology

// InstanceID 对象实例标识，全局全序（字典序）用于跨实例占用的确定性排序。
type InstanceID string

// ActionID 动作执行标识，全局唯一。
type ActionID string

// CallerID 普通乐观更新调用方标识。
type CallerID string

// PropertyValue 属性值。
type PropertyValue = any

// PropertySpec 属性约束：列表基数 [Min, Max]（Max<0 表示不限），Required 表示必填标量。
type PropertySpec struct {
	Name     string
	Required bool
	IsList   bool
	Min      int
	Max      int
}

// ObjectType 对象类型定义。
type ObjectType struct {
	Name       string
	Properties []PropertySpec
}

// LinkType 链接类型定义。
type LinkType struct {
	Name string
	From string
	To   string
}

// Link 实例间链接。
type Link struct {
	Type string
	From InstanceID
	To   InstanceID
}

// InstanceSnapshot 实例的一致性快照（读结果）。
type InstanceSnapshot struct {
	ID         InstanceID
	Type       string
	Version    uint64
	Properties map[string]PropertyValue
	Occupied   bool
	Holder     ActionID
}

// Mutation 一次属性变更。
type Mutation struct {
	Property string
	Value    PropertyValue
}

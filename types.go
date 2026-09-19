package ontology

// Object 是本体中的一个对象实例。
type Object struct {
	ID         string
	Type       string
	Attributes map[string]any
}

// Relation 描述两个对象之间的一条有向关系。
type Relation struct {
	Type   string
	FromID string
	ToID   string
}

// ParamType 是 Action 参数的声明类型。
type ParamType string

const (
	TypeString ParamType = "string"
	TypeInt    ParamType = "int"
	TypeBool   ParamType = "bool"
	TypeFloat  ParamType = "float"
)

// ParamSpec 声明一个参数：名字、类型、是否必填、默认值。
type ParamSpec struct {
	Name     string
	Type     ParamType
	Required bool
	// Default 仅在可选参数缺失时使用。引擎会深拷贝该值，
	// 保证多次调用之间默认值互不污染。
	Default any
}

// Schema 是一个 Action 的参数契约。
type Schema struct {
	Params []ParamSpec
}

// ImpactKind 标记一条影响清单项的类别。
type ImpactKind string

const (
	ImpactObjectCreated   ImpactKind = "object_created"
	ImpactObjectChanged   ImpactKind = "object_changed"
	ImpactObjectDeleted   ImpactKind = "object_deleted"
	ImpactRelationAdded   ImpactKind = "relation_added"
	ImpactRelationRemoved ImpactKind = "relation_removed"
)

// ImpactItem 描述一条受影响的对象或关系。
type ImpactItem struct {
	Kind     ImpactKind
	ObjectID string
	// ObjType / RelType 在对应类别下有值。
	ObjType string
	RelType Relation
	// Attr 记录属性修改影响到的属性名。
	Attr string
}

// Record 是一次成功提交产出的不可变执行记录。
type Record struct {
	Seq     int64
	Action  string
	Params  map[string]any
	Impacts []ImpactItem
}

// Package ontology 提供本体对象类型的注册、引用解析与原子重命名能力。
package ontology

// PrimitiveType 是属性的基元类型。
type PrimitiveType string

const (
	PrimitiveString  PrimitiveType = "string"
	PrimitiveInteger PrimitiveType = "integer"
	PrimitiveDouble  PrimitiveType = "double"
	PrimitiveBoolean PrimitiveType = "boolean"
	// PrimitiveObjectRef 表示属性值是对另一个对象类型的引用。
	PrimitiveObjectRef PrimitiveType = "object_ref"
)

// Property 描述对象类型的一个属性。
// 当 Type 为 PrimitiveObjectRef 时，RefType 持有被引用对象类型的名字。
type Property struct {
	Name    string
	Type    PrimitiveType
	RefType string
}

// LinkType 描述两个对象类型之间的链接。
type LinkType struct {
	Name       string
	SourceType string
	TargetType string
}

// ActionParam 是 Action 的一个参数，TypeRef 引用一个对象类型名。
type ActionParam struct {
	Name    string
	TypeRef string
}

// PropertyRef 以 "TypeName.PropertyName" 形式引用某类型的属性。
type PropertyRef struct {
	TypeName     string
	PropertyName string
}

// Action 描述一个作用在本体上的动作，其参数、返回值与规则均可引用对象类型。
type Action struct {
	Name        string
	Params      []ActionParam
	ReturnRefs  []string
	PropertyRef []PropertyRef
}

// ObjectType 是一个对象类型定义。
type ObjectType struct {
	Name       string
	Properties []Property
	Links      []LinkType
	Actions    []Action
}

// clone 返回 ObjectType 的深拷贝。
func (t *ObjectType) clone() *ObjectType {
	cp := *t
	cp.Properties = append([]Property(nil), t.Properties...)
	cp.Links = append([]LinkType(nil), t.Links...)
	cp.Actions = append([]Action(nil), t.Actions...)
	for i := range cp.Actions {
		cp.Actions[i].Params = append([]ActionParam(nil), t.Actions[i].Params...)
		cp.Actions[i].ReturnRefs = append([]string(nil), t.Actions[i].ReturnRefs...)
		cp.Actions[i].PropertyRef = append([]PropertyRef(nil), t.Actions[i].PropertyRef...)
	}
	return &cp
}

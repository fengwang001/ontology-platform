package ontology

import "fmt"

// ObjectType 是对象类型的静态声明。链接类型按对象类型名称判断方向是否允许。
type ObjectType struct {
	id   string // 类型名称，作为跨链接类型引用的稳定标识
	name string
}

// NewObjectType 声明一个对象类型。
func NewObjectType(id, displayName string) *ObjectType {
	return &ObjectType{id: id, name: displayName}
}

// ID 返回对象类型名称。
func (t *ObjectType) ID() string { return t.id }

// Name 返回展示名。
func (t *ObjectType) Name() string { return t.name }

// Object 是一个对象实例。deleted 是逻辑删除标记：逻辑删除后的实例
// 在创建仲裁中按“不存在”处理，且该状态只能通过 Store 改变。
type Object struct {
	id         string
	objectType string
	deleted    bool
}

// NewObjectType 之外提供实例构造器；Store 内部使用以登记实例。
func NewObject(id string, objectType string) *Object {
	return &Object{id: id, objectType: objectType}
}

// ID 返回实例标识。
func (o *Object) ID() string { return o.id }

// Type 返回对象类型名称。
func (o *Object) Type() string { return o.objectType }

// IsAlive 报告实例是否未被逻辑删除。
func (o *Object) IsAlive() bool { return !o.deleted }

func (o *Object) String() string {
	return fmt.Sprintf("Object(%s:%s, alive=%t)", o.objectType, o.id, o.IsAlive())
}

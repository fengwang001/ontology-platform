// Package ontology 实现本体平台的跨对象类型权限传播模块。
package ontology

// ObjectTypeID 标识一种对象类型。
type ObjectTypeID string

// LinkTypeID 标识一种链接类型。
type LinkTypeID string

// InstanceID 标识一个对象实例。
type InstanceID string

// SubjectID 标识一个主体（用户或服务账号）。
type SubjectID string

// ActionTypeID 标识一种动作声明。
type ActionTypeID string

// ObjectType 描述对象类型及其允许的属性集合。
type ObjectType struct {
	ID         ObjectTypeID
	Properties []string
}

// HasProperty 报告该对象类型是否声明了给定属性。
func (t ObjectType) HasProperty(name string) bool {
	for _, p := range t.Properties {
		if p == name {
			return true
		}
	}
	return false
}

// LinkType 描述从 From 对象类型指向 To 对象类型的有向链接类型。
type LinkType struct {
	ID   LinkTypeID
	From ObjectTypeID
	To   ObjectTypeID
}

// Instance 是一个对象实例。Version 与 CascadeMark 由引擎维护，
// 只在事务提交时推进；被拒绝的动作不会修改它们。
type Instance struct {
	ID          InstanceID
	Type        ObjectTypeID
	Props       map[string]string
	Version     int64
	CascadeMark int64
}

// clone 返回实例的深拷贝。
func (in *Instance) clone() *Instance {
	out := *in
	out.Props = make(map[string]string, len(in.Props))
	for k, v := range in.Props {
		out.Props[k] = v
	}
	return &out
}

// Link 是两个实例之间、具有给定链接类型的有向边。
type Link struct {
	Type LinkTypeID
	From InstanceID
	To   InstanceID
}

// AuditRecord 是动作成功提交时追加的审计记录。
type AuditRecord struct {
	Seq       int64
	Action    ActionTypeID
	Subject   SubjectID
	Created   []InstanceID
	Modified  []InstanceID
	Touched   []InstanceID
	Skipped   []InstanceID
	Committed bool
}

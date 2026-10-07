// Package spec 定义本体平台动作子系统的共享数据模型：
// 对象类型模式、写入操作与动作定义。该包只包含数据类型，
// 不包含任何行为逻辑，因此被引擎与朴素对照模型共同引用。
package spec

// FieldType 是对象字段的标量类型。
type FieldType string

const (
	FieldString FieldType = "string"
	FieldInt    FieldType = "int"
	FieldFloat  FieldType = "float"
	FieldBool   FieldType = "bool"
)

// Schema 描述一个对象类型的字段集合。
type Schema struct {
	Fields map[string]FieldType
}

// WriteOp 是写入操作的种类。
type WriteOp int

const (
	OpCreate WriteOp = iota
	OpUpdate
	OpDelete
)

func (op WriteOp) String() string {
	switch op {
	case OpCreate:
		return "create"
	case OpUpdate:
		return "update"
	case OpDelete:
		return "delete"
	}
	return "unknown"
}

// Write 表示对单个对象实例的一次写入。
type Write struct {
	Type   string         // 对象类型名
	ID     string         // 实例 ID
	Op     WriteOp        // 创建 / 更新 / 删除
	Fields map[string]any // create / update 的写入内容
}

// Op 是动作体中的一步：要么是一次写入，要么是一次嵌套动作调用。
type Op struct {
	Write *Write // 非空时表示一次写入
	Call  string // 非空时表示调用名为 Call 的动作定义
}

// ActionDef 是一个动作定义：对若干对象实例的一组写入
// （可夹杂对其他动作定义的嵌套调用），整体原子生效。
type ActionDef struct {
	Name string
	Body []Op
}

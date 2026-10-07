package compensate

import "fmt"

// WriteOp 是具体的副作用子操作：把某对象的某属性置为某值。
// 逆操作为确定性的“恢复旧值/删除属性”。Fail* 字段用于故障注入。
type WriteOp struct {
	ID             string
	ObjectID       string
	Property       string
	Value          string
	FailApply      bool
	FailCompensate bool
}

func (op WriteOp) label() string {
	id := op.ID
	if id == "" {
		id = "set"
	}
	return fmt.Sprintf("%s(%s.%s=%s)", id, op.ObjectID, op.Property, op.Value)
}

// AppliedEffect 记录一条已生效子操作与其确定性逆操作所需信息。
type AppliedEffect struct {
	Branch string
	Index  int
	Op     WriteOp
	Undo   UndoRecord
}

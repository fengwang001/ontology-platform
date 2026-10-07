package ontologyindex

import "fmt"

// ChangeEvent 是一条对象属性取值变更事件。
type ChangeEvent struct {
	// EventID 全局唯一，用于精确去重（重复投递是幂等的）。
	EventID string
	// ObjectType / ObjectID 定位被修改对象。
	ObjectType string
	ObjectID   string
	// PropertyID 引用稳定的逻辑属性（不引用可能改名的物理字段）。
	PropertyID string
	// NewValue 是该属性的新取值。
	NewValue Value
	// EffectiveAt 是该变更在业务世界中的逻辑生效时刻，
	// 是确定合法串行顺序与版本归属的唯一依据。
	EffectiveAt LogicalClock
	// ArrivedAt 是物理到达序号，仅用于审计，绝不参与顺序判定。
	ArrivedAt int64
}

// valid 检查事件自身的基本完备性。
func (e ChangeEvent) valid() bool {
	return e.EventID != "" && e.ObjectType != "" && e.ObjectID != "" && e.PropertyID != ""
}

// String 便于审计与测试日志阅读。
func (e ChangeEvent) String() string {
	return fmt.Sprintf("event{id=%s obj=%s/%s prop=%s ts=%d arrived=%d}",
		e.EventID, e.ObjectType, e.ObjectID, e.PropertyID, e.EffectiveAt, e.ArrivedAt)
}

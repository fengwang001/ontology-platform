package ontology

// BreakKind 对破坏性变更进行分类，调用方可以据此区分具体破坏类型。
type BreakKind string

const (
	// BreakPropertyRemoved 删除了已有属性。
	BreakPropertyRemoved BreakKind = "property_removed"
	// BreakRequiredTightened 把可空属性收紧为必填。
	BreakRequiredTightened BreakKind = "required_tightened"
	// BreakTypeChanged 改变了已有属性的类型。
	BreakTypeChanged BreakKind = "type_changed"
	// BreakPrimaryKeyChanged 主键发生变化（改名、换属性或缺失）。
	BreakPrimaryKeyChanged BreakKind = "primary_key_changed"
	// BreakRequiredPropertyAdded 新增了必填属性，存量实例无法自动满足。
	BreakRequiredPropertyAdded BreakKind = "required_property_added"
)

// BreakingChange 描述一次演进中检测到的某一处破坏性变更。
type BreakingChange struct {
	ObjectType string
	Property   string
	Kind       BreakKind
}

func (b BreakingChange) String() string {
	return string(b.Kind) + " on objectType " + b.ObjectType + " property " + b.Property
}

// ValidationError 表示提交的 ObjectType 定义本身不合法（与旧版本无关），
// 例如属性名重复、主键数量不为一个。
type ValidationError struct {
	ObjectType string
	Property   string
	Reason     string
}

func (e *ValidationError) Error() string {
	return "invalid objectType " + e.ObjectType + " property " + e.Property + ": " + e.Reason
}

// BreakingChangesError 汇总一次演进中检测到的全部破坏性变更。
// 当且仅当存在未提供迁移函数的破坏性变更时，演进才因此被拒绝。
type BreakingChangesError struct {
	Changes []BreakingChange
}

func (e *BreakingChangesError) Error() string {
	msg := "breaking changes without migration:"
	for _, c := range e.Changes {
		msg += "\n  - " + c.String()
	}
	return msg
}

// MigrationError 记录迁移函数在某个 ObjectType 的某个实例上失败的信息。
// 发生迁移失败时，整次演进以及已经迁移过的实例都会回滚。
type MigrationError struct {
	ObjectType string
	InstanceID string
	Err        error
}

func (e *MigrationError) Error() string {
	id := e.InstanceID
	if id == "" {
		id = "?"
	}
	return "migration failed for objectType " + e.ObjectType + " instance " + id + ": " + e.Err.Error()
}

func (e *MigrationError) Unwrap() error { return e.Err }

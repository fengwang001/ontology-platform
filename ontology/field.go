package ontology

// FieldDef 是一个字段在某个版本下的完整定义。
type FieldDef struct {
	Name       string
	Type       ValueType
	Constraint Constraint
	Nullable   bool // 是否允许取值缺失
	HasDefault bool
	Default    Value
}

// FieldSignature 是字段语义的可比较快照，供外部引用方捕获并做漂移检测。
type FieldSignature struct {
	Exists     bool
	Kind       Kind
	Constraint Constraint
	Nullable   bool
	HasDefault bool
	Default    Value
}

// SignatureOf 从字段定义提取语义快照；def 为 nil 表示字段不存在。
func SignatureOf(def *FieldDef) FieldSignature {
	if def == nil {
		return FieldSignature{Exists: false}
	}
	return FieldSignature{
		Exists:     true,
		Kind:       def.Type.Kind(),
		Constraint: def.Constraint,
		Nullable:   def.Nullable,
		HasDefault: def.HasDefault,
		Default:    def.Default,
	}
}

// signatureEqual 判定两个签名是否完全相等（含可空标志），
// 用于提交时的 CAS 基线校验。
func signatureEqual(a, b FieldSignature) bool {
	if a.Exists != b.Exists {
		return false
	}
	if !a.Exists {
		return true
	}
	return a.Kind == b.Kind &&
		a.Constraint.Equal(b.Constraint) &&
		a.Nullable == b.Nullable &&
		a.HasDefault == b.HasDefault &&
		(!a.HasDefault || a.Default.Equal(b.Default))
}

// sameSemantics 比较两个签名在指定依赖维度上是否一致。
// 存在性变化（字段被删除或凭空出现）必然使任何引用方的
// 判断逻辑失效，因此无论依赖维度如何都视为漂移。
func (s FieldSignature) sameSemantics(o FieldSignature, d Dependency) bool {
	if s.Exists != o.Exists {
		return false
	}
	if !s.Exists {
		return true
	}
	if d.OnType && s.Kind != o.Kind {
		return false
	}
	if d.OnConstraint && !s.Constraint.Equal(o.Constraint) {
		return false
	}
	if d.OnDefault && (s.HasDefault != o.HasDefault || (s.HasDefault && o.HasDefault && !s.Default.Equal(o.Default))) {
		return false
	}
	return true
}

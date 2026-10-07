package ontology

// FieldSemantics 是引用方判定时可见的字段语义（字段定义）。
// 字段被删除时传入 nil。
type FieldSemantics = FieldDef

// EffectiveView 是引用方在某个字段语义下看到的“字段名 -> 取值”视图，
// 已按新增默认值/回填规则、删除字段缺省等规则完成了取值投影。
type EffectiveView map[string]Value

// Reference 表示链接类型或动作对某对象类型字段语义的依赖。
// Predicate 是引用方原有判断逻辑（链接判定/前置条件/后置条件），
// 它既可以依赖实例取值，也可以依赖字段自身的语义（取值域、默认值、
// 类型）。同一段逻辑分别在旧语义与新语义下求值，结果不同即语义漂移。
type Reference interface {
	ID() string
	Kind() string
	ObjectTypeName() string
	FieldName() string
	Predicate(view EffectiveView, sem *FieldSemantics) bool
}

type LinkTypeReference struct {
	RefID       string
	ObjType     string
	Field       string
	Desc        string
	OldJudgment func(EffectiveView, *FieldSemantics) bool
	NewJudgment func(EffectiveView, *FieldSemantics) bool
}

type ActionReference struct {
	RefID    string
	ObjType  string
	Field    string
	Phase    string
	Judgment func(EffectiveView, *FieldSemantics) bool
}

// ReferenceRegistry 登记对象类型字段的全部外部引用方。
type ReferenceRegistry struct {
	refs map[string][]Reference
}

func (r LinkTypeReference) ID() string             { return r.RefID }
func (r LinkTypeReference) Kind() string           { return "link-type" }
func (r LinkTypeReference) ObjectTypeName() string { return r.ObjType }
func (r LinkTypeReference) FieldName() string      { return r.Field }

// Predicate 返回引用方登记时的原有判断逻辑（旧语义下的判定）。
func (r LinkTypeReference) Predicate(view EffectiveView, sem *FieldSemantics) bool {
	return r.OldJudgment(view, sem)
}

// NewPredicate 返回同一链接在新字段语义下必须采用的判定逻辑；
// 为 nil 表示链接无法迁移，任何语义差异都算漂移。
func (r LinkTypeReference) NewPredicate() func(EffectiveView, *FieldSemantics) bool {
	return r.NewJudgment
}

func (r ActionReference) ID() string             { return r.RefID }
func (r ActionReference) Kind() string           { return "action:" + r.Phase }
func (r ActionReference) ObjectTypeName() string { return r.ObjType }
func (r ActionReference) FieldName() string      { return r.Field }
func (r ActionReference) Predicate(view EffectiveView, sem *FieldSemantics) bool {
	return r.Judgment(view, sem)
}

// NewReferenceRegistry 创建空登记表。
func NewReferenceRegistry() *ReferenceRegistry {
	return &ReferenceRegistry{refs: make(map[string][]Reference)}
}

func key(objectType, field string) string { return objectType + "\x00" + field }

// Add 登记一个引用方（链接类型或动作的前/后置条件）。
func (r *ReferenceRegistry) Add(ref Reference) {
	k := key(ref.ObjectTypeName(), ref.FieldName())
	r.refs[k] = append(r.refs[k], ref)
}

// On 返回引用了指定对象类型指定字段的全部引用方。
func (r *ReferenceRegistry) On(objectType, field string) []Reference {
	out := r.refs[key(objectType, field)]
	cp := make([]Reference, len(out))
	copy(cp, out)
	return cp
}

// adaptableReference 由能够同时提供新语义判定逻辑的引用方实现。
type adaptableReference interface {
	NewPredicate() func(EffectiveView, *FieldSemantics) bool
}

package ontology

// RefKind 标识外部引用方的种类。
type RefKind int

const (
	// LinkRef 表示链接类型把该字段用作链接判定的一部分。
	LinkRef RefKind = iota
	// ActionRef 表示动作把该字段用作前置/后置条件的判断依据。
	ActionRef
)

func (k RefKind) String() string {
	if k == LinkRef {
		return "link"
	}
	return "action"
}

// Dependency 声明一个引用方依赖字段语义的哪些维度。
// 字段存在性不在其列：存在性变化对任何引用方都必然构成漂移。
type Dependency struct {
	OnType       bool // 依赖字段取值类型
	OnConstraint bool // 依赖字段取值约束
	OnDefault    bool // 依赖字段默认取值
}

// FieldReference 是一个链接类型或动作对某对象类型字段的引用。
// Captured 是引用建立（或上一版本对齐）时捕获的字段语义快照；
// 兼容性判定时用它与变更后的语义对比，判断引用方原有判断逻辑
// 是否会在新语义下得到不同结果。
type FieldReference struct {
	ID         string
	Kind       RefKind
	Owner      string // 链接类型或动作的标识
	ObjectType string
	Field      string
	Depends    Dependency
	Captured   FieldSignature
}

// RefRegistry 保存全部外部引用。只读查询，并发安全由 Engine 的串行化保证。
type RefRegistry struct {
	refs map[string]FieldReference
}

func NewRefRegistry() *RefRegistry {
	return &RefRegistry{refs: make(map[string]FieldReference)}
}

// Register 登记一个引用方，并以其建立时的字段语义作为 Captured 快照。
func (r *RefRegistry) Register(ref FieldReference) {
	r.refs[ref.ID] = ref
}

// Unregister 移除一个引用方。
func (r *RefRegistry) Unregister(id string) {
	delete(r.refs, id)
}

// OnField 返回引用指定对象类型字段的全部引用方。
func (r *RefRegistry) OnField(objectType, field string) []FieldReference {
	var out []FieldReference
	for _, ref := range r.refs {
		if ref.ObjectType == objectType && ref.Field == field {
			out = append(out, ref)
		}
	}
	return out
}

// driftedAgainst 返回因字段语义从旧快照漂移而受影响的引用方 ID。
func (r *RefRegistry) driftedAgainst(objectType, field string, newSig FieldSignature) []string {
	var out []string
	for _, ref := range r.OnField(objectType, field) {
		if !ref.Captured.sameSemantics(newSig, ref.Depends) {
			out = append(out, ref.ID)
		}
	}
	return out
}

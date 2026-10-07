package ontology

// ValueType 是属性值的类型标签。
type ValueType string

const (
	TypeString ValueType = "string"
	TypeInt    ValueType = "int"
	TypeFloat  ValueType = "float"
	TypeBool   ValueType = "bool"
)

// PropertyDef 描述对象类型的一个属性定义。
// ID 在类型的整个生命周期内稳定；重命名只改 Name，不改 ID，
// 因此历史值（按 ID 存储）在迁移前后都能被正确定位。
type PropertyDef struct {
	ID       string
	Name     string
	Type     ValueType
	Required bool
}

// ObjectTypeVersion 是对象类型属性定义的一个版本，
// 有效期为版本区间 [From, To)。
type ObjectTypeVersion struct {
	Interval
	Props []PropertyDef
}

// objectTypeHistory 是某对象类型的全部定义版本（按 From 升序，不可变发布）。
type objectTypeHistory struct {
	versions []ObjectTypeVersion
}

// versionAt 返回恰好覆盖 v 的那一版定义（精确匹配，非就近取整）。
func (h *objectTypeHistory) versionAt(v Version) (*ObjectTypeVersion, int, bool) {
	ivs := make([]Interval, len(h.versions))
	for i, ver := range h.versions {
		ivs[i] = ver.Interval
	}
	idx, steps := findInterval(ivs, v)
	if idx < 0 {
		return nil, steps, false
	}
	return &h.versions[idx], steps, true
}

// propByName 在该版本内按属性名查找定义。
func (tv *ObjectTypeVersion) propByName(name string) (PropertyDef, bool) {
	for _, p := range tv.Props {
		if p.Name == name {
			return p, true
		}
	}
	return PropertyDef{}, false
}

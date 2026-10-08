package ontology

// PropertyType 描述属性值的静态类型。
type PropertyType int

const (
	PropString PropertyType = iota
	PropInt
	// PropObjectRef 表示属性值是对另一个对象实例的引用（Ref）。
	PropObjectRef
)

// PropertySpec 描述对象类型上一个属性的约束。
type PropertySpec struct {
	Name     string
	Type     PropertyType
	Required bool
	// RefObjectType 仅在 Type == PropObjectRef 时有效，非空时约束被引用对象的类型。
	RefObjectType string
	// Validate 是可选的自定义校验，在静态类型检查通过后执行。
	Validate func(value any) error
}

// ObjectType 是对象类型的注册描述。
type ObjectType struct {
	RID        string
	Properties []PropertySpec
}

func (ot *ObjectType) spec(name string) (PropertySpec, bool) {
	for _, p := range ot.Properties {
		if p.Name == name {
			return p, true
		}
	}
	return PropertySpec{}, false
}

// LinkType 是链接类型的注册描述。
type LinkType struct {
	RID              string
	SourceObjectType string
	TargetObjectType string
	// MaxTargetsPerSource 是基数约束：从同一源实例出发、该类型的链接最多允许的目标数。
	// 0 表示不限制；1 即“最多只能指向一个目标”。
	MaxTargetsPerSource int
}

// ObjectInstance 是已落地的对象实例。
type ObjectInstance struct {
	RID        string
	TypeRID    string
	Properties map[string]any
}

// LinkInstance 是已落地的链接实例。
type LinkInstance struct {
	RID       string
	TypeRID   string
	SourceRID string
	TargetRID string
}

// Ref 在批量导入条目中表示对一个对象实例的引用：
// 要么引用同批次中某个条目即将创建的对象（EntryID 非空），
// 要么引用批次之外已经存在的对象（ObjectRID 非空）。
type Ref struct {
	EntryID   string
	ObjectRID string
}

// EntryRef 引用同批次条目声明的对象。
func EntryRef(entryID string) Ref { return Ref{EntryID: entryID} }

// ExistingRef 引用批次外已存在的对象。
func ExistingRef(rid string) Ref { return Ref{ObjectRID: rid} }

package ontology

// ValueType 标识 PropertyType 承载的取值类型。
type ValueType string

const (
	TypeString ValueType = "string"
	TypeInt    ValueType = "int"
	TypeFloat  ValueType = "float"
	TypeBool   ValueType = "bool"
)

// PropertyType 描述对象类型上的一个属性。
type PropertyType struct {
	Name     string
	Type     ValueType
	Required bool
	Primary  bool
}

// ObjectType 由若干 PropertyType 组成。同一个 ObjectType 内属性名不可重复，
// 主键有且只有一个。
type ObjectType struct {
	Name       string
	Properties []PropertyType
}

// Schema 是某一版本时刻全部 ObjectType 的不可变快照。
type Schema struct {
	Version     int
	ObjectTypes []ObjectType
}

func (s *Schema) objectType(name string) (ObjectType, bool) {
	for _, ot := range s.ObjectTypes {
		if ot.Name == name {
			return ot, true
		}
	}
	return ObjectType{}, false
}

func cloneSchema(s *Schema) *Schema {
	if s == nil {
		return nil
	}
	cp := &Schema{Version: s.Version, ObjectTypes: make([]ObjectType, len(s.ObjectTypes))}
	for i, ot := range s.ObjectTypes {
		cp.ObjectTypes[i] = cloneObjectType(ot)
	}
	return cp
}

func cloneObjectType(ot ObjectType) ObjectType {
	props := make([]PropertyType, len(ot.Properties))
	copy(props, ot.Properties)
	return ObjectType{Name: ot.Name, Properties: props}
}

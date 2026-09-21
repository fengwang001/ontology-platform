// Package ontology 提供本体（Ontology）的核心类型系统：
// 属性类型、对象类型、链接类型及其注册表。
package ontology

// PrimitiveType 属性值的基础类型。
type PrimitiveType string

const (
	TypeString    PrimitiveType = "string"
	TypeInteger   PrimitiveType = "integer"
	TypeDouble    PrimitiveType = "double"
	TypeBoolean   PrimitiveType = "boolean"
	TypeTimestamp PrimitiveType = "timestamp"
)

// PropertyType 描述一个属性的名称与取值类型。
type PropertyType struct {
	Name string        `json:"name"`
	Type PrimitiveType `json:"type"`
}

// ObjectType 描述一类对象（对标 Foundry ObjectType）。
type ObjectType struct {
	Name        string                   `json:"name"`
	Description string                   `json:"description,omitempty"`
	PrimaryKey  string                   `json:"primaryKey"`
	Properties  map[string]*PropertyType `json:"properties"`
}

// LinkType 描述两类对象之间的关联。
type LinkType struct {
	Name       string `json:"name"`
	SourceType string `json:"sourceType"`
	TargetType string `json:"targetType"`
}

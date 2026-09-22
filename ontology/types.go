// Package ontology 提供本体服务平台的核心领域模型与内存实现，
// 对标 Palantir Foundry Ontology：对象类型、关系类型、对象实例、
// 关系实例、动作（Action）与查询。
package ontology

import "time"

// PropertyType 是属性支持的数据类型。
type PropertyType string

const (
	TypeString    PropertyType = "string"
	TypeInteger   PropertyType = "integer"
	TypeDouble    PropertyType = "double"
	TypeBoolean   PropertyType = "boolean"
	TypeTimestamp PropertyType = "timestamp"
)

// valid 报告属性类型是否受支持。
func (t PropertyType) valid() bool {
	switch t {
	case TypeString, TypeInteger, TypeDouble, TypeBoolean, TypeTimestamp:
		return true
	}
	return false
}

// Property 描述对象类型上的一个属性定义。
type Property struct {
	Name     string       `json:"name"`
	Type     PropertyType `json:"type"`
	Required bool         `json:"required,omitempty"`
}

// ObjectType 定义一类对象的 schema。
type ObjectType struct {
	Name        string     `json:"name"`
	Description string     `json:"description,omitempty"`
	PrimaryKey  string     `json:"primaryKey"`
	Properties  []Property `json:"properties"`
}

// property 按名称查找属性定义。
func (ot *ObjectType) property(name string) *Property {
	for i := range ot.Properties {
		if ot.Properties[i].Name == name {
			return &ot.Properties[i]
		}
	}
	return nil
}

// Cardinality 描述关系类型的基数。
type Cardinality string

const (
	OneToOne   Cardinality = "ONE_TO_ONE"
	OneToMany  Cardinality = "ONE_TO_MANY"
	ManyToMany Cardinality = "MANY_TO_MANY"
)

// valid 报告基数是否受支持。
func (c Cardinality) valid() bool {
	switch c {
	case OneToOne, OneToMany, ManyToMany:
		return true
	}
	return false
}

// LinkType 定义两类对象之间的关系。
type LinkType struct {
	Name        string      `json:"name"`
	SourceType  string      `json:"sourceType"`
	TargetType  string      `json:"targetType"`
	Cardinality Cardinality `json:"cardinality"`
}

// Object 是某个对象类型的一个实例。
type Object struct {
	Type       string         `json:"type"`
	PrimaryKey string         `json:"primaryKey"`
	Properties map[string]any `json:"properties"`
	CreatedAt  time.Time      `json:"createdAt"`
	UpdatedAt  time.Time      `json:"updatedAt"`
}

// Link 是两个对象实例之间的关系。
type Link struct {
	Type      string `json:"type"`
	SourceKey string `json:"sourceKey"`
	TargetKey string `json:"targetKey"`
}

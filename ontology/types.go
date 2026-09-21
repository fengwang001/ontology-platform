package ontology

// PropertyType enumerates the supported scalar property types.
type PropertyType string

const (
	PropertyTypeString    PropertyType = "string"
	PropertyTypeInteger   PropertyType = "integer"
	PropertyTypeDouble    PropertyType = "double"
	PropertyTypeBoolean   PropertyType = "boolean"
	PropertyTypeTimestamp PropertyType = "timestamp"
	PropertyTypeDate      PropertyType = "date"
)

// PropertyDef describes a single property of an ObjectType.
type PropertyDef struct {
	APIName     string       `json:"apiName"`
	DisplayName string       `json:"displayName"`
	Type        PropertyType `json:"type"`
	Required    bool         `json:"required,omitempty"`
}

// ObjectType defines a class of objects in the ontology.
type ObjectType struct {
	APIName     string                 `json:"apiName"`
	DisplayName string                 `json:"displayName"`
	Description string                 `json:"description,omitempty"`
	PrimaryKey  string                 `json:"primaryKey"`
	Properties  map[string]PropertyDef `json:"properties"`
}

// Cardinality of a LinkType.
type Cardinality string

const (
	CardinalityOneToOne   Cardinality = "ONE_TO_ONE"
	CardinalityOneToMany  Cardinality = "ONE_TO_MANY"
	CardinalityManyToMany Cardinality = "MANY_TO_MANY"
)

// LinkType defines a relationship between two object types.
type LinkType struct {
	APIName     string      `json:"apiName"`
	DisplayName string      `json:"displayName"`
	SourceType  string      `json:"sourceType"`
	TargetType  string      `json:"targetType"`
	Cardinality Cardinality `json:"cardinality"`
}

// Object is a concrete instance of an ObjectType.
type Object struct {
	Type       string         `json:"__type"`
	PrimaryKey any            `json:"__primaryKey"`
	Properties map[string]any `json:"properties"`
}

// Link is a concrete edge between two objects.
type Link struct {
	Type     string `json:"type"`
	SourcePK any    `json:"sourcePk"`
	TargetPK any    `json:"targetPk"`
}

// ActionType defines a parameterized mutation over the ontology.
type ActionType struct {
	APIName     string              `json:"apiName"`
	DisplayName string              `json:"displayName"`
	Parameters  map[string]ParamDef `json:"parameters"`
	Rules       []ActionRule        `json:"rules"`
}

// ParamDef describes an action parameter.
type ParamDef struct {
	Type     PropertyType `json:"type"`
	Required bool         `json:"required,omitempty"`
}

// ActionRuleKind enumerates supported rule kinds.
type ActionRuleKind string

const (
	RuleCreateObject ActionRuleKind = "createObject"
	RuleModifyObject ActionRuleKind = "modifyObject"
	RuleDeleteObject ActionRuleKind = "deleteObject"
	RuleCreateLink   ActionRuleKind = "createLink"
	RuleDeleteLink   ActionRuleKind = "deleteLink"
)

// ActionRule is one mutation step executed when an action is applied.
// Values may reference parameters via the "$paramName" syntax.
type ActionRule struct {
	Kind       ActionRuleKind `json:"kind"`
	ObjectType string         `json:"objectType,omitempty"`
	LinkType   string         `json:"linkType,omitempty"`
	// PrimaryKey is the primary key expression for modify/delete rules.
	PrimaryKey string `json:"primaryKey,omitempty"`
	// Properties maps property apiName to a literal value or "$param".
	Properties map[string]any `json:"properties,omitempty"`
	// SourcePK/TargetPK are expressions for link rules.
	SourcePK string `json:"sourcePk,omitempty"`
	TargetPK string `json:"targetPk,omitempty"`
}

package ontology

// AttrType enumerates supported attribute value types.
type AttrType string

const (
	AttrInt    AttrType = "int"
	AttrFloat  AttrType = "float"
	AttrString AttrType = "string"
)

// AttrSpec declares one attribute of an object type.
type AttrSpec struct {
	Name     string
	Type     AttrType
	Required bool
}

// ObjectType declares an object type's attributes.
type ObjectType struct {
	Name  string
	Attrs map[string]AttrSpec
}

// AggKind selects how per-member contributions are combined in a view.
type AggKind string

const (
	AggSum   AggKind = "sum"
	AggCount AggKind = "count"
)

// SourceSpec binds an object type into an aggregate view.
type SourceSpec struct {
	Type       string
	GroupAttr  string // attribute whose value defines the group key
	ValueAttr  string // numeric attribute contributing to sum; ignored by count
	GroupValid bool   // whether a non-empty group value is mandatory
}

// ViewSpec declares an aggregate view derived purely from source instances.
type ViewSpec struct {
	Name    string
	Kind    AggKind
	Sources []SourceSpec
}

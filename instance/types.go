package instance

import "time"

// PrimaryKey is the stable primary key value of an object instance.
// It must be comparable (numbers, strings, or comparable structs).
type PrimaryKey = any

// Instance is an externally visible snapshot of a stored object
// instance.
type Instance struct {
	ObjectType string
	Key        PrimaryKey
	// Properties hold the full current property set. Update replaces
	// them wholesale.
	Properties map[string]any
	// Version starts at 1 on Create and increases by one on every
	// successful Create/Update of the key, including resurrection after
	// a logical Delete.
	Version int64
	// LastWriteTime is the time of the most recent successful write.
	LastWriteTime time.Time
}

// ValueKind enumerates the property value kinds understood by the
// constraint checker and by the deep-copy snapshot logic.
type ValueKind int

const (
	KindString ValueKind = iota + 1
	KindInt
	KindFloat
	KindBool
)

// PropertySpec declares one allowed property of an object type.
type PropertySpec struct {
	Name     string
	Kind     ValueKind
	Required bool
}

// TypeSpec declares the properties of an object type. When a type has
// no registered spec, writes for it carry arbitrary unvalidated
// properties.
type TypeSpec struct {
	Name       string
	Properties []PropertySpec
}

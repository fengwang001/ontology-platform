package ontology

import "time"

type TypeKind int

const (
	KindNamed TypeKind = iota
	KindAlias
	KindStruct
	KindInstance
)

type Type struct {
	Kind    TypeKind
	Name    string
	AliasTo *Type
	Fields  []Field
	Def     string
	Args    []Type
}

type Field struct {
	Name string
	Type Type
}

type ConstraintKind int

const (
	ConstraintNone ConstraintKind = iota
	ConstraintOneOf
	ConstraintSameAs
)

type Constraint struct {
	Kind       ConstraintKind
	Allowed    []Type
	OtherParam int
}

type Param struct {
	Name        string
	Default     *Type
	Constraints []Constraint
}

type Definition struct {
	Name   string
	Params []Param
	Body   func(nested Nested, args []Type) (Type, error)
}

type Nested interface {
	Instantiate(def string, args ...Type) (Type, error)
}

type Instance struct {
	ID           uint64
	Def          string
	Args         []Type
	Result       Type
	Dependencies []string
	Dependents   []string
	Stale        bool
	StaleReason  *StaleReason
	CreatedAt    time.Time
}

type StaleReason struct {
	UpdatedDef string
	Revision   uint64
	Hop        int
	UpdatedAt  time.Time
}

type Result struct {
	Instance *Instance
	Hit      bool
}

type Summary struct {
	ActiveInstances   int
	StaleInstances    int
	InstancesByDef    map[string]int
	CumulativeHits    uint64
	CumulativeCreates uint64
	Revision          uint64
}

func Named(name string) Type {
	return Type{Kind: KindNamed, Name: name}
}

func Alias(name string, target Type) Type {
	return Type{Kind: KindAlias, Name: name, AliasTo: &target}
}

func Struct(fields ...Field) Type {
	return Type{Kind: KindStruct, Fields: fields}
}

func InstanceOf(def string, args ...Type) Type {
	return Type{Kind: KindInstance, Def: def, Args: args}
}

func Ptr(t Type) *Type {
	return &t
}

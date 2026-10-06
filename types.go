package ontology

type Field struct {
	Name    string
	Type    string
	Compact bool
	Pointer bool
}

type TypeSpec struct {
	Name      string
	Basic     bool
	Size      int
	Alignment int
	MaxAlign  int
	Fields    []Field
}

type FieldLayout struct {
	Name    string
	Type    string
	Offset  int
	Size    int
	Align   int
	Compact bool
	Pointer bool
}

type Layout struct {
	Name    string
	Size    int
	Align   int
	Version int
	Fields  []FieldLayout
}

type View struct {
	Name           string
	Version        int
	Size           int
	Align          int
	Dependents     int
	Recalculations int
}

type ChangeResult struct {
	RootCompatibility Compatibility
	Compatibility     map[string]Compatibility
	Recalculated      []string
	OldLayout         *Layout
	NewLayout         *Layout
}

type typeRecord struct {
	spec           TypeSpec
	layout         Layout
	directDeps     map[string]bool
	dependents     map[string]bool
	version        int
	recalculations int
}

func cloneSpec(spec TypeSpec) TypeSpec {
	clone := spec
	clone.Fields = append([]Field(nil), spec.Fields...)
	return clone
}

func cloneLayout(layout Layout) Layout {
	clone := layout
	clone.Fields = append([]FieldLayout(nil), layout.Fields...)
	return clone
}

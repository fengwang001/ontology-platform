package evolution

// Op kinds for ChangeOp.
const (
	OpAdd    = "add"
	OpUpdate = "update"
	OpDelete = "delete"
)

// ColumnType is the stored type of a column.
type ColumnType string

const (
	TypeInt    ColumnType = "int"
	TypeString ColumnType = "string"
)

// ColumnDef describes one column in a schema version.
type ColumnDef struct {
	Name     string
	Type     ColumnType
	Required bool
}

// Schema is one immutable, complete registered version.
type Schema struct {
	Version int
	Columns []ColumnDef
}

// validateColumnSet rejects illegal types, empty names and duplicate names.
func validateColumnSet(cols []ColumnDef) error {
	seen := make(map[string]struct{}, len(cols))
	for _, col := range cols {
		if col.Name == "" {
			return reject(ReasonEmptyColumnName, "column name must not be empty (version schema)")
		}
		if !col.Type.valid() {
			return reject(ReasonInvalidType, "column %q has unknown type %q", col.Name, col.Type)
		}
		if _, dup := seen[col.Name]; dup {
			return reject(ReasonDuplicateColumn, "column %q is declared more than once", col.Name)
		}
		seen[col.Name] = struct{}{}
	}
	return nil
}

func (t ColumnType) valid() bool {
	return t == TypeInt || t == TypeString
}

// zeroValue returns the deterministic zero value for a column type.
func (t ColumnType) zeroValue() any {
	switch t {
	case TypeInt:
		return int64(0)
	case TypeString:
		return ""
	default:
		return nil
	}
}

func (s Schema) columnIndex() map[string]int {
	idx := make(map[string]int, len(s.Columns))
	for i, col := range s.Columns {
		idx[col.Name] = i
	}
	return idx
}

// cloneColumns returns a defensive copy so callers cannot mutate a schema.
func cloneColumns(cols []ColumnDef) []ColumnDef {
	out := make([]ColumnDef, len(cols))
	copy(out, cols)
	return out
}

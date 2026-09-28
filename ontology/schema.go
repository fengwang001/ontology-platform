package ontology

// ColumnID 是列的稳定标识。一经分配永不复用：删除列后再新增同名列，
// 新列会拿到全新的 ID。
type ColumnID int64

// Column 描述某一版本结构中的一列。
type Column struct {
	// ID 是稳定标识，解码时只按它取值，不按名字或位置。
	ID ColumnID
	// Name 是当前名字，可随改名操作变化。
	Name string
	// Default 是当前结构中的默认值；事件缺少该列时使用。
	// 空串是合法的默认值，也与“缺列”语义不同。
	Default string
}

// ColumnSpec 是初始化建表时对一列的声明（ID 由注册表分配）。
type ColumnSpec struct {
	Name    string
	Default string
}

// Schema 是某一版本的不可变列结构快照。发布后其底层切片不再被任何路径
// 修改，因此可被并发读取而无需加锁。
type Schema struct {
	version int
	columns []Column
}

// newSchema 构造快照；调用方必须确保 columns 之后不再被外部修改。
func newSchema(version int, columns []Column) Schema {
	return Schema{version: version, columns: columns}
}

// Version 返回该结构的版本号（初始结构为 1）。
func (s Schema) Version() int { return s.version }

// Columns 返回该版本的列副本，顺序即该版本的位置顺序。
// 返回副本，调用方无法透过它篡改不可变快照。
func (s Schema) Columns() []Column {
	if len(s.columns) == 0 {
		return nil
	}
	out := make([]Column, len(s.columns))
	copy(out, s.columns)
	return out
}

// columnAt 按位置返回列（包内使用，事件值即按位置对齐）。
func (s Schema) columnAt(pos int) (Column, bool) {
	if pos < 0 || pos >= len(s.columns) {
		return Column{}, false
	}
	return s.columns[pos], true
}

// columnByID 按稳定标识返回列。
func (s Schema) columnByID(id ColumnID) (Column, bool) {
	for _, c := range s.columns {
		if c.ID == id {
			return c, true
		}
	}
	return Column{}, false
}

// indexByID 返回列在当前结构中的位置，不存在时返回 -1。
func (s Schema) indexByID(id ColumnID) int {
	for i, c := range s.columns {
		if c.ID == id {
			return i
		}
	}
	return -1
}

// width 返回列数。
func (s Schema) width() int { return len(s.columns) }

// cloneColumns 返回列切片的独立副本，供演进派生新版本使用。
func (s Schema) cloneColumns() []Column {
	out := make([]Column, len(s.columns))
	copy(out, s.columns)
	return out
}

// Cell 是解码结果行中的一个单元。
type Cell struct {
	// ID 是列的稳定标识。
	ID ColumnID
	// Name 是当前（解码目标）结构中的列名。
	Name string
	// Value 是解码得到的值。
	Value string
	// FromDefault 为 true 表示该值取自当前结构默认值而非事件载荷。
	FromDefault bool
}

// Row 是一次解码的完整结果，完整基于某一版本的最新结构。
// 其底层切片同样不可变，可安全并发使用。
type Row struct {
	// SchemaVersion 是本次解码所基于的结构版本号。
	SchemaVersion int
	cells         []Cell
}

// newRow 构造解码结果行；调用方必须确保 cells 之后不再被修改。
func newRow(schemaVersion int, cells []Cell) Row {
	return Row{SchemaVersion: schemaVersion, cells: cells}
}

// Cells 返回按当前结构列顺序排列的单元副本。
func (r Row) Cells() []Cell {
	if len(r.cells) == 0 {
		return nil
	}
	out := make([]Cell, len(r.cells))
	copy(out, r.cells)
	return out
}

// Value 按列的稳定标识取值。第二个返回值表示该列是否存在于当前结构中；
// 已删除的列在结果行中没有对应单元，返回 ("", false)。
func (r Row) Value(id ColumnID) (string, bool) {
	for _, c := range r.cells {
		if c.ID == id {
			return c.Value, true
		}
	}
	return "", false
}

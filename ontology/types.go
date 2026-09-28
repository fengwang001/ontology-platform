// Package ontology 实现表结构（Schema）按版本演进，并能按列的稳定标识
// 把任意历史版本的事件解码为当前最新结构下的一行。
package ontology

// ColumnID 是列的稳定标识。一经分配，永不复用：删除列后再新增同名列，
// 会得到一个全新的 ColumnID。
type ColumnID uint64

// ColumnSpec 是建表 / 新增列时由调用方提供的列描述。
type ColumnSpec struct {
	// Name 为列名。同一版本内列名唯一；改名只影响 Name，不影响 ID。
	Name string
	// Default 为该列的当前默认值。事件中缺少该列时使用。
	// 空串是合法的默认值，也与“缺列”语义不同。
	Default string
}

// ColumnDef 是某一版本结构中一列的完整定义。
type ColumnDef struct {
	ID      ColumnID
	Name    string
	Default string
}

// Schema 是某一个版本的完整表结构。
type Schema struct {
	// Version 为从 1 开始、单调递增的版本号。
	Version int
	// Columns 为该版本的列，按稳定顺序排列（新增一律追加在末尾）。
	Columns []ColumnDef
	// index 为 ID -> Columns 下标的不可变索引，构建快照时一次性生成。
	index map[ColumnID]int
}

// Column 按稳定标识返回该版本中的列定义；不存在时 ok 为 false。
func (s *Schema) Column(id ColumnID) (col ColumnDef, ok bool) {
	if s == nil || s.index == nil {
		return ColumnDef{}, false
	}
	i, ok := s.index[id]
	if !ok {
		return ColumnDef{}, false
	}
	return s.Columns[i], true
}

// Has 报告该版本是否包含指定标识的列。
func (s *Schema) Has(id ColumnID) bool {
	_, ok := s.Column(id)
	return ok
}

// ChangeKind 标识一次演进操作的种类。
type ChangeKind int

const (
	// ChangeUnknown 为零值，属于非法操作。
	ChangeUnknown ChangeKind = iota
	// ChangeAdd 在末尾新增一列（Name / Default 有效）。
	ChangeAdd
	// ChangeDrop 删除 ID 指定的列（ID 有效）。
	ChangeDrop
	// ChangeRename 修改 ID 指定列的名字（ID / Name 有效）。
	ChangeRename
)

// Change 描述一次演进操作。多个 Change 可在一次 Evolve 中原子生效，
// 共同形成下一个版本。
type Change struct {
	Kind    ChangeKind
	ID      ColumnID // ChangeDrop / ChangeRename 时使用
	Name    string   // ChangeAdd 的新列名 / ChangeRename 的新名字
	Default string   // ChangeAdd 时使用
}

// Event 是某一历史版本下产生的一行事件。
type Event struct {
	// Version 为事件写入时的结构版本号。
	Version int
	// Values 按该版本列的顺序存放各列的值；个数必须与该版本列数一致。
	// 空串是合法值，不等于缺列。
	Values []string
}

// ValueSource 标识解码后某一列取值的来源（判定依据）。
type ValueSource int

const (
	// SourceUnknown 为零值占位。
	SourceUnknown ValueSource = iota
	// SourceEvent 表示该值取自事件本身（含空串这种合法值）。
	SourceEvent
	// SourceDefault 表示事件所在版本没有该列（列在事件之后才新增），
	// 取当前最新结构中的默认值。
	SourceDefault
)

// Row 是一行解码结果，始终对齐当前最新结构。
type Row struct {
	// SchemaVersion 为本次解码所基于的最新结构版本号。
	SchemaVersion int
	// Order 为当前最新结构的列顺序。
	Order []ColumnID
	// schema 指向解码时使用的最新结构不可变快照，用于还原列定义。
	schema *Schema
	// values 以稳定标识为键保存结果值（含空串）。
	values map[ColumnID]string
	// sources 记录每列取值来自事件还是默认值。
	sources map[ColumnID]ValueSource
}

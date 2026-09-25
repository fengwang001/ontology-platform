// Package col 定义源列模式、单行值的存取以及单个输出列的求值。
// 它不依赖本工程其他任何包。
package col

import "errors"

// 源列模式非法：列名为空或重复（哨兵错误，可 errors.Is 判定）。
var ErrInvalidSchema = errors.New("col: invalid source schema (empty or duplicate column)")

// 变更行非法：缺源列或出现未知列（哨兵错误）。
var ErrInvalidRow = errors.New("col: invalid change row (missing or unknown column)")

// Schema 是有序的源列模式；所有源列均为 int。
type Schema struct {
	names []string
	pos   map[string]int
}

// New 校验并构造源列模式：列名必须非空且互不重复。
func New(names []string) (*Schema, error) {
	if len(names) == 0 {
		return nil, ErrInvalidSchema
	}
	pos := make(map[string]int, len(names))
	for i, n := range names {
		if n == "" {
			return nil, ErrInvalidSchema
		}
		if _, ok := pos[n]; ok {
			return nil, ErrInvalidSchema
		}
		pos[n] = i
	}
	return &Schema{names: append([]string(nil), names...), pos: pos}, nil
}

// Names 按定义顺序返回源列名副本。
func (s *Schema) Names() []string { return append([]string(nil), s.names...) }

// Has 报告 name 是否为已知源列。
func (s *Schema) Has(name string) bool {
	_, ok := s.pos[name]
	return ok
}

// CheckRow 校验一个完整变更行：每个源列都必须给出值，且不得出现未知列。
func (s *Schema) CheckRow(row map[string]int) error {
	if len(row) != len(s.names) {
		return ErrInvalidRow
	}
	for _, n := range s.names {
		if _, ok := row[n]; !ok {
			return ErrInvalidRow
		}
	}
	return nil
}

// Row 是经过列裁剪后的单行：求值器只能读到裁剪后保留下来的源列，
// 被裁掉的源列根本不进入此结构，因此无从被读取。
type Row struct {
	v map[string]int
}

// NewRow 用一组已裁剪的源列值构造单行。
func NewRow(v map[string]int) *Row { return &Row{v: v} }

// Get 读取一个（已保留的）源列值。
func (r *Row) Get(name string) int { return r.v[name] }

// Eval 对单个输出列就本行求值：值等于 refs 中各源列之和。
// refs 只有一个元素时即「直接取列/别名」；多个即聚合求和；空列表为常量 0。
func Eval(r *Row, refs []string) int {
	sum := 0
	for _, c := range refs {
		sum += r.Get(c)
	}
	return sum
}

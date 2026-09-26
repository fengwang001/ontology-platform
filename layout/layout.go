// Package layout 描述定长记录的字段布局：宽度、偏移、符号性。
// 字段依次紧密排布（无填充），偏移为前面所有字段宽度之和。
package layout

import (
	"errors"
	"fmt"
	"sync/atomic"
)

// ErrBadWidth 字段宽度不是 1/2/4/8 之一。
var ErrBadWidth = errors.New("layout: width must be 1, 2, 4 or 8")

// ErrDupField 字段名重复。
var ErrDupField = errors.New("layout: duplicate field name")

// Field 单个字段的布局。
type Field struct {
	Name   string
	Offset int
	Width  int
	Signed bool
}

// Spec 构建 Schema 时的字段描述（偏移由宽度序列推导）。
type Spec struct {
	Name   string
	Width  int
	Signed bool
}

// Schema 一张固定布局表，构建后只读，可并发使用。
type Schema struct {
	fields []Field
	byName map[string]Field
	total  int
	lastN  atomic.Int64 // 最近一次 Field() 查找比较的字段个数（非导出，仅包内测试可读）
}

// NewSchema 由宽度序列推导偏移并构建 Schema。
func NewSchema(specs []Spec) (*Schema, error) {
	if len(specs) == 0 {
		return nil, fmt.Errorf("layout: %w", errors.New("empty schema"))
	}
	s := &Schema{byName: make(map[string]Field, len(specs))}
	off := 0
	for _, sp := range specs {
		switch sp.Width {
		case 1, 2, 4, 8:
		default:
			return nil, fmt.Errorf("layout: field %q: %w", sp.Name, ErrBadWidth)
		}
		if _, dup := s.byName[sp.Name]; dup {
			return nil, fmt.Errorf("layout: field %q: %w", sp.Name, ErrDupField)
		}
		f := Field{Name: sp.Name, Offset: off, Width: sp.Width, Signed: sp.Signed}
		s.fields = append(s.fields, f)
		s.byName[sp.Name] = f
		off += sp.Width
	}
	s.total = off
	return s, nil
}

// Field 按名查字段，O(1) 哈希定位。
func (s *Schema) Field(name string) (Field, bool) {
	f, ok := s.byName[name]
	s.lastN.Store(1) // 一次哈希定位，与字段总数无关
	return f, ok
}

// Total 记录总长度（所有字段宽度之和）。
func (s *Schema) Total() int { return s.total }

// Fields 返回全部字段的副本（按布局顺序）。
func (s *Schema) Fields() []Field {
	out := make([]Field, len(s.fields))
	copy(out, s.fields)
	return out
}

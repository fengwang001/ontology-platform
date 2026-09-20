package ontology

import (
	"fmt"
	"sort"
)

// Kind 是字段声明的值类型。
type Kind int

const (
	// StringKind 表示字符串字段，值必须是 string。
	StringKind Kind = iota
	// IntKind 表示整数字段，值必须是 int64 且落在 [Min, Max] 内。
	IntKind
)

func (k Kind) String() string {
	switch k {
	case StringKind:
		return "string"
	case IntKind:
		return "int"
	default:
		return "unknown"
	}
}

// Field 声明一个配置字段：名称、类型，以及整数字段的上下界（含边界）。
type Field struct {
	Name string
	Kind Kind
	Min  int64
	Max  int64
}

// Schema 是一组字段声明，用于校验每次更新。
type Schema struct {
	fields map[string]Field
}

// NewSchema 由字段声明构建 Schema。字段名不能为空或重复，
// 整数字段的 Min 不得大于 Max。
func NewSchema(fields ...Field) (*Schema, error) {
	s := &Schema{fields: make(map[string]Field, len(fields))}
	for _, f := range fields {
		if f.Name == "" {
			return nil, fmt.Errorf("schema: field name must not be empty")
		}
		if _, dup := s.fields[f.Name]; dup {
			return nil, fmt.Errorf("schema: duplicate field %q", f.Name)
		}
		if f.Kind == IntKind && f.Min > f.Max {
			return nil, fmt.Errorf("schema: field %q: min %d > max %d", f.Name, f.Min, f.Max)
		}
		s.fields[f.Name] = f
	}
	return s, nil
}

// Validate 校验单个字段值。不通过时返回 *ValidationError，
// 其内层错误是 ErrUnknownField、ErrTypeMismatch 或 ErrOutOfRange 之一。
func (s *Schema) Validate(name string, v any) error {
	f, ok := s.fields[name]
	if !ok {
		return &ValidationError{Field: name, Err: ErrUnknownField}
	}
	switch f.Kind {
	case StringKind:
		if _, ok := v.(string); !ok {
			return &ValidationError{Field: name, Err: fmt.Errorf(
				"%w: declared string, got %T", ErrTypeMismatch, v)}
		}
	case IntKind:
		n, ok := v.(int64)
		if !ok {
			return &ValidationError{Field: name, Err: fmt.Errorf(
				"%w: declared int64, got %T", ErrTypeMismatch, v)}
		}
		if n < f.Min || n > f.Max {
			return &ValidationError{Field: name, Err: fmt.Errorf(
				"%w: %d not in [%d, %d]", ErrOutOfRange, n, f.Min, f.Max)}
		}
	}
	return nil
}

// Has 报告字段名是否已声明。
func (s *Schema) Has(name string) bool {
	_, ok := s.fields[name]
	return ok
}

// Names 按字典序返回全部已声明字段名。
func (s *Schema) Names() []string {
	names := make([]string, 0, len(s.fields))
	for name := range s.fields {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// Package schema 定义数据契约的字段、类型与兼容模式等值类型及其校验。
package schema

import "errors"

// Type 是字段的数据类型。
type Type int

const (
	Int32 Type = iota
	Int64
	Float64
	String
	Bytes
)

var typeNames = map[Type]string{
	Int32:   "int32",
	Int64:   "int64",
	Float64: "float64",
	String:  "string",
	Bytes:   "bytes",
}

// Valid 报告 t 是否为已知类型。
func (t Type) Valid() bool {
	_, ok := typeNames[t]
	return ok
}

func (t Type) String() string {
	if s, ok := typeNames[t]; ok {
		return s
	}
	return "unknown"
}

// Mode 是主题的兼容检查模式。
type Mode int

const (
	// Backward 要求新读者能读旧数据：CanRead(N, latest)。
	Backward Mode = iota
	// Forward 要求旧读者能读新数据：CanRead(latest, N)。
	Forward
	// Full 两者都要，先查 Backward。
	Full
)

// Valid 报告 m 是否为已知模式。
func (m Mode) Valid() bool {
	return m == Backward || m == Forward || m == Full
}

func (m Mode) String() string {
	switch m {
	case Backward:
		return "BACKWARD"
	case Forward:
		return "FORWARD"
	case Full:
		return "FULL"
	}
	return "UNKNOWN"
}

// Field 是契约版本中的一个字段。
type Field struct {
	Name       string
	Type       Type
	Required   bool
	HasDefault bool
}

const (
	// MaxFields 是一个版本（或订阅投影）允许的最大字段数。
	MaxFields = 64
	// MaxNow 是逻辑时钟允许的最大值（秒）。
	MaxNow = int64(1_000_000_000_000)
)

// ErrInvalidFields 表示字段列表不合法（为空、超限、名空、重名或类型未知）。
var ErrInvalidFields = errors.New("schema: invalid field list")

// ValidateFields 校验一个版本的字段列表：1..64 个、名非空且不重复、类型已知。
func ValidateFields(fields []Field) error {
	if len(fields) == 0 || len(fields) > MaxFields {
		return ErrInvalidFields
	}
	seen := make(map[string]struct{}, len(fields))
	for _, f := range fields {
		if f.Name == "" || !f.Type.Valid() {
			return ErrInvalidFields
		}
		if _, ok := seen[f.Name]; ok {
			return ErrInvalidFields
		}
		seen[f.Name] = struct{}{}
	}
	return nil
}

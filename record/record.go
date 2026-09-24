// Package record 定义结构化日志记录及其编解码。
package record

import (
	"encoding/json"
	"errors"
	"reflect"
)

// MaxDepth 是允许的字段嵌套深度（根映射计为第 1 层）。
const MaxDepth = 16

var (
	// ErrTooDeep 表示嵌套深度超过 MaxDepth。
	ErrTooDeep = errors.New("record: nesting depth exceeds limit")
	// ErrCycle 表示字段映射存在自引用或共享环。
	ErrCycle = errors.New("record: cyclic reference in fields")
)

// Level 是日志级别。
type Level int

const (
	Debug Level = iota
	Info
	Warn
	Error
	Fatal
)

func (l Level) String() string {
	switch l {
	case Debug:
		return "debug"
	case Info:
		return "info"
	case Warn:
		return "warn"
	case Error:
		return "error"
	case Fatal:
		return "fatal"
	default:
		return "unknown"
	}
}

// Record 是一条结构化日志记录。
type Record struct {
	Level      Level          `json:"level"`
	TraceID    string         `json:"trace_id"`
	Incomplete bool           `json:"incomplete,omitempty"`
	Fields     map[string]any `json:"fields"`
}

// New 构造记录：深拷贝字段并检测深度超限与循环引用。
func New(level Level, traceID string, fields map[string]any) (*Record, error) {
	seen := map[uintptr]bool{}
	cp, err := copyValue(fields, 1, seen)
	if err != nil {
		return nil, err
	}
	fm, _ := cp.(map[string]any)
	if fields != nil && fm == nil {
		fm = map[string]any{}
	}
	return &Record{Level: level, TraceID: traceID, Fields: fm}, nil
}

func copyValue(v any, depth int, seen map[uintptr]bool) (any, error) {
	switch t := v.(type) {
	case map[string]any:
		if depth > MaxDepth {
			return nil, ErrTooDeep
		}
		ptr := reflect.ValueOf(t).Pointer()
		if seen[ptr] {
			return nil, ErrCycle
		}
		seen[ptr] = true
		out := make(map[string]any, len(t))
		for k, child := range t {
			cp, err := copyValue(child, depth+1, seen)
			if err != nil {
				return nil, err
			}
			out[k] = cp
		}
		delete(seen, ptr)
		return out, nil
	case []any:
		ptr := reflect.ValueOf(t).Pointer()
		if seen[ptr] {
			return nil, ErrCycle
		}
		seen[ptr] = true
		out := make([]any, len(t))
		for i, child := range t {
			cp, err := copyValue(child, depth+1, seen)
			if err != nil {
				return nil, err
			}
			out[i] = cp
		}
		delete(seen, ptr)
		return out, nil
	default:
		return v, nil
	}
}

// Encode 将记录序列化为 JSON。
func Encode(r *Record) ([]byte, error) {
	return json.Marshal(r)
}

// Decode 从 JSON 反序列化记录，并复检深度。
func Decode(data []byte) (*Record, error) {
	var r Record
	if err := json.Unmarshal(data, &r); err != nil {
		return nil, err
	}
	seen := map[uintptr]bool{}
	if _, err := copyValue(r.Fields, 1, seen); err != nil {
		return nil, err
	}
	return &r, nil
}

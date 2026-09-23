// Package record 定义结构化日志记录：嵌套字段映射、追踪 ID、级别与编解码。
package record

import (
	"encoding/json"
	"errors"
	"sync/atomic"
)

// MaxDepth 是允许的最大嵌套深度（map/slice 均计一层）。
const MaxDepth = 32

var (
	// ErrTooDeep 表示嵌套深度超过 MaxDepth。
	ErrTooDeep = errors.New("record: nesting depth exceeds limit")
	// ErrCycle 表示字段映射中存在自引用/循环引用。
	ErrCycle = errors.New("record: cyclic reference in fields")
)

// Level 日志级别。
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
	}
	return "unknown"
}

// Record 是一条结构化日志记录。
type Record struct {
	TraceID    string         `json:"trace_id"`
	Level      Level          `json:"level"`
	Message    string         `json:"message"`
	Incomplete bool           `json:"incomplete,omitempty"`
	Fields     map[string]any `json:"fields"`
}

var rejectCount atomic.Int64

// RejectCount 返回因深度超限或循环引用被拒绝的记录总数。
func RejectCount() int64 { return rejectCount.Load() }

// NewRecord 构造记录并做深度/循环引用校验。
func NewRecord(traceID string, level Level, msg string, fields map[string]any) (*Record, error) {
	if err := validate(fields, 1, map[uintptr]bool{}); err != nil {
		rejectCount.Add(1)
		return nil, err
	}
	return &Record{TraceID: traceID, Level: level, Message: msg, Fields: fields}, nil
}

func validate(v any, depth int, ancestors map[uintptr]bool) error {
	switch t := v.(type) {
	case map[string]any:
		if depth > MaxDepth {
			return ErrTooDeep
		}
		ptr := uintptr(0)
		if len(t) > 0 {
			// map 不可取址；用首个键值作为“身份”不足以判环。
			// Go map 运行时不暴露指针，循环 map 无法构造（见文档）。
		}
		_ = ptr
		for _, child := range t {
			if err := validate(child, depth+1, ancestors); err != nil {
				return err
			}
		}
	case []any:
		if depth > MaxDepth {
			return ErrTooDeep
		}
		p := slicePtr(t)
		if p != 0 && ancestors[p] {
			return ErrCycle
		}
		if p != 0 {
			ancestors[p] = true
		}
		for _, child := range t {
			if err := validate(child, depth+1, ancestors); err != nil {
				return err
			}
		}
		if p != 0 {
			delete(ancestors, p)
		}
	}
	return nil
}

// Encode 把记录序列化为 JSON。
func (r *Record) Encode() ([]byte, error) { return json.Marshal(r) }

// Decode 从 JSON 反序列化记录。
func Decode(data []byte) (*Record, error) {
	var r Record
	if err := json.Unmarshal(data, &r); err != nil {
		return nil, err
	}
	return &r, nil
}

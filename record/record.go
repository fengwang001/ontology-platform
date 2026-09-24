// Package record 定义结构化日志记录：嵌套字段、追踪 ID、级别，以及校验与编解码。
package record

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sync/atomic"
)

const MaxDepth = 16

var (
	ErrTooDeep = errors.New("record: nesting depth exceeds limit")
	ErrCycle   = errors.New("record: cyclic reference in fields")
	ErrValue   = errors.New("record: unsupported field value")
)

// Counters 记录被拒绝的记录数（并发安全）。
var Counters struct {
	TooDeep atomic.Int64
	Cycle   atomic.Int64
}

type Level int

const (
	Debug Level = iota
	Info
	Warn
	Error
	Fatal
)

// Record 是一条结构化日志。Fields 的容器类型为 map[string]any 或 []any，
// 叶值限 string/float64/int64/bool/nil（JSON 解码的自然结果）。
type Record struct {
	Level           Level          `json:"level"`
	TraceID         string         `json:"trace_id"`
	Fields          map[string]any `json:"fields"`
	IncompleteChain bool           `json:"incomplete_chain,omitempty"`
}

// New 构造记录并执行深度/循环引用/值类型校验。
func New(level Level, traceID string, fields map[string]any) (*Record, error) {
	onPath := map[uintptr]struct{}{}
	if err := validate(fields, 1, onPath); err != nil {
		if errors.Is(err, ErrTooDeep) {
			Counters.TooDeep.Add(1)
		}
		if errors.Is(err, ErrCycle) {
			Counters.Cycle.Add(1)
		}
		return nil, err
	}
	return &Record{Level: level, TraceID: traceID, Fields: fields}, nil
}

func validate(v any, depth int, onPath map[uintptr]struct{}) error {
	switch t := v.(type) {
	case map[string]any:
		if depth > MaxDepth {
			return ErrTooDeep
		}
		ptr := reflect.ValueOf(t).Pointer()
		if _, seen := onPath[ptr]; seen {
			return ErrCycle
		}
		onPath[ptr] = struct{}{}
		defer delete(onPath, ptr)
		for _, child := range t {
			if err := validate(child, depth+1, onPath); err != nil {
				return err
			}
		}
	case []any:
		if depth > MaxDepth {
			return ErrTooDeep
		}
		for _, child := range t {
			if err := validate(child, depth+1, onPath); err != nil {
				return err
			}
		}
	case nil, string, int64, float64, bool:
	default:
		// 兼容任意整数/浮点类型，但拒绝函数、通道等不可表示值。
		switch reflect.TypeOf(v).Kind() {
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
			reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
			reflect.Float32, reflect.Float64:
		default:
			return fmt.Errorf("%w: %T", ErrValue, v)
		}
	}
	return nil
}

// Encode 以确定性 JSON（标准库对 map 按 key 排序）序列化。
func (r *Record) Encode() ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(r); err != nil {
		return nil, err
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}

// Decode 从 JSON 还原记录，并执行同样的结构校验。
func Decode(data []byte) (*Record, error) {
	var r Record
	if err := json.Unmarshal(data, &r); err != nil {
		return nil, err
	}
	if err := validate(r.Fields, 1, map[uintptr]struct{}{}); err != nil {
		return nil, err
	}
	return &r, nil
}

// Clone 深拷贝字段，脱敏在副本上进行以保证原记录不被改动。
func (r *Record) Clone() *Record {
	cp := *r
	cp.Fields = cloneValue(r.Fields).(map[string]any)
	return &cp
}

func cloneValue(v any) any {
	switch t := v.(type) {
	case map[string]any:
		m := make(map[string]any, len(t))
		for k, child := range t {
			m[k] = cloneValue(child)
		}
		return m
	case []any:
		s := make([]any, len(t))
		for i, child := range t {
			s[i] = cloneValue(child)
		}
		return s
	default:
		return v
	}
}

// Package record 定义结构化日志记录及其编解码与遍历。
package record

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"sync/atomic"
)

// 可判定错误。
var (
	ErrTooDeep = errors.New("record: nesting depth exceeds limit")
	ErrCycle   = errors.New("record: cyclic reference detected")
	ErrValue   = errors.New("record: unsupported field value type")
)

// MaxDepth 是允许的最大嵌套深度（根映射深度为 1）。
const MaxDepth = 8

// DeepRejects 统计因深度超限被拒绝的记录数。
var DeepRejects atomic.Int64

// Level 为日志级别。
type Level int

const (
	Debug Level = 10
	Info  Level = 20
	Warn  Level = 30
	Error Level = 40
)

// Fields 为嵌套字段映射。
type Fields map[string]any

// Record 是一条结构化日志记录。
type Record struct {
	TraceID string `json:"trace_id"`
	Level   Level  `json:"level"`
	Fields  Fields `json:"fields"`
}

// New 构造记录并执行深度与自引用校验。
func New(traceID string, level Level, f Fields) (*Record, error) {
	if err := validate(f, 1, map[any]int{}); err != nil {
		if errors.Is(err, ErrTooDeep) {
			DeepRejects.Add(1)
		}
		return nil, err
	}
	return &Record{TraceID: traceID, Level: level, Fields: f}, nil
}

func validate(v any, depth int, seen map[any]int) error {
	switch t := v.(type) {
	case nil, string, bool:
		return nil
	case int, int64, float64, float32, int32:
		return nil
	case Fields:
		return validateMap(t, depth, seen)
	case map[string]any:
		return validateMap(t, depth, seen)
	case []any:
		if seen[t] == 1 {
			return ErrCycle
		}
		if depth > MaxDepth {
			return ErrTooDeep
		}
		seen[t] = 1
		for _, e := range t {
			if err := validate(e, depth+1, seen); err != nil {
				return err
			}
		}
		seen[t] = 2
		return nil
	default:
		return fmt.Errorf("%w: %T", ErrValue, v)
	}
}

func validateMap(m map[string]any, depth int, seen map[any]int) error {
	if seen[m] == 1 {
		return ErrCycle
	}
	if depth > MaxDepth {
		return ErrTooDeep
	}
	seen[m] = 1
	for _, v := range m {
		if err := validate(v, depth+1, seen); err != nil {
			return err
		}
	}
	seen[m] = 2
	return nil
}

// Encode 返回规范化 JSON（键排序、无多余空白）。
func (r *Record) Encode() ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(r); err != nil {
		return nil, err
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}

// Decode 从 JSON 还原记录并重新校验。
func Decode(b []byte) (*Record, error) {
	var r Record
	if err := json.Unmarshal(b, &r); err != nil {
		return nil, err
	}
	if err := validate(r.Fields, 1, map[any]int{}); err != nil {
		return nil, err
	}
	return &r, nil
}

// StringValues 收集整棵字段树的全部字符串叶子值。
func (f Fields) StringValues() []string {
	var out []string
	collect(f, &out)
	return out
}

func collect(v any, out *[]string) {
	switch t := v.(type) {
	case string:
		*out = append(*out, t)
	case Fields:
		for _, c := range t {
			collect(c, out)
		}
	case map[string]any:
		for _, c := range t {
			collect(c, out)
		}
	case []any:
		for _, c := range t {
			collect(c, out)
		}
	}
}

// MutateStrings 以 fn 原地替换全部字符串叶子。
func (f Fields) MutateStrings(fn func(string) string) {
	mutateMap(f, fn)
}

func mutateMap(m map[string]any, fn func(string) string) {
	for k, v := range m {
		switch t := v.(type) {
		case string:
			m[k] = fn(t)
		case Fields:
			mutateMap(t, fn)
		case map[string]any:
			mutateMap(t, fn)
		case []any:
			mutateSlice(t, fn)
		}
	}
}

func mutateSlice(s []any, fn func(string) string) {
	for i, v := range s {
		switch t := v.(type) {
		case string:
			s[i] = fn(t)
		case Fields:
			mutateMap(t, fn)
		case map[string]any:
			mutateMap(t, fn)
		case []any:
			mutateSlice(t, fn)
		}
	}
}

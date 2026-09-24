// Package record 定义结构化日志记录及其确定性编解码。
package record

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
)

// MaxDepth 是允许的最大嵌套深度（根映射为第 1 层）。
const MaxDepth = 32

var (
	// ErrDepth 表示嵌套深度超过 MaxDepth。
	ErrDepth = errors.New("record: nesting depth exceeds limit")
	// ErrCycle 表示字段中存在自引用映射或切片。
	ErrCycle = errors.New("record: self-referential value detected")
)

// Level 是日志级别，数值越大越严重。
type Level int

const (
	LevelDebug Level = 10
	LevelInfo  Level = 20
	LevelWarn  Level = 30
	LevelError Level = 40
)

// Fields 是嵌套字段映射。
type Fields map[string]any

// Record 是一条结构化日志记录。Incomplete 表示链被采样丢弃但本条被强制保留。
type Record struct {
	Level      Level
	TraceID    string
	Fields     Fields
	Incomplete bool
}

type wireRecord struct {
	Level      int    `json:"level"`
	TraceID    string `json:"trace_id"`
	Incomplete bool   `json:"incomplete,omitempty"`
	Fields     Fields `json:"fields"`
}

// New 校验并构造记录：拒绝深度超限与自引用。
func New(level Level, traceID string, f Fields) (*Record, error) {
	if err := validate(reflect.ValueOf(f), 1, map[uintptr]bool{}); err != nil {
		return nil, err
	}
	return &Record{Level: level, TraceID: traceID, Fields: f}, nil
}

func validate(rv reflect.Value, depth int, seen map[uintptr]bool) error {
	if depth > MaxDepth {
		return ErrDepth
	}
	if !rv.IsValid() {
		return nil
	}
	switch rv.Kind() {
	case reflect.Interface:
		if rv.IsNil() {
			return nil
		}
		return validate(rv.Elem(), depth, seen)
	case reflect.Pointer:
		if rv.IsNil() {
			return nil
		}
		return validate(rv.Elem(), depth, seen)
	case reflect.String, reflect.Bool:
		return nil
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return nil
	case reflect.Float32, reflect.Float64:
		return nil
	case reflect.Map, reflect.Slice:
	default:
		return fmt.Errorf("record: unsupported value type %s", rv.Kind())
	}
	ptr := rv.Pointer()
	if seen[ptr] {
		return ErrCycle
	}
	seen[ptr] = true
	defer delete(seen, ptr)
	if rv.Kind() == reflect.Map {
		if rv.Type().Key().Kind() != reflect.String {
			return fmt.Errorf("record: map key must be string, got %s", rv.Type().Key().Kind())
		}
		iter := rv.MapRange()
		for iter.Next() {
			if err := validate(iter.Value(), depth+1, seen); err != nil {
				return err
			}
		}
		return nil
	}
	for i := 0; i < rv.Len(); i++ {
		if err := validate(rv.Index(i), depth+1, seen); err != nil {
			return err
		}
	}
	return nil
}

// Encode 以确定性 JSON 编码记录（编码/json 对 map 键按字典序输出）。
func (r *Record) Encode() ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(wireRecord{
		Level: int(r.Level), TraceID: r.TraceID,
		Incomplete: r.Incomplete, Fields: r.Fields,
	}); err != nil {
		return nil, err
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}

// Decode 从 JSON 还原记录。
func Decode(b []byte) (*Record, error) {
	var w wireRecord
	if err := json.Unmarshal(b, &w); err != nil {
		return nil, err
	}
	return &Record{
		Level: Level(w.Level), TraceID: w.TraceID,
		Incomplete: w.Incomplete, Fields: w.Fields,
	}, nil
}

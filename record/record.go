// Package record 定义结构化日志记录、校验与编解码。
package record

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
)

const MaxDepth = 32

const (
	LevelDebug = 10
	LevelInfo  = 20
	LevelWarn  = 30
	LevelError = 40
	LevelFatal = 50
)

var (
	// ErrDepthExceeded 嵌套深度超过 MaxDepth。
	ErrDepthExceeded = errors.New("record: nesting depth exceeded")
	// ErrCycle 字段映射中出现自引用（环）。
	ErrCycle = errors.New("record: cyclic reference detected")
)

// Record 是一条结构化日志：嵌套字段 + 追踪 ID + 级别。
type Record struct {
	Fields     map[string]any `json:"fields"`
	TraceID    string         `json:"trace_id"`
	Level      int            `json:"level"`
	Incomplete bool           `json:"incomplete,omitempty"`
}

// New 构造记录并做深度与循环引用校验。
func New(fields map[string]any, traceID string, level int) (*Record, error) {
	if err := Validate(fields); err != nil {
		return nil, err
	}
	return &Record{Fields: fields, TraceID: traceID, Level: level}, nil
}

type frame struct {
	rv   reflect.Value
	dep  int
	iter *reflect.MapIter
	anc  map[uintptr]bool
}

// Validate 以显式栈迭代，检测深度超限与映射自引用。
func Validate(v any) error {
	root := reflect.ValueOf(v)
	if root.Kind() != reflect.Map {
		return nil
	}
	st := []frame{{rv: root, dep: 1, anc: map[uintptr]bool{}}}
	for len(st) > 0 {
		f := &st[len(st)-1]
		if f.iter == nil {
			ptr := f.rv.Pointer()
			if f.anc[ptr] {
				return fmt.Errorf("%w: depth=%d", ErrCycle, f.dep)
			}
			f.anc[ptr] = true
			f.iter = f.rv.MapRange()
		}
		if !f.iter.Next() {
			st = st[:len(st)-1]
			continue
		}
		child := f.iter.Value()
		if child.Kind() == reflect.Interface {
			child = child.Elem()
		}
		if child.Kind() != reflect.Map {
			continue
		}
		if f.dep+1 > MaxDepth {
			return fmt.Errorf("%w: depth=%d", ErrDepthExceeded, f.dep+1)
		}
		next := make(map[uintptr]bool, len(f.anc)+1)
		for p := range f.anc {
			next[p] = true
		}
		st = append(st, frame{rv: child, dep: f.dep + 1, anc: next})
	}
	return nil
}

// Marshal 编码为 JSON 信封。
func (r *Record) Marshal() ([]byte, error) {
	if r.Fields == nil {
		r.Fields = map[string]any{}
	}
	return json.Marshal(r)
}

// Unmarshal 从 JSON 信封恢复记录，并重新校验深度。
func Unmarshal(data []byte) (*Record, error) {
	r := &Record{}
	if err := json.Unmarshal(data, r); err != nil {
		return nil, err
	}
	if r.Fields == nil {
		r.Fields = map[string]any{}
	}
	if err := Validate(r.Fields); err != nil {
		return nil, err
	}
	return r, nil
}

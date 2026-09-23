// Package record 定义结构化日志记录及其编解码。
package record

import (
	"bytes"
	"encoding/json"
	"errors"
	"reflect"
)

// MaxDepth 是允许的最大嵌套深度（根映射为第 1 层）。
const MaxDepth = 32

var (
	// ErrDepth 表示嵌套深度超过 MaxDepth。
	ErrDepth = errors.New("record: nesting depth exceeded")
	// ErrCycle 表示字段映射中存在自引用环。
	ErrCycle = errors.New("record: self-referential cycle detected")
)

// Level 是日志级别。
type Level int

const (
	LevelDebug Level = iota
	LevelInfo
	LevelWarn
	LevelError
)

// Record 是一条结构化日志记录。
type Record struct {
	Level   Level
	TraceID string
	Fields  map[string]any
}

type validator struct {
	stack map[uintptr]bool
}

// Validate 检查深度与自引用环。
func (r Record) Validate() error {
	v := &validator{stack: map[uintptr]bool{}}
	return v.walkMap(r.Fields, 1)
}

func (v *validator) walkMap(m map[string]any, depth int) error {
	if depth > MaxDepth {
		return ErrDepth
	}
	if m == nil {
		return nil
	}
	ptr := reflect.ValueOf(m).Pointer()
	if v.stack[ptr] {
		return ErrCycle
	}
	v.stack[ptr] = true
	defer delete(v.stack, ptr)
	for _, val := range m {
		if err := v.walk(val, depth); err != nil {
			return err
		}
	}
	return nil
}

func (v *validator) walk(val any, depth int) error {
	switch t := val.(type) {
	case map[string]any:
		return v.walkMap(t, depth+1)
	case []any:
		if depth+1 > MaxDepth {
			return ErrDepth
		}
		for _, e := range t {
			if err := v.walk(e, depth); err != nil {
				return err
			}
		}
	}
	return nil
}

type wire struct {
	Level   Level           `json:"level"`
	TraceID string          `json:"trace_id"`
	Fields  json.RawMessage `json:"fields"`
	// IncompleteChain 标记该记录是被采样丢弃链上的唯一幸存者。
	IncompleteChain bool `json:"incomplete_chain,omitempty"`
}

// Encode 校验并把记录序列化为 JSON（不含标记）。
func (r Record) Encode() ([]byte, error) {
	if err := r.Validate(); err != nil {
		return nil, err
	}
	fb, err := json.Marshal(r.Fields)
	if err != nil {
		return nil, err
	}
	return json.Marshal(wire{Level: r.Level, TraceID: r.TraceID, Fields: fb})
}

// EncodeMarked 序列化并附加链不完整标记。
func (r Record) EncodeMarked(incomplete bool) ([]byte, error) {
	if err := r.Validate(); err != nil {
		return nil, err
	}
	fb, err := json.Marshal(r.Fields)
	if err != nil {
		return nil, err
	}
	return json.Marshal(wire{
		Level: r.Level, TraceID: r.TraceID, Fields: fb,
		IncompleteChain: incomplete,
	})
}

// Decoded 是解码结果。
type Decoded struct {
	Record
	IncompleteChain bool
}

// Decode 解析 Encode/EncodeMarked 的产物。
func Decode(b []byte) (Decoded, error) {
	var w wire
	dec := json.NewDecoder(bytes.NewReader(b))
	if err := dec.Decode(&w); err != nil {
		return Decoded{}, err
	}
	var fields map[string]any
	if len(bytes.TrimSpace(w.Fields)) > 0 {
	if err := json.Unmarshal(w.Fields, &fields); err != nil {
			return Decoded{}, err
		}
	}
	d := Decoded{
		Record:          Record{Level: w.Level, TraceID: w.TraceID, Fields: fields},
		IncompleteChain: w.IncompleteChain,
	}
	return d, d.Validate()
}

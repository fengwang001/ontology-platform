// Package record 定义结构化日志记录：嵌套字段映射、追踪 ID、级别，
// 以及记录的校验（深度上限、循环引用）与 JSON 编解码。
package record

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sync/atomic"
)

// MaxDepth 是字段嵌套（映射/数组）的最大深度，顶层字段表深度为 1。
const MaxDepth = 32

var (
	// ErrDepth 表示嵌套深度超过 MaxDepth。
	ErrDepth = errors.New("record: nesting depth exceeds limit")
	// ErrCycle 表示字段映射存在循环引用。
	ErrCycle = errors.New("record: cyclic map reference")
)

var depthRejections atomic.Uint64

// DepthRejections 返回因深度超限被拒绝的记录总数。
func DepthRejections() uint64 { return depthRejections.Load() }

// Level 是日志级别，Error 及以上会被采样器强制保留。
type Level int

const (
	Debug Level = iota
	Info
	Warn
	Error
	Fatal
)

// ErrorOrAbove 报告级别是否为 error 及以上。
func (l Level) ErrorOrAbove() bool { return l >= Error }

// ChainIncompleteKey 是「链不完整」标记的保留字段名。
const ChainIncompleteKey = "_chain_incomplete"

// Record 是一条结构化日志记录。
type Record struct {
	TraceID string
	Level   Level
	Fields  map[string]any
}

// New 构造记录并校验字段：nil 字段表视为空表；
// 深度超限或存在循环引用时返回错误。
func New(traceID string, level Level, fields map[string]any) (*Record, error) {
	if fields == nil {
		fields = map[string]any{}
	}
	if err := validate(fields, 1, map[uintptr]bool{}); err != nil {
		if errors.Is(err, ErrDepth) {
			depthRejections.Add(1)
		}
		return nil, err
	}
	return &Record{TraceID: traceID, Level: level, Fields: fields}, nil
}

// validate 递归校验深度与循环引用；path 保存活跃路径上的映射指针。
func validate(v any, depth int, path map[uintptr]bool) error {
	if depth > MaxDepth {
		return fmt.Errorf("%w: depth %d", ErrDepth, depth)
	}
	switch t := v.(type) {
	case map[string]any:
		p := reflect.ValueOf(t).Pointer()
		if path[p] {
			return ErrCycle
		}
		path[p] = true
		defer delete(path, p)
		for _, e := range t {
			if err := validate(e, depth+1, path); err != nil {
				return err
			}
		}
	case []any:
		for _, e := range t {
			if err := validate(e, depth+1, path); err != nil {
				return err
			}
		}
	}
	return nil
}

// Encode 将记录序列化为 JSON（映射键有序，输出确定）。
func (r *Record) Encode() ([]byte, error) { return json.Marshal(r) }

// Decode 从 JSON 还原记录，并施加与 New 相同的校验。
func Decode(b []byte) (*Record, error) {
	var r Record
	if err := json.Unmarshal(b, &r); err != nil {
		return nil, err
	}
	if r.Fields == nil {
		r.Fields = map[string]any{}
	}
	if err := validate(r.Fields, 1, map[uintptr]bool{}); err != nil {
		return nil, err
	}
	return &r, nil
}

// Clone 返回记录的深拷贝。
func (r *Record) Clone() *Record {
	return &Record{TraceID: r.TraceID, Level: r.Level, Fields: cloneValue(r.Fields).(map[string]any)}
}

func cloneValue(v any) any {
	switch t := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, e := range t {
			out[k] = cloneValue(e)
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, e := range t {
			out[i] = cloneValue(e)
		}
		return out
	}
	return v
}

// MarkChainIncomplete 给记录打上「链不完整」标记。
func (r *Record) MarkChainIncomplete() { r.Fields[ChainIncompleteKey] = true }

// ChainIncomplete 报告记录是否带有「链不完整」标记。
func (r *Record) ChainIncomplete() bool {
	v, _ := r.Fields[ChainIncompleteKey].(bool)
	return v
}

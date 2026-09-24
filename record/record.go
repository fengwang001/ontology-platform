// Package record 定义结构化日志记录及其构造校验与 JSON 编解码。
package record

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sync/atomic"
)

// Level 是日志级别，Error 及以上会被采样器强制保留。
type Level int

const (
	LevelDebug Level = iota
	LevelInfo
	LevelWarn
	LevelError
	LevelFatal
)

// MaxDepth 是字段树允许的最大嵌套深度（根映射为深度 1）。
const MaxDepth = 32

var (
	// ErrDepth 表示嵌套深度超过 MaxDepth。
	ErrDepth = errors.New("record: nesting depth exceeds limit")
	// ErrCycle 表示字段树中存在自引用映射。
	ErrCycle = errors.New("record: cyclic field reference")
)

var depthRejections atomic.Int64

// DepthRejections 返回因深度超限被拒绝的构造次数。
func DepthRejections() int64 { return depthRejections.Load() }

// Record 是一条结构化日志记录。HasTrace=false 表示没有追踪 ID；
// HasTrace=true 且 TraceID=="" 表示追踪 ID 显式为空串，两者语义不同。
type Record struct {
	TraceID     string         `json:"trace_id,omitempty"`
	HasTrace    bool           `json:"has_trace"`
	Level       Level          `json:"level"`
	BrokenChain bool           `json:"broken_chain,omitempty"`
	Fields      map[string]any `json:"fields,omitempty"`
}

// New 构造记录并做深度与循环引用校验；校验失败返回哨兵错误并计数。
func New(level Level, traceID string, hasTrace bool, fields map[string]any) (*Record, error) {
	if err := validate(fields); err != nil {
		if errors.Is(err, ErrDepth) {
			depthRejections.Add(1)
		}
		return nil, err
	}
	return &Record{TraceID: traceID, HasTrace: hasTrace, Level: level, Fields: fields}, nil
}

type frame struct {
	value any
	depth int
	leave uintptr // 非零表示「离开该引用」的标记帧
}

// validate 迭代遍历字段树：深度受限、循环引用以「进入/离开」标记检出。
func validate(fields map[string]any) error {
	active := map[uintptr]bool{}
	stack := []frame{{fields, 1, 0}}
	for len(stack) > 0 {
		top := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if top.leave != 0 {
			delete(active, top.leave)
			continue
		}
		switch v := top.value.(type) {
		case map[string]any:
			ptr := reflect.ValueOf(v).Pointer()
			if active[ptr] {
				return ErrCycle
			}
			active[ptr] = true
			stack = append(stack, frame{nil, 0, ptr})
			if top.depth > MaxDepth {
				return fmt.Errorf("%w: depth=%d > %d", ErrDepth, top.depth, MaxDepth)
			}
			for _, child := range v {
				stack = append(stack, frame{child, top.depth + 1, 0})
			}
		case []any:
			ptr := reflect.ValueOf(v).Pointer()
			if active[ptr] {
				return ErrCycle
			}
			active[ptr] = true
			stack = append(stack, frame{nil, 0, ptr})
			if top.depth > MaxDepth {
				return fmt.Errorf("%w: depth=%d > %d", ErrDepth, top.depth, MaxDepth)
			}
			for _, child := range v {
				stack = append(stack, frame{child, top.depth + 1, 0})
			}
		}
	}
	return nil
}

// Encode 将记录序列化为 JSON 字节。
func (r *Record) Encode() ([]byte, error) { return json.Marshal(r) }

// Decode 反序列化记录，并对解码出的字段树做与 New 相同的校验。
func Decode(data []byte) (*Record, error) {
	r := &Record{}
	if err := json.Unmarshal(data, r); err != nil {
		return nil, err
	}
	if err := validate(r.Fields); err != nil {
		if errors.Is(err, ErrDepth) {
			depthRejections.Add(1)
		}
		return nil, err
	}
	return r, nil
}

// String 返回级别名。
func (l Level) String() string {
	switch l {
	case LevelDebug:
		return "debug"
	case LevelInfo:
		return "info"
	case LevelWarn:
		return "warn"
	case LevelError:
		return "error"
	case LevelFatal:
		return "fatal"
	default:
		return "unknown"
	}
}

// MarshalJSON 让级别以可读字符串落盘。
func (l Level) MarshalJSON() ([]byte, error) {
	return json.Marshal(l.String())
}

// UnmarshalJSON 同时接受级别名字符串（自身格式）与数字（兼容外部 JSON）。
func (l *Level) UnmarshalJSON(data []byte) error {
	var s string
	if err := json.Unmarshal(data, &s); err == nil {
		switch s {
		case "debug":
			*l = LevelDebug
		case "info":
			*l = LevelInfo
		case "warn":
			*l = LevelWarn
		case "error":
			*l = LevelError
		case "fatal":
			*l = LevelFatal
		default:
			return fmt.Errorf("record: unknown level %q", s)
		}
		return nil
	}
	var n int
	if err := json.Unmarshal(data, &n); err != nil {
		return err
	}
	*l = Level(n)
	return nil
}

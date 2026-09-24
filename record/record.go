// Package record defines the structured log record model plus validation,
// JSON codec and field walkers shared by the rule/mask/sample/sink packages.
package record

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
)

// MaxDepth bounds nesting depth so adversarial input cannot overflow the stack.
const MaxDepth = 32

var (
	// ErrDepthExceeded indicates nesting deeper than MaxDepth.
	ErrDepthExceeded = errors.New("record: nesting depth exceeded")
	// ErrCycle indicates a map reachable from itself on one descent path.
	ErrCycle = errors.New("record: self-referential map detected")
)

// Level is a log severity. Higher values are more severe.
type Level int

// Supported levels.
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

// Fields is a nested structured field mapping.
type Fields map[string]any

// Record is one structured log line.
type Record struct {
	Level           Level  `json:"level"`
	TraceID         string `json:"trace_id"`
	Fields          Fields `json:"fields"`
	ChainIncomplete bool   `json:"chain_incomplete,omitempty"`
}

// New validates fields (depth, cycles) and returns a record.
func New(level Level, traceID string, f Fields) (*Record, error) {
	if err := Validate(f); err != nil {
		return nil, err
	}
	return &Record{Level: level, TraceID: traceID, Fields: f}, nil
}

// Validate walks fields iteratively, tracking active map pointers per descent
// path. Sibling branches may legally share the same map value.
func Validate(f Fields) error {
	if f == nil {
		return nil
	}
	type node struct {
		v      any
		depth  int
		active map[uintptr]bool
	}
	stack := []node{{v: f, depth: 1, active: map[uintptr]bool{}}}
	for len(stack) > 0 {
		n := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if n.depth > MaxDepth {
			return fmt.Errorf("%w: >%d", ErrDepthExceeded, MaxDepth)
		}
		switch t := n.v.(type) {
		case map[string]any:
			ptr := mapPtr(t)
			if n.active[ptr] {
				return fmt.Errorf("%w: map reached from itself", ErrCycle)
			}
			next := cloneSet(n.active)
			next[ptr] = true
			for _, child := range t {
				stack = append(stack, node{v: child, depth: n.depth + 1, active: next})
			}
		case Fields:
			ptr := mapPtr(t)
			if n.active[ptr] {
				return fmt.Errorf("%w: map reached from itself", ErrCycle)
			}
			next := cloneSet(n.active)
			next[ptr] = true
			for _, child := range t {
				stack = append(stack, node{v: child, depth: n.depth + 1, active: next})
			}
		case []any:
			for _, child := range t {
				stack = append(stack, node{v: child, depth: n.depth + 1, active: n.active})
			}
		}
	}
	return nil
}

func mapPtr(m any) uintptr {
	return reflect.ValueOf(m).Pointer()
}

func cloneSet(s map[uintptr]bool) map[uintptr]bool {
	c := make(map[uintptr]bool, len(s)+1)
	for k := range s {
		c[k] = true
	}
	return c
}

// Encode returns canonical JSON (map keys sorted by encoding/json).
func (r *Record) Encode() ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(r); err != nil {
		return nil, err
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}

// Decode parses one JSON record.
func Decode(data []byte) (*Record, error) {
	var r Record
	if err := json.Unmarshal(data, &r); err != nil {
		return nil, err
	}
	if err := Validate(r.Fields); err != nil {
		return nil, err
	}
	return &r, nil
}

// Seg is one field-path segment: either a map key (Index<0) or array index.
type Seg struct {
	Key   string
	Index int
}

// StringVisitor receives every string leaf with its full segment path.
type StringVisitor func(path []Seg, value string)

// WalkStrings iterates every string leaf without recursion.
func WalkStrings(f Fields, visit StringVisitor) {
	type pos struct {
		v    any
		path []Seg
		d    int
	}
	stack := []pos{{v: f, d: 0}}
	for len(stack) > 0 {
		p := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if p.d > MaxDepth {
			return
		}
		switch t := p.v.(type) {
		case Fields:
			for k, child := range t {
				stack = append(stack, pos{child, append(append([]Seg{}, p.path...), Seg{Key: k, Index: -1}), p.d + 1})
			}
		case map[string]any:
			for k, child := range t {
				stack = append(stack, pos{child, append(append([]Seg{}, p.path...), Seg{Key: k, Index: -1}), p.d + 1})
			}
		case []any:
			for i, child := range t {
				stack = append(stack, pos{child, append(append([]Seg{}, p.path...), Seg{Index: i}), p.d + 1})
			}
		case string:
			visit(p.path, t)
		}
	}
}

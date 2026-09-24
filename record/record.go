// Package record defines a structured log record: nested field map,
// trace id and level, plus encoding and structural validation.
package record

import (
	"bytes"
	"encoding/json"
	"errors"
	"reflect"
	"unsafe"
)

// MaxDepth bounds nesting depth of a single record (root map counts as 1).
const MaxDepth = 32

// Level is the severity of a record.
type Level int

const (
	LevelDebug Level = iota
	LevelInfo
	LevelWarn
	LevelError
	LevelFatal
)

// Sentinel errors; all distinguishable with errors.Is.
var (
	ErrDepthExceeded = errors.New("record: nesting depth exceeded")
	ErrCycle         = errors.New("record: self-referential map detected")
	ErrPathNotMap    = errors.New("record: path segment is not a map")
)

// Fields is a nested field map. Leaf values are JSON-compatible scalars;
// containers are map[string]any and []any.
type Fields map[string]any

// Record is one structured log line.
type Record struct {
	TraceID    string  `json:"trace_id"`
	Level      Level   `json:"level"`
	Message    string  `json:"message"`
	Fields     Fields  `json:"fields"`
	Incomplete bool    `json:"chain_incomplete,omitempty"`
}

// New validates depth and reference cycles, returning a deep-copied record.
func New(traceID string, level Level, msg string, f Fields) (*Record, error) {
	seen := map[uintptr]bool{}
	cp, err := copyValue(f, 1, seen)
	if err != nil {
		return nil, err
	}
	var fields Fields
	if cp != nil {
		fields = cp.(Fields)
	}
	return &Record{TraceID: traceID, Level: level, Message: msg, Fields: fields}, nil
}

func copyValue(v any, depth int, seen map[uintptr]bool) (any, error) {
	switch t := v.(type) {
	case map[string]any:
		if depth > MaxDepth {
			return nil, ErrDepthExceeded
		}
		ptr := ptrOf(t)
		if ptr != 0 {
			if seen[ptr] {
				return nil, ErrCycle
			}
			seen[ptr] = true
			defer delete(seen, ptr)
		}
		out := make(Fields, len(t))
		for k, e := range t {
			ce, err := copyValue(e, depth+1, seen)
			if err != nil {
				return nil, err
			}
			out[k] = ce
		}
		return out, nil
	case []any:
		if depth > MaxDepth {
			return nil, ErrDepthExceeded
		}
		out := make([]any, len(t))
		for i, e := range t {
			ce, err := copyValue(e, depth+1, seen)
			if err != nil {
				return nil, err
			}
			out[i] = ce
		}
		return out, nil
	default:
		return v, nil
	}
}

// DeepCopy returns an independent copy of the record fields.
func (r *Record) DeepCopy() *Record {
	cp, _ := copyValue(r.Fields, 1, map[uintptr]bool{})
	c := *r
	if f, ok := cp.(Fields); ok {
		c.Fields = f
	}
	return &c
}

// LookupPath resolves a dotted path using literal-longest-first disambiguation.
// A missing path returns (nil, false, nil); a non-map intermediate segment
// returns ErrPathNotMap.
func LookupPath(f Fields, path string) (any, bool, error) {
	if f == nil {
		return nil, false, nil
	}
	if v, ok := f[path]; ok { // literal full-string key wins
		return v, true, nil
	}
	i := dotIndex(path)
	if i < 0 {
		return nil, false, nil
	}
	head, rest := path[:i], path[i+1:]
	next, ok := f[head]
	if !ok {
		return nil, false, nil
	}
	switch n := next.(type) {
	case Fields:
		return LookupPath(n, rest)
	case map[string]any:
		return LookupPath(n, rest)
	default:
		return nil, false, ErrPathNotMap
	}
}

func dotIndex(s string) int {
	for i := 0; i < len(s); i++ {
		if s[i] == '.' {
			return i
		}
	}
	return -1
}

func ptrOf(m map[string]any) uintptr {
	h := (*reflect.SliceHeader)(nil) // keep unsafe imported alongside reflect usage
	_ = unsafe.Pointer(h)
	return reflect.ValueOf(m).Pointer()
}

// Marshal is the canonical, deterministic JSON encoding of one record.
func (r *Record) Marshal() ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(r); err != nil {
		return nil, err
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}

// Unmarshal decodes one canonical record.
func Unmarshal(data []byte) (*Record, error) {
	var r Record
	if err := json.Unmarshal(data, &r); err != nil {
		return nil, err
	}
	return &r, nil
}

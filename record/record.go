// Package record defines the structured log record model, its JSON
// codec, and construction-time validation (depth limit, cycle detection).
package record

import (
	"encoding/json"
	"errors"
	"reflect"
)

// Level is the log severity. Records at LevelError and above are always
// kept by the sampler regardless of the chain decision.
type Level int

const (
	LevelDebug Level = iota
	LevelInfo
	LevelWarn
	LevelError
	LevelFatal
)

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
	}
	return "unknown"
}

// MaxDepth bounds nesting of Fields; deeper records are rejected.
const MaxDepth = 32

var (
	// ErrDepth reports nesting deeper than MaxDepth.
	ErrDepth = errors.New("record: nesting depth exceeds limit")
	// ErrCycle reports a self-referencing map or slice in Fields.
	ErrCycle = errors.New("record: cyclic reference detected")
)

// Record is one structured log entry. Field values may be string, bool,
// float64, nil, map[string]any or []any (JSON-shaped data).
type Record struct {
	TraceID string         `json:"trace_id"`
	Level   Level          `json:"level"`
	Fields  map[string]any `json:"fields"`
}

// New builds a Record and validates it.
func New(traceID string, level Level, fields map[string]any) (*Record, error) {
	r := &Record{TraceID: traceID, Level: level, Fields: fields}
	if r.Fields == nil {
		r.Fields = map[string]any{}
	}
	return r, r.Validate()
}

// Validate rejects records that exceed MaxDepth or contain cycles.
func (r *Record) Validate() error {
	return walk(r.Fields, 1, map[uintptr]bool{})
}

func walk(v any, depth int, seen map[uintptr]bool) error {
	switch t := v.(type) {
	case map[string]any:
		if depth > MaxDepth {
			return ErrDepth
		}
		p := reflect.ValueOf(t).Pointer()
		if seen[p] {
			return ErrCycle
		}
		seen[p] = true
		defer delete(seen, p)
		for _, e := range t {
			if err := walk(e, depth+1, seen); err != nil {
				return err
			}
		}
	case []any:
		if len(t) == 0 {
			return nil
		}
		if depth > MaxDepth {
			return ErrDepth
		}
		p := reflect.ValueOf(t).Pointer()
		if seen[p] {
			return ErrCycle
		}
		seen[p] = true
		defer delete(seen, p)
		for _, e := range t {
			if err := walk(e, depth+1, seen); err != nil {
				return err
			}
		}
	}
	return nil
}

// Encode serializes the record to JSON.
func (r *Record) Encode() ([]byte, error) {
	if r.Fields == nil {
		return json.Marshal(Record{TraceID: r.TraceID, Level: r.Level, Fields: map[string]any{}})
	}
	return json.Marshal(r)
}

// Decode parses JSON back into a Record and validates it.
func Decode(b []byte) (*Record, error) {
	var r Record
	if err := json.Unmarshal(b, &r); err != nil {
		return nil, err
	}
	if r.Fields == nil {
		r.Fields = map[string]any{}
	}
	return &r, r.Validate()
}

// Clone returns a deep copy; the original is never aliased.
func Clone(r *Record) *Record {
	return &Record{TraceID: r.TraceID, Level: r.Level, Fields: cloneMap(r.Fields)}
}

func cloneMap(m map[string]any) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = cloneValue(v)
	}
	return out
}

func cloneValue(v any) any {
	switch t := v.(type) {
	case map[string]any:
		return cloneMap(t)
	case []any:
		out := make([]any, len(t))
		for i, e := range t {
			out[i] = cloneValue(e)
		}
		return out
	}
	return v
}

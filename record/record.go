// Package record defines a structured log record with nested fields, a trace
// id and a level, plus JSON codec and structural validation.
package record

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"unsafe"
)

// MaxDepth bounds nesting depth of field maps and arrays.
const MaxDepth = 32

var (
	// ErrDepth means nesting exceeds MaxDepth.
	ErrDepth = errors.New("record: nesting depth exceeded")
	// ErrCycle means a field map contains a self reference.
	ErrCycle = errors.New("record: cyclic reference detected")
)

// Level is an ordered log severity.
type Level int

// Supported levels; error and above are LevelError and LevelFatal.
const (
	LevelDebug Level = iota
	LevelInfo
	LevelWarn
	LevelError
	LevelFatal
)

// Record is a single structured log entry.
type Record struct {
	// HasTrace distinguishes a missing trace id from an empty-string trace id.
	HasTrace        bool           `json:"-"`
	TraceID         string         `json:"trace_id,omitempty"`
	Level           Level          `json:"level"`
	Fields          map[string]any `json:"fields"`
	ChainIncomplete bool           `json:"chain_incomplete,omitempty"`
}

type wireRecord struct {
	TraceID         *string        `json:"trace_id"`
	Level           Level          `json:"level"`
	Fields          map[string]any `json:"fields"`
	ChainIncomplete bool           `json:"chain_incomplete,omitempty"`
}

// New builds a record and immediately validates it.
func New(traceID string, hasTrace bool, level Level, fields map[string]any) (*Record, error) {
	r := &Record{HasTrace: hasTrace, TraceID: traceID, Level: level, Fields: fields}
	if err := r.Validate(); err != nil {
		return nil, err
	}
	return r, nil
}

// Encode serializes the record as JSON.
func (r *Record) Encode() ([]byte, error) {
	if err := r.Validate(); err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(r); err != nil {
		return nil, err
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}

// Decode parses a JSON record and validates depth/cycles.
func Decode(data []byte) (*Record, error) {
	var w wireRecord
	if err := json.Unmarshal(data, &w); err != nil {
		return nil, err
	}
	r := &Record{Level: w.Level, Fields: w.Fields, ChainIncomplete: w.ChainIncomplete}
	if w.TraceID != nil {
		r.HasTrace = true
		r.TraceID = *w.TraceID
	}
	if err := r.Validate(); err != nil {
		return nil, err
	}
	return r, nil
}

// MarshalJSON implements json.Marshaler.
func (r *Record) MarshalJSON() ([]byte, error) {
	w := wireRecord{Level: r.Level, Fields: r.Fields, ChainIncomplete: r.ChainIncomplete}
	if r.HasTrace {
		id := r.TraceID
		w.TraceID = &id
	}
	return json.Marshal(w)
}

// Validate checks depth limits and cyclic references without stack overflow.
func (r *Record) Validate() error {
	if r.Fields == nil {
		return nil
	}
	active := map[unsafe.Pointer]struct{}{}
	return validateValue(r.Fields, 1, active)
}

func mapID(m map[string]any) unsafe.Pointer {
	return reflect.ValueOf(m).UnsafePointer()
}

func validateValue(v any, depth int, active map[unsafe.Pointer]struct{}) error {
	switch t := v.(type) {
	case map[string]any:
		if depth > MaxDepth {
			return fmt.Errorf("%w: depth=%d", ErrDepth, depth)
		}
		id := mapID(t)
		if _, seen := active[id]; seen {
			return fmt.Errorf("%w: map visited twice on one path", ErrCycle)
		}
		active[id] = struct{}{}
		for _, child := range t {
			if err := validateValue(child, depth+1, active); err != nil {
				return err
			}
		}
		delete(active, id)
	case []any:
		if depth > MaxDepth {
			return fmt.Errorf("%w: depth=%d", ErrDepth, depth)
		}
		for _, child := range t {
			if err := validateValue(child, depth+1, active); err != nil {
				return err
			}
		}
	}
	return nil
}

// Clone returns a deep copy using the JSON representation.
func (r *Record) Clone() (*Record, error) {
	b, err := r.Encode()
	if err != nil {
		return nil, err
	}
	return Decode(b)
}

// CountFields counts map keys and array elements in the field tree.
func CountFields(v any) int {
	switch t := v.(type) {
	case map[string]any:
		n := len(t)
		for _, child := range t {
			n += CountFields(child)
		}
		return n
	case []any:
		n := len(t)
		for _, child := range t {
			n += CountFields(child)
		}
		return n
	default:
		return 0
	}
}

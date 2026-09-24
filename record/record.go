// Package record defines a structured log record: nested field map, trace id
// and level, plus JSON codec and construction-time depth/cycle validation.
package record

import (
	"encoding/json"
	"errors"
	"reflect"
)

// MaxDepth is the maximum allowed nesting depth (root map counts as 1).
const MaxDepth = 32

// Level is a log severity.
type Level int

const (
	Debug Level = iota
	Info
	Warn
	Error
)

// Sentinel errors, distinguishable with errors.Is.
var (
	// ErrTooDeep reports a record whose nesting exceeds MaxDepth.
	ErrTooDeep = errors.New("record: nesting depth exceeds limit")
	// ErrCycle reports a self-referential (map) value.
	ErrCycle = errors.New("record: cyclic field reference")
)

// Record is one structured log entry.
type Record struct {
	Trace      string         `json:"trace"`
	Level      Level          `json:"level"`
	Fields     map[string]any `json:"fields"`
	Incomplete bool           `json:"incomplete,omitempty"`
}

// New validates fields (depth and cycles) and returns a record.
func New(trace string, level Level, fields map[string]any) (*Record, error) {
	if err := validate(fields, 1, map[uintptr]bool{}); err != nil {
		return nil, err
	}
	return &Record{Trace: trace, Level: level, Fields: fields}, nil
}

func validate(v any, depth int, seen map[uintptr]bool) error {
	switch t := v.(type) {
	case map[string]any:
		if depth > MaxDepth {
			return ErrTooDeep
		}
		p := reflect.ValueOf(t).Pointer()
		if seen[p] {
			return ErrCycle
		}
		seen[p] = true
		defer delete(seen, p)
		for _, child := range t {
			if err := validate(child, depth+1, seen); err != nil {
				return err
			}
		}
	case []any:
		if depth > MaxDepth {
			return ErrTooDeep
		}
		for _, child := range t {
			if err := validate(child, depth+1, seen); err != nil {
				return err
			}
		}
	}
	return nil
}

// Marshal encodes the record as one JSON object.
func (r *Record) Marshal() ([]byte, error) { return json.Marshal(r) }

// Unmarshal decodes JSON and validates the result.
func Unmarshal(data []byte) (*Record, error) {
	r := &Record{}
	if err := json.Unmarshal(data, r); err != nil {
		return nil, err
	}
	if err := validate(r.Fields, 1, map[uintptr]bool{}); err != nil {
		return nil, err
	}
	return r, nil
}

// String returns the level name.
func (l Level) String() string {
	switch l {
	case Debug:
		return "debug"
	case Info:
		return "info"
	case Warn:
		return "warn"
	default:
		return "error"
	}
}

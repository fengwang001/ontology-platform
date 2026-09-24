package record

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
)

const (
	// Debug is the lowest emitted level.
	Debug Level = iota
	// Info is the default informational level.
	Info
	// Warn is retained like other ordinary levels.
	Warn
	// Error and above are forcibly retained by the sampler.
	Error
)

// MaxDepth is the maximum nesting depth accepted by New.
const MaxDepth = 8

var (
	// ErrDepth means a field map exceeds MaxDepth.
	ErrDepth = errors.New("record: nesting depth exceeds limit")
	// ErrCycle means a map occurs in its own ancestry.
	ErrCycle = errors.New("record: cyclic field reference")
)

// Level is a log severity. Error and higher force retention.
type Level int

// Record is one structured log record.
type Record struct {
	Level   Level         `json:"level"`
	TraceID string        `json:"trace_id"`
	Fields  map[string]any `json:"fields"`
}

// New validates depth and map cycles and returns a record.
func New(level Level, traceID string, fields map[string]any) (*Record, error) {
	if err := validate(fields, 1, map[uintptr]bool{}); err != nil {
		return nil, err
	}
	return &Record{Level: level, TraceID: traceID, Fields: fields}, nil
}

// Marshal returns deterministic JSON bytes.
func (r *Record) Marshal() ([]byte, error) { return json.Marshal(r) }

// Unmarshal parses one JSON record.
func Unmarshal(data []byte) (*Record, error) {
	var r Record
	if err := json.Unmarshal(data, &r); err != nil {
		return nil, err
	}
	return &r, nil
}

func validate(value any, depth int, parents map[uintptr]bool) error {
	switch typed := value.(type) {
	case map[string]any:
		if depth > MaxDepth {
			return fmt.Errorf("%w: depth %d", ErrDepth, depth)
		}
		identity := mapIdentity(typed)
		if identity != 0 {
			if parents[identity] {
				return fmt.Errorf("%w at depth %d", ErrCycle, depth)
			}
			parents[identity] = true
			defer delete(parents, identity)
		}
		for _, child := range typed {
			if err := validate(child, depth+1, parents); err != nil {
				return err
			}
		}
	case []any:
		if depth > MaxDepth {
			return fmt.Errorf("%w: depth %d", ErrDepth, depth)
		}
		for _, child := range typed {
			if err := validate(child, depth, parents); err != nil {
				return err
			}
		}
	}
	return nil
}

func mapIdentity(m map[string]any) uintptr {
	return reflect.ValueOf(m).Pointer()
}

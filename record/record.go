// Package record defines structured log records, deterministic encoding,
// and structural validation (depth limit, cycle detection).
package record

import (
	"bytes"
	"encoding/json"
	"errors"
	"reflect"
	"sort"
	"sync/atomic"
)

// MaxDepth is the maximum allowed nesting depth (root map = depth 1).
const MaxDepth = 32

// Sentinel errors, distinguishable via errors.Is.
var (
	ErrDepthExceeded = errors.New("record: nesting depth exceeded")
	ErrCycle         = errors.New("record: self-referential cycle detected")
)

// Record is a structured log record.
type Record struct {
	Level   string         `json:"level"`
	TraceID string         `json:"trace_id"`
	Fields  map[string]any `json:"fields"`

	// IncompleteChain marks a force-kept record whose trace chain was sampled out.
	IncompleteChain bool `json:"incomplete_chain,omitempty"`
}

// LevelError is the first level that is always retained.
const LevelError = "error"

func mapPtr(m map[string]any) uintptr  { return reflect.ValueOf(m).Pointer() }
func slicePtr(s []any) uintptr         { return reflect.ValueOf(s).Pointer() }

var depthRejected atomic.Int64

// DepthRejected returns the number of records rejected for exceeding MaxDepth.
func DepthRejected() int64 { return depthRejected.Load() }

// Check validates a value tree: rejects cycles and excessive depth.
// The root map counts as depth 1.
func Check(v any) error {
	seen := map[uintptr]bool{}
	return check(v, 1, seen)
}

func check(v any, depth int, seen map[uintptr]bool) error {
	switch t := v.(type) {
	case map[string]any:
		if depth > MaxDepth {
			depthRejected.Add(1)
			return ErrDepthExceeded
		}
		ptr := mapPtr(t)
		if ptr != 0 {
			if seen[ptr] {
				return ErrCycle
			}
			seen[ptr] = true
			defer delete(seen, ptr)
		}
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			if err := check(t[k], depth+1, seen); err != nil {
				return err
			}
		}
	case []any:
		if depth > MaxDepth {
			depthRejected.Add(1)
			return ErrDepthExceeded
		}
		ptr := slicePtr(t)
		if ptr != 0 {
			if seen[ptr] {
				return ErrCycle
			}
			seen[ptr] = true
			defer delete(seen, ptr)
		}
		for _, e := range t {
			if err := check(e, depth+1, seen); err != nil {
				return err
			}
		}
	}
	return nil
}

// Encode deterministically encodes a record to sorted-key JSON.
func Encode(r *Record) ([]byte, error) {
	if err := Check(r.Fields); err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(orderedRecord{r}); err != nil {
		return nil, err
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}

// Decode parses a JSON record.
func Decode(b []byte) (*Record, error) {
	var r Record
	if err := json.Unmarshal(b, &r); err != nil {
		return nil, err
	}
	if err := Check(r.Fields); err != nil {
		return nil, err
	}
	return &r, nil
}

// orderedRecord renders Fields with sorted keys via jsonOrderer.
type orderedRecord struct{ r *Record }

func (o orderedRecord) MarshalJSON() ([]byte, error) {
	f, err := json.Marshal(orderValue(o.r.Fields))
	if err != nil {
		return nil, err
	}
	buf := []byte(`{"level":`)
	lb, _ := json.Marshal(o.r.Level)
	tb, _ := json.Marshal(o.r.TraceID)
	buf = append(buf, lb...)
	buf = append(buf, []byte(`,"trace_id":`)...)
	buf = append(buf, tb...)
	buf = append(buf, []byte(`,"fields":`)...)
	buf = append(buf, f...)
	if o.r.IncompleteChain {
		buf = append(buf, []byte(`,"incomplete_chain":true}`)...)
	} else {
		buf = append(buf, '}')
	}
	return buf, nil
}

func orderValue(v any) any {
	switch t := v.(type) {
	case map[string]any:
		return jsonOrderer(t)
	case []any:
		out := make([]any, len(t))
		for i, e := range t {
			out[i] = orderValue(e)
		}
		return out
	default:
		return v
	}
}

// jsonOrderer is a map that marshals with sorted keys.
type jsonOrderer map[string]any

func (m jsonOrderer) MarshalJSON() ([]byte, error) {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var buf bytes.Buffer
	buf.WriteByte('{')
	for i, k := range keys {
		if i > 0 {
			buf.WriteByte(',')
		}
		kb, err := json.Marshal(k)
		if err != nil {
			return nil, err
		}
		buf.Write(kb)
		buf.WriteByte(':')
		vb, err := json.Marshal(orderValue(m[k]))
		if err != nil {
			return nil, err
		}
		buf.Write(vb)
}
	buf.WriteByte('}')
	return buf.Bytes(), nil
}

// Package record defines a structured log record and its encoding.
package record

import (
	"bytes"
	"encoding/json"
	"errors"
	"reflect"
)

// MaxDepth is the maximum allowed nesting depth of field maps.
const MaxDepth = 32

// Sentinel errors, distinguishable with errors.Is.
var (
	ErrDepthExceeded = errors.New("record: field depth exceeds limit")
	ErrCycle         = errors.New("record: self-referential map detected")
)

// Level is a log severity.
type Level int

const (
	LevelDebug Level = iota
	LevelInfo
	LevelWarn
	LevelError
)

// Kind enumerates value types.
type Kind uint8

const (
	KStr Kind = iota
	KNum
	KBool
	KMap
	KArr
)

// Value is a discriminated field value.
type Value struct {
	Kind Kind
	Str  string
	Num  float64
	Bool bool
	Map  map[string]Value
	Arr  []Value
}

// String is a convenience constructor.
func String(s string) Value { return Value{Kind: KStr, Str: s} }

// Number is a convenience constructor.
func Number(n float64) Value { return Value{Kind: KNum, Num: n} }

// Boolean is a convenience constructor.
func Boolean(b bool) Value { return Value{Kind: KBool, Bool: b} }

// M builds a map value.
func M(entries map[string]Value) Value { return Value{Kind: KMap, Map: entries} }

// A builds an array value.
func A(items ...Value) Value { return Value{Kind: KArr, Arr: items} }

// Record is one structured log entry.
type Record struct {
	Level           Level              `json:"level"`
	TraceID         string             `json:"trace_id"`
	Fields          map[string]Value   `json:"fields"`
	ChainIncomplete bool               `json:"chain_incomplete,omitempty"`
}

// Validate rejects over-deep or cyclic field structures.
func (r *Record) Validate() error {
	return validateMap(r.Fields, 1, map[uintptr]struct{}{})
}

func validateMap(m map[string]Value, depth int, visits map[uintptr]struct{}) error {
	if depth > MaxDepth {
		return ErrDepthExceeded
	}
	if m != nil {
		p := reflect.ValueOf(m).Pointer()
		if _, ok := visits[p]; ok {
			return ErrCycle
		}
		visits[p] = struct{}{}
		defer delete(visits, p)
		for _, v := range m {
			if err := validateValue(v, depth, visits); err != nil {
				return err
			}
		}
	}
	return nil
}

func validateValue(v Value, depth int, visits map[uintptr]struct{}) error {
	switch v.Kind {
	case KMap:
		return validateMap(v.Map, depth+1, visits)
	case KArr:
		for _, e := range v.Arr {
			if err := validateValue(e, depth, visits); err != nil {
				return err
			}
		}
	}
	return nil
}

// MarshalJSON encodes a value canonically (keys sorted by encoding/json).
func (v Value) MarshalJSON() ([]byte, error) {
	switch v.Kind {
	case KStr:
		return json.Marshal(v.Str)
	case KNum:
		return json.Marshal(v.Num)
	case KBool:
		return json.Marshal(v.Bool)
	case KArr:
		return json.Marshal(v.Arr)
	default:
		return json.Marshal(v.Map)
	}
}

// UnmarshalJSON decodes JSON into the discriminated value.
func (v *Value) UnmarshalJSON(data []byte) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var raw any
	if err := dec.Decode(&raw); err != nil {
		return err
	}
	*v = fromAny(raw)
	return nil
}

func fromAny(x any) Value {
	switch t := x.(type) {
	case nil:
		return String("")
	case string:
		return String(t)
	case bool:
		return Boolean(t)
	case json.Number:
		f, _ := t.Float64()
		return Number(f)
	case []any:
		items := make([]Value, len(t))
		for i, e := range t {
			items[i] = fromAny(e)
		}
		return A(items...)
	case map[string]any:
		m := make(map[string]Value, len(t))
		for k, e := range t {
			m[k] = fromAny(e)
		}
		return M(m)
	default:
		return String("")
	}
}

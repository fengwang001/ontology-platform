package bitemporal

import (
	"crypto/sha256"
	"encoding/json"
	"math"
)

// Tick is one point on both time axes. The valid-time axis is the discrete
// domain [MinTick, MaxTick); intervals are half-open [Start, End).
type Tick = int64

const (
	// MinTick is the smallest representable valid-time point.
	MinTick Tick = math.MinInt64
	// MaxTick is one past the largest representable valid-time point.
	MaxTick Tick = math.MaxInt64
)

// Interval is a half-open valid-time interval [Start, End): Start is covered,
// End is not.
type Interval struct {
	Start Tick `json:"start"`
	End   Tick `json:"end"`
}

// NewInterval validates and constructs a half-open interval.
func NewInterval(start, end Tick) (Interval, error) {
	if start >= end {
		return Interval{}, &ExportError{Code: CodeInvalidRange, Msg: "valid interval must be half-open with start < end"}
	}
	return Interval{Start: start, End: end}, nil
}

// Contains reports whether t is covered by the half-open interval.
func (iv Interval) Contains(t Tick) bool { return iv.Start <= t && t < iv.End }

// FieldKind enumerates the scalar field kinds a schema may declare.
type FieldKind string

const (
	KindString FieldKind = "string"
	KindInt    FieldKind = "int64"
	KindFloat  FieldKind = "float64"
	KindBool   FieldKind = "bool"
)

// Schema describes an object type and the transaction time at which it became
// defined. Types cannot be undeleted in this implementation.
type Schema struct {
	Name      string               `json:"name"`
	DefinedAt Tick                 `json:"defined_at"`
	Fields    map[string]FieldKind `json:"fields"`
}

// Value is the scalar payload of a record. Field values must be one of
// string, int64, float64, bool.
type Value struct {
	Fields map[string]any `json:"fields"`
}

// Equal is a deterministic structural comparison.
func (v Value) Equal(o Value) bool {
	return string(v.CanonicalBytes()) == string(o.CanonicalBytes())
}

// CanonicalBytes returns a deterministic encoding of the value, independent of
// Go map iteration order. It is the basis for hashing and for byte-identical
// repeated exports.
func (v Value) CanonicalBytes() []byte {
	b, err := json.Marshal(sortedFields(v.Fields))
	if err != nil {
		panic(err)
	}
	return b
}

// Record is one bi-temporal assertion: payload Value was business-true over
// [Start, End), and the system recorded that claim at TxTime. Seq is the
// global arrival order assigned by the store (1-based; 0 means unassigned).
type Record struct {
	ObjectID string   `json:"object_id"`
	TypeName string   `json:"type_name"`
	Start    Tick     `json:"start"`
	End      Tick     `json:"end"`
	TxTime   Tick     `json:"tx_time"`
	Seq      uint64   `json:"seq"`
	Value    Value    `json:"value"`
	Hash     [32]byte `json:"-"`
}

// Interval returns the record's valid-time interval.
func (r *Record) Interval() Interval { return Interval{Start: r.Start, End: r.End} }

// computeHash derives the integrity hash from the immutable record content.
func (r *Record) computeHash() [32]byte {
	type hashBody struct {
		ObjectID string `json:"object_id"`
		TypeName string `json:"type_name"`
		Start    Tick   `json:"start"`
		End      Tick   `json:"end"`
		TxTime   Tick   `json:"tx_time"`
		Value    []byte `json:"value"`
	}
	b, _ := json.Marshal(hashBody{
		ObjectID: r.ObjectID,
		TypeName: r.TypeName,
		Start:    r.Start,
		End:      r.End,
		TxTime:   r.TxTime,
		Value:    r.Value.CanonicalBytes(),
	})
	return sha256.Sum256(b)
}

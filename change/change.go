// Package change defines a single base-table change record and its encoding.
package change

import (
	"encoding/json"
	"errors"
	"math"
)

// Op is the mutation kind carried by a Change.
type Op uint8

const (
	// Insert adds a record into NewGroup with NewValue.
	Insert Op = 1
	// Delete removes the record identified by OldGroup/OldValue.
	Delete Op = 2
	// Update replaces OldGroup/OldValue by NewGroup/NewValue.
	Update Op = 3
)

// Sentinel errors returned by Valid / Decode; all are errors.Is-matchable.
var (
	ErrBadVersion   = errors.New("change: version must be positive")
	ErrBadOp        = errors.New("change: unknown op")
	ErrMissingGroup = errors.New("change: group key missing")
	ErrNaN          = errors.New("change: NaN value rejected")
	ErrBadEncoding  = errors.New("change: malformed encoding")
)

// Change is one base-table mutation. The *OK flags distinguish a present
// empty-string group key from an absent (rejected) key.
type Change struct {
	Version    int64
	Op         Op
	OldGroup   string
	OldGroupOK bool
	NewGroup   string
	NewGroupOK bool
	OldValue   float64
	NewValue   float64
}

type wire struct {
	V  int64    `json:"v"`
	O  int      `json:"o"`
	OG *string  `json:"og"`
	NG *string  `json:"ng"`
	OV *float64 `json:"ov"`
	NV *float64 `json:"nv"`
}

// Valid checks op-specific presence rules and rejects NaN / nonpositive versions.
func (c Change) Valid() error {
	if c.Version <= 0 {
		return ErrBadVersion
	}
	switch c.Op {
	case Insert:
		if !c.NewGroupOK || math.IsNaN(c.NewValue) {
			return missingOrNaN(c.NewGroupOK, c.NewValue)
		}
	case Delete:
		if !c.OldGroupOK || math.IsNaN(c.OldValue) {
			return missingOrNaN(c.OldGroupOK, c.OldValue)
		}
	case Update:
		if !c.OldGroupOK || !c.NewGroupOK {
			return ErrMissingGroup
		}
		if math.IsNaN(c.OldValue) || math.IsNaN(c.NewValue) {
			return ErrNaN
		}
	default:
		return ErrBadOp
	}
	return nil
}

func missingOrNaN(groupOK bool, v float64) error {
	if !groupOK {
		return ErrMissingGroup
	}
	if math.IsNaN(v) {
		return ErrNaN
	}
	return nil
}

// Encode returns the canonical self-describing JSON form of the change.
func (c Change) Encode() ([]byte, error) {
	if err := c.Valid(); err != nil {
		return nil, err
	}
	w := wire{V: c.Version, O: int(c.Op)}
	if c.OldGroupOK {
		g := c.OldGroup
		w.OG = &g
		w.OV = &c.OldValue
	}
	if c.NewGroupOK {
		g := c.NewGroup
		w.NG = &g
		w.NV = &c.NewValue
	}
	b, err := json.Marshal(w)
	if err != nil {
		return nil, errors.Join(ErrBadEncoding, err)
	}
	return b, nil
}

// Decode parses an encoded change and re-validates it.
func Decode(b []byte) (Change, error) {
	var w wire
	if err := json.Unmarshal(b, &w); err != nil {
		return Change{}, errors.Join(ErrBadEncoding, err)
	}
	c := Change{Version: w.V, Op: Op(w.O)}
	if w.OG != nil {
		if w.OV == nil {
			return Change{}, ErrBadEncoding
		}
		c.OldGroup, c.OldGroupOK, c.OldValue = *w.OG, true, *w.OV
	}
	if w.NG != nil {
		if w.NV == nil {
			return Change{}, ErrBadEncoding
		}
		c.NewGroup, c.NewGroupOK, c.NewValue = *w.NG, true, *w.NV
	}
	if err := c.Valid(); err != nil {
		return Change{}, err
	}
	return c, nil
}

// NormalizeZero canonicalizes -0 to +0 so the two zeros compare equal.
func NormalizeZero(v float64) float64 {
	if v == 0 {
		return 0
	}
	return v
}

func (o Op) String() string {
	switch o {
	case Insert:
		return "insert"
	case Delete:
		return "delete"
	case Update:
		return "update"
	}
	return "unknown"
}

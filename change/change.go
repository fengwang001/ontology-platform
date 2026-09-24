// Package change defines base-table change records and their wire codec.
package change

import (
	"encoding/json"
	"errors"
	"math"
)

// Op is the kind of a base-table change.
type Op uint8

const (
	Insert Op = 1
	Delete Op = 2
	Update Op = 3
)

func (o Op) String() string {
	switch o {
	case Insert:
		return "insert"
	case Delete:
		return "delete"
	case Update:
		return "update"
	default:
		return "unknown"
	}
}

// Change is one base-table mutation. Group/Value/RecID describe the target
// member (new member for Insert and Update; removed member for Delete).
// For Update, OldGroup/OldValue describe the member before replacement and
// OldID defaults to RecID when empty. A nil Group means "key missing" and is
// rejected; a pointer to "" is the legal empty-string key.
type Change struct {
	Version  uint64  `json:"v"`
	Op       Op      `json:"op"`
	Group    *string `json:"g"`
	Value    float64 `json:"val"`
	RecID    string  `json:"id"`
	OldGroup *string `json:"og"`
	OldValue float64 `json:"oval"`
	OldID    string  `json:"oid"`
}

// Validation errors returned by Valid; callers may use errors.Is.
var (
	ErrBadOp       = errors.New("change: unknown op")
	ErrMissingID   = errors.New("change: missing record id")
	ErrMissingGrp  = errors.New("change: missing group key")
	ErrNaNValue    = errors.New("change: NaN value rejected")
	ErrBadVersion  = errors.New("change: version must be positive")
	ErrOldMissing  = errors.New("change: update missing old group key")
)

// G returns a pointer to s, for constructing the Group field.
func G(s string) *string { return &s }

// Valid checks wire-level validity independent of view state.
// The empty-string group key itself is legal; callers distinguish
// "missing" via an explicit HasGroup-like convention: this project never
// produces missing keys in Go, so only NaN/op/id are structurally rejected
// here, and view additionally rejects unknown members.
func (c Change) Valid() error {
	if c.Version == 0 {
		return ErrBadVersion
	}
	if c.Op != Insert && c.Op != Delete && c.Op != Update {
		return ErrBadOp
	}
	if c.RecID == "" {
		return ErrMissingID
	}
	if c.Group == nil || (c.Op == Update && c.OldGroup == nil) {
		return ErrMissingGrp
	}
	if math.IsNaN(c.Value) || (c.Op == Update && math.IsNaN(c.OldValue)) {
		return ErrNaNValue
	}
	return nil
}

// Encode returns the canonical JSON payload of the change.
func (c Change) Encode() ([]byte, error) {
	if err := c.Valid(); err != nil {
		return nil, err
	}
	return json.Marshal(c)
}

// Decode parses a JSON payload produced by Encode.
func Decode(p []byte) (Change, error) {
	var c Change
	if err := json.Unmarshal(p, &c); err != nil {
		return Change{}, err
	}
	return c, c.Valid()
}

// OldRecID returns the id of the member replaced by an update.
func (c Change) OldRecID() string {
	if c.OldID != "" {
		return c.OldID
	}
	return c.RecID
}

// Key returns the target group key; panics only if called on an invalid
// change (nil Group), which callers must have rejected via Valid.
func (c Change) Key() string { return *c.Group }

// OldKey returns the group key of the member replaced by an update.
func (c Change) OldKey() string { return *c.OldGroup }

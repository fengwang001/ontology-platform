// Package change defines a base-table change record and its body encoding.
package change

import (
	"encoding/json"
	"fmt"
	"math"
)

// Op is the mutation kind carried by a Change.
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
		return fmt.Sprintf("op(%d)", int(o))
	}
}

// Change is one versioned base-table mutation.
//
// Group is a pointer so that an absent group key (nil, rejected) is
// distinguishable from a legal but empty-string group key. For Update,
// Group/Value describe the record's new state; its old state is resolved
// by the view through RecID.
type Change struct {
	Version uint64  `json:"v"`
	Op      Op      `json:"o"`
	RecID   string  `json:"id"`
	Group   *string `json:"g"`
	Value   float64 `json:"val"`
}

// Group returns the group key and whether it was present.
func (c Change) GroupKey() (string, bool) {
	if c.Group == nil {
		return "", false
	}
	return *c.Group, true
}

// G is a helper for an existing (possibly empty) group key.
func G(key string) *string { return &key }

// Valid reports semantic validity independent of versioning.
func (c Change) Valid() error {
	if c.Op < Insert || c.Op > Update {
		return fmt.Errorf("change: unknown op %d", c.Op)
	}
	if c.RecID == "" {
		return fmt.Errorf("change: missing record id")
	}
	if c.Group == nil {
		return fmt.Errorf("change: missing group key")
	}
	if math.IsNaN(c.Value) {
		return fmt.Errorf("change: NaN value")
	}
	return nil
}

// Encode renders the self-describing JSON body of one record.
func Encode(c Change) ([]byte, error) {
	if err := c.Valid(); err != nil {
		return nil, err
	}
	return json.Marshal(c)
}
// Decode parses one JSON body produced by Encode.
func Decode(b []byte) (Change, error) {
	var c Change
	if err := json.Unmarshal(b, &c); err != nil {
		return Change{}, err
	}
	if err := c.Valid(); err != nil {
		return Change{}, err
	}
	return c, nil
}

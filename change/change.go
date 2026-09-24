package change

import (
	"encoding/json"
	"errors"
	"math"
)

type Op string

const (
	Insert Op = "insert"
	Delete Op = "delete"
	Update Op = "update"
)

type Change struct {
	Version  int64   `json:"version"`
	Op       Op      `json:"op"`
	ID       string  `json:"id"`
	OldGroup string  `json:"old_group,omitempty"`
	NewGroup string  `json:"new_group,omitempty"`
	OldValue float64 `json:"old_value,omitempty"`
	NewValue float64 `json:"new_value,omitempty"`
	HasOld   bool    `json:"has_old,omitempty"`
	HasNew   bool    `json:"has_new,omitempty"`
}

var (
	ErrInvalid  = errors.New("invalid change")
	ErrEncoding = errors.New("change encoding error")
)

func Encode(c Change) ([]byte, error) {
	if err := c.Validate(); err != nil {
		return nil, err
	}
	data, err := json.Marshal(c)
	if err != nil {
		return nil, errors.Join(ErrEncoding, err)
	}
	return data, nil
}

func Decode(data []byte) (Change, error) {
	var c Change
	if err := json.Unmarshal(data, &c); err != nil {
		return Change{}, errors.Join(ErrEncoding, err)
	}
	if err := c.Validate(); err != nil {
		return Change{}, err
	}
	return c, nil
}

func (c Change) Validate() error {
	if c.Version <= 0 || c.ID == "" {
		return ErrInvalid
	}
	switch c.Op {
	case Insert:
		if c.HasOld || !c.HasNew || c.NewGroup == "" || badFloat(c.NewValue) {
			return ErrInvalid
		}
	case Delete:
		if !c.HasOld || c.HasNew || c.OldGroup == "" || badFloat(c.OldValue) {
			return ErrInvalid
		}
	case Update:
		if !c.HasOld || !c.HasNew || c.OldGroup == "" || c.NewGroup == "" ||
			badFloat(c.OldValue) || badFloat(c.NewValue) {
			return ErrInvalid
		}
	default:
		return ErrInvalid
	}
	return nil
}

func badFloat(v float64) bool {
	return math.IsNaN(v) || math.IsInf(v, 0)
}

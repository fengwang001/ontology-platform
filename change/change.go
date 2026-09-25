package change

import (
	"bytes"
	"encoding/binary"
	"errors"
	"math"
)

type Op uint8

const (
	Insert Op = iota + 1
	Delete
	Update
)

type Change struct {
	Version   uint64
	Op        Op
	ID        string
	HasGroup  bool
	Group     string
	Value     float64
	NewHasGrp bool
	NewGroup  string
	NewValue  float64
}

var (
	ErrInvalidOp    = errors.New("invalid change operation")
	ErrInvalidBody  = errors.New("invalid change body")
	ErrMissingGroup = errors.New("missing group")
	ErrNaNValue     = errors.New("NaN value")
)

func (c Change) Validate() error {
	if c.Version == 0 || c.Op < Insert || c.Op > Update || c.ID == "" || !c.HasGroup {
		return ErrInvalidBody
	}
	if math.IsNaN(c.Value) || (c.Op == Update && math.IsNaN(c.NewValue)) {
		return ErrNaNValue
	}
	if c.Op == Update && !c.NewHasGrp {
		return ErrMissingGroup
	}
	return nil
}

func appendString(b []byte, s string) []byte {
	var n [8]byte
	binary.BigEndian.PutUint64(n[:], uint64(len(s)))
	return append(append(b, n[:]...), s...)
}

func readString(b []byte) (string, []byte, bool) {
	if len(b) < 8 {
		return "", nil, false
	}
	n := binary.BigEndian.Uint64(b[:8])
	b = b[8:]
	if uint64(len(b)) < n {
		return "", nil, false
	}
	return string(b[:n]), b[n:], true
}

func appendFloat(b []byte, v float64) []byte {
	var n [8]byte
	binary.BigEndian.PutUint64(n[:], math.Float64bits(v))
	return append(b, n[:]...)
}

func readFloat(b []byte) (float64, []byte, bool) {
	if len(b) < 8 {
		return 0, nil, false
	}
	return math.Float64frombits(binary.BigEndian.Uint64(b[:8])), b[8:], true
}

func (c Change) Encode() ([]byte, error) {
	if err := c.Validate(); err != nil {
		return nil, err
	}
	b := make([]byte, 0, 64+len(c.ID)+len(c.Group)+len(c.NewGroup))
	var h [10]byte
	binary.BigEndian.PutUint64(h[:8], c.Version)
	h[8] = byte(c.Op)
	if c.NewHasGrp {
		h[9] = 1
	}
	b = append(b, h[:]...)
	b = appendString(b, c.ID)
	b = appendString(b, c.Group)
	b = appendFloat(b, c.Value)
	if c.Op == Update {
		b = appendString(b, c.NewGroup)
		b = appendFloat(b, c.NewValue)
	}
	return b, nil
}

func Decode(b []byte) (Change, error) {
	var c Change
	if len(b) < 10 {
		return c, ErrInvalidBody
	}
	c.Version = binary.BigEndian.Uint64(b[:8])
	c.Op = Op(b[8])
	c.NewHasGrp = b[9] == 1
	var ok bool
	rest := b[10:]
	if c.ID, rest, ok = readString(rest); !ok {
		return c, ErrInvalidBody
	}
	if c.Group, rest, ok = readString(rest); !ok {
		return c, ErrInvalidBody
	}
	if c.Value, rest, ok = readFloat(rest); !ok {
		return c, ErrInvalidBody
	}
	c.HasGroup = true
	if c.Op == Update {
		if c.NewGroup, rest, ok = readString(rest); !ok {
			return c, ErrInvalidBody
		}
		if c.NewValue, rest, ok = readFloat(rest); !ok || len(rest) != 0 {
			return c, ErrInvalidBody
		}
	} else if len(rest) != 0 || c.Op < Insert || c.Op > Update || c.NewHasGrp {
		return c, ErrInvalidBody
	}
	if err := c.Validate(); err != nil && !errors.Is(err, ErrMissingGroup) {
		return c, err
	}
	return c, nil
}

func Equal(a, b Change) bool {
	ab, _ := a.Encode()
	bb, _ := b.Encode()
	return bytes.Equal(ab, bb)
}

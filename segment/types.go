// Package segment stores one immutable, in-memory column segment made of
// independently encoded row groups. Each row group keeps zone stats and a
// null bitmap; data is either bit-packed (integers) or dictionary encoded
// (integers or strings). It depends on bitpack, dict and zone only.
package segment

import (
	"errors"

	"ontology/zone"
)

// Encoding selects how a row group's non-null values are stored.
type Encoding uint8

const (
	EncBitPack Encoding = 1
	EncDict    Encoding = 2
)

func (e Encoding) String() string {
	switch e {
	case EncBitPack:
		return "bitpack"
	case EncDict:
		return "dict"
	default:
		return "unknown"
	}
}

// Stage names reported by CorruptError, in decode order.
const (
	StageHeader = "header"
	StageStats  = "stats"
	StageNulls  = "null-bitmap"
	StageData   = "data-block"
)

var (
	// ErrTooManyRows is returned when the segment row limit is exceeded.
	ErrTooManyRows = errors.New("segment: total row limit exceeded")
	// ErrTooManyGroups is returned when the row-group count limit is exceeded.
	ErrTooManyGroups = errors.New("segment: row-group count limit exceeded")
	// ErrDictLimit is returned when an explicitly requested dictionary
	// encoding exceeds the configured cardinality limit.
	ErrDictLimit = errors.New("segment: dictionary cardinality limit exceeded")
)

// CorruptError identifies where a truncated or malposed segment failed.
type CorruptError struct {
	Group int    // row-group index, -1 for the file header
	Stage string // one of the Stage* constants
	Err   error
}

func (e *CorruptError) Error() string {
	return "segment: corrupt at group " + itoa(e.Group) + " stage " + e.Stage + ": " + e.Err.Error()
}

func (e *CorruptError) Unwrap() error { return e.Err }

func itoa(i int) string {
	if i < 0 {
		return "-"
	}
	if i == 0 {
		return "0"
	}
	var b [20]byte
	n := len(b)
	for i > 0 {
		n--
		b[n] = byte('0' + i%10)
		i /= 10
	}
	return string(b[n:])
}

// RowGroup is the read-only view of one row group. Data is only decoded when
// Decode is called, so metadata queries never touch the encoded payload.
type RowGroup struct {
	enc     Encoding
	kind    zone.Kind
	rows    int
	nulls   int
	stats   zone.Stats
	nullSet []int // sorted positions of null rows
	data    []byte
}

// Encoding returns the encoding chosen for this row group.
func (g *RowGroup) Encoding() Encoding { return g.enc }

// Rows returns the number of rows in the group.
func (g *RowGroup) Rows() int { return g.rows }

// Stats returns the zone statistics (nulls excluded from min/max).
func (g *RowGroup) Stats() zone.Stats { return g.stats }

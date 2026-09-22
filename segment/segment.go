// Package segment implements immutable columnar segment writers and
// read-only views: row-group boundaries, encoding selection and null
// bitmaps. It depends on bitpack, dict and zone only.
package segment

import (
	"errors"

	"ontology/zone"
)

// Encoding identifies how a row group payload is encoded.
type Encoding int

// Row-group encodings.
const (
	EncInvalid Encoding = iota
	EncBitPack
	EncDict
)

// ColKind is the logical type of a column.
type ColKind int

// Supported column kinds.
const (
	ColInt ColKind = iota
	ColStr
)

// Limit errors are mutually distinguishable so callers can react to each
// resource cap independently.
var (
	ErrTooManyRows    = errors.New("segment: max rows exceeded")
	ErrTooManyGroups  = errors.New("segment: max row groups exceeded")
	ErrDictTooLarge   = errors.New("segment: dictionary cardinality exceeded")
)

// Options configures writer resource limits.
type Options struct {
	MaxRows      int
	MaxGroups    int
	MaxDictCard  int
	RowsPerGroup int
}

// Value is one logical cell. Exactly one of Null/Int/Str states is active.
type Value struct {
	Null bool
	Int  int64
	Str  string
}

// GroupInfo exposes queryable metadata without decoding payloads.
type GroupInfo struct {
	Encoding Encoding
	Stats    zone.Stats
}

// Segment is a read-only, immutable segment view.
type Segment struct{}

// Writer builds an immutable segment in process memory.
type Writer struct{}

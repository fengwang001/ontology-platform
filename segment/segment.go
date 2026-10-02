// Package segment writes immutable column segments and opens read-only
// views. Layout: [24B header][group table][data blocks]. All offsets
// and lengths are explicit; decoding validates every length before
// reading so any truncation yields a *CorruptError, never a panic.
package segment

import (
	"encoding/binary"
	"errors"
	"math/bits"

	"ontology/bitpack"
	"ontology/dict"
	"ontology/zone"
)

// Encoding identifies a row group's value encoding.
type Encoding uint8

const (
	Bitpack Encoding = iota
	Dict
)

func (e Encoding) String() string {
	if e == Dict {
		return "dict"
	}
	return "bitpack"
}

var (
	ErrTooManyRows   = errors.New("segment: max rows exceeded")
	ErrTooManyGroups = errors.New("segment: max row groups exceeded")
)

// Config configures a Builder. Zero fields get defaults.
type Config struct {
	RowsPerGroup int
	MaxRows      int
	MaxGroups    int
	MaxDictCard  int
}

// Value is one decoded cell: Null distinguishes null from a real 0.
type Value struct {
	V    int64
	Null bool
}

const (
	headerSize = 24
	entrySize  = 80
)

var magic = []byte{'O', 'S', 'E', 'G'}

// Builder accumulates rows and seals them into an immutable segment.
type Builder struct {
	cfg  Config
	rows []Value
}

func NewBuilder(cfg Config) *Builder {
	if cfg.RowsPerGroup <= 0 {
		cfg.RowsPerGroup = 128
	}
	if cfg.MaxRows <= 0 {
		cfg.MaxRows = 1 << 30
	}
	if cfg.MaxGroups <= 0 {
		cfg.MaxGroups = 1 << 20
	}
	if cfg.MaxDictCard <= 0 {
		cfg.MaxDictCard = 1024
	}
	return &Builder{cfg: cfg}
}

// Append adds one row. Limit violations are checked before any state
// change, so a rejected Append leaves the builder untouched.
func (b *Builder) Append(v int64, null bool) error {
	if len(b.rows) >= b.cfg.MaxRows {
		return ErrTooManyRows
	}
	if len(b.rows)/b.cfg.RowsPerGroup >= b.cfg.MaxGroups {
		return ErrTooManyGroups
	}
	b.rows = append(b.rows, Value{v, null})
	return nil
}

// Len returns the number of accepted rows.
func (b *Builder) Len() int { return len(b.rows) }

// Seal encodes all rows into an immutable segment image.
func (b *Builder) Seal() []byte {
	nGroups := (len(b.rows) + b.cfg.RowsPerGroup - 1) / b.cfg.RowsPerGroup
	out := append([]byte{}, magic...)
	out = binary.LittleEndian.AppendUint16(out, 1)
	out = binary.LittleEndian.AppendUint16(out, 0)
	out = binary.LittleEndian.AppendUint64(out, uint64(len(b.rows)))
	out = binary.LittleEndian.AppendUint32(out, uint32(nGroups))
	out = binary.LittleEndian.AppendUint32(out, uint32(b.cfg.RowsPerGroup))
	tablePos := len(out)
	out = append(out, make([]byte, nGroups*entrySize)...)
	for g := 0; g < nGroups; g++ {
		lo := g * b.cfg.RowsPerGroup
		hi := lo + b.cfg.RowsPerGroup
		if hi > len(b.rows) {
			hi = len(b.rows)
		}
		out = b.sealGroup(out, tablePos+g*entrySize, b.rows[lo:hi])
	}
	return out
}

func (b *Builder) sealGroup(out []byte, ep int, rows []Value) []byte {
	var st zone.Stats
	nullFlags := make([]uint64, len(rows))
	for i, r := range rows {
		st.Add(r.V, r.Null)
		if r.Null {
			nullFlags[i] = 1
		}
	}
	nullBytes := bitpack.Pack(nullFlags, 1)
	nullOff := len(out)
	out = append(out, nullBytes...)

	enc, width, card := Bitpack, uint8(1), uint32(0)
	var dictBytes, dataBytes []byte
	db := dict.NewBuilder(b.cfg.MaxDictCard)
	codes := make([]uint64, len(rows))
	dictOK := st.HasMinMax
	for i, r := range rows {
		if r.Null || !dictOK {
			continue
		}
		c, err := db.Add(r.V)
		if err != nil { // dict.ErrCardinality: degrade to bitpack
			dictOK = false
			continue
		}
		codes[i] = uint64(c)
	}
	if dictOK {
		enc, card = Dict, uint32(db.Len())
		if w := bits.Len32(card - 1); w > 1 {
			width = uint8(w)
		}
		for _, v := range db.Dict().Values() {
			dictBytes = binary.LittleEndian.AppendUint64(dictBytes, uint64(v))
		}
		dataBytes = bitpack.Pack(codes, width)
	} else {
		var maxDelta uint64
		deltas := make([]uint64, len(rows))
		for i, r := range rows {
			if r.Null {
				continue
			}
			d := uint64(r.V) - uint64(st.Min)
			deltas[i] = d
			if d > maxDelta {
				maxDelta = d
			}
		}
		if w := bits.Len64(maxDelta); w > 1 {
			width = uint8(w)
		}
		dataBytes = bitpack.Pack(deltas, width)
	}
	dictOff := len(out)
	out = append(out, dictBytes...)
	dataOff := len(out)
	out = append(out, dataBytes...)

	e := out[ep : ep+entrySize]
	binary.LittleEndian.PutUint32(e[0:], uint32(len(rows)))
	binary.LittleEndian.PutUint32(e[4:], uint32(st.Nulls))
	binary.LittleEndian.PutUint64(e[8:], uint64(st.Min))
	binary.LittleEndian.PutUint64(e[16:], uint64(st.Max))
	if st.HasMinMax {
		e[24] = 1
	}
	e[25], e[26] = uint8(enc), width
	binary.LittleEndian.PutUint32(e[28:], card)
	binary.LittleEndian.PutUint64(e[32:], uint64(nullOff))
	binary.LittleEndian.PutUint64(e[40:], uint64(len(nullBytes)))
	binary.LittleEndian.PutUint64(e[48:], uint64(dataOff))
	binary.LittleEndian.PutUint64(e[56:], uint64(len(dataBytes)))
	binary.LittleEndian.PutUint64(e[64:], uint64(dictOff))
	binary.LittleEndian.PutUint64(e[72:], uint64(len(dictBytes)))
	return out
}

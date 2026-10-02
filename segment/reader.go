package segment

import (
	"encoding/binary"
	"fmt"

	"ontology/bitpack"
	"ontology/zone"
)

// Stage identifies where corruption was detected.
type Stage int

const (
	StageHeader Stage = iota
	StageStats
	StageNullBitmap
	StageData
)

func (s Stage) String() string {
	return []string{"header", "stats", "null-bitmap", "data"}[s]
}

// CorruptError reports a truncated/corrupt segment read.
type CorruptError struct {
	Group int
	Stage Stage
	msg   string
}

func (e *CorruptError) Error() string {
	return fmt.Sprintf("segment: corrupt group %d at %s: %s", e.Group, e.Stage, e.msg)
}

type groupMeta struct {
	rows, nulls      uint32
	min, max         int64
	hasMM            bool
	enc              Encoding
	width            uint8
	card             uint32
	nullOff, nullLen uint64
	dataOff, dataLen uint64
	dictOff, dictLen uint64
}

// Segment is a read-only view over a sealed segment image.
type Segment struct {
	data   []byte
	rows   int
	groups []groupMeta
}

// Open validates the header and group table. Data blocks are validated
// lazily at DecodeGroup, so truncation is reported with the exact group
// and stage where reading failed.
func Open(data []byte) (*Segment, error) {
	if len(data) < headerSize {
		return nil, &CorruptError{-1, StageHeader, "short header"}
	}
	if string(data[:4]) != string(magic) {
		return nil, &CorruptError{-1, StageHeader, "bad magic"}
	}
	if binary.LittleEndian.Uint16(data[4:]) != 1 {
		return nil, &CorruptError{-1, StageHeader, "bad version"}
	}
	nGroups := int(binary.LittleEndian.Uint32(data[16:]))
	if len(data) < headerSize+nGroups*entrySize {
		g := 0
		if len(data) > headerSize {
			g = (len(data) - headerSize) / entrySize
		}
		return nil, &CorruptError{g, StageStats, "truncated group table"}
	}
	s := &Segment{data: data, rows: int(binary.LittleEndian.Uint64(data[8:]))}
	for i := 0; i < nGroups; i++ {
		e := data[headerSize+i*entrySize:]
		s.groups = append(s.groups, groupMeta{
			rows:    binary.LittleEndian.Uint32(e[0:]),
			nulls:   binary.LittleEndian.Uint32(e[4:]),
			min:     int64(binary.LittleEndian.Uint64(e[8:])),
			max:     int64(binary.LittleEndian.Uint64(e[16:])),
			hasMM:   e[24]&1 != 0,
			enc:     Encoding(e[25]),
			width:   e[26],
			card:    binary.LittleEndian.Uint32(e[28:]),
			nullOff: binary.LittleEndian.Uint64(e[32:]),
			nullLen: binary.LittleEndian.Uint64(e[40:]),
			dataOff: binary.LittleEndian.Uint64(e[48:]),
			dataLen: binary.LittleEndian.Uint64(e[56:]),
			dictOff: binary.LittleEndian.Uint64(e[64:]),
			dictLen: binary.LittleEndian.Uint64(e[72:]),
		})
	}
	return s, nil
}

// NumRows returns the total row count.
func (s *Segment) NumRows() int { return s.rows }

// NumGroups returns the row group count.
func (s *Segment) NumGroups() int { return len(s.groups) }

// GroupStats returns the statistics of group i without decoding it.
func (s *Segment) GroupStats(i int) zone.Stats {
	m := s.groups[i]
	return zone.Stats{Rows: int(m.rows), Nulls: int(m.nulls), Min: m.min, Max: m.max, HasMinMax: m.hasMM}
}

// GroupEncoding returns the encoding of group i without decoding it.
func (s *Segment) GroupEncoding(i int) Encoding { return s.groups[i].enc }

// DecodeGroup fully decodes group i. Any length inconsistency yields a
// *CorruptError naming the group and stage; no partial rows are returned.
func (s *Segment) DecodeGroup(i int) ([]Value, error) {
	m := s.groups[i]
	n := int(m.rows)
	if m.width < 1 || m.width > 64 {
		return nil, &CorruptError{i, StageData, "bad bit width"}
	}
	if !fits(m.nullOff, m.nullLen, len(s.data)) || int(m.nullLen) != bitpack.ByteLen(n, 1) {
		return nil, &CorruptError{i, StageNullBitmap, "bad null bitmap"}
	}
	nulls := bitpack.Unpack(s.data[m.nullOff:m.nullOff+m.nullLen], 1, n)
	var vals []uint64
	switch m.enc {
	case Dict:
		if m.card == 0 || !fits(m.dictOff, m.dictLen, len(s.data)) || int(m.dictLen) != int(m.card)*8 {
			return nil, &CorruptError{i, StageData, "bad dictionary"}
		}
		if !fits(m.dataOff, m.dataLen, len(s.data)) || int(m.dataLen) != bitpack.ByteLen(n, m.width) {
			return nil, &CorruptError{i, StageData, "bad code block"}
		}
		codes := bitpack.Unpack(s.data[m.dataOff:m.dataOff+m.dataLen], m.width, n)
		vals = make([]uint64, n)
		for j, c := range codes {
			if c >= uint64(m.card) {
				return nil, &CorruptError{i, StageData, "code out of range"}
			}
			vals[j] = binary.LittleEndian.Uint64(s.data[m.dictOff+c*8:])
		}
	case Bitpack:
		if !fits(m.dataOff, m.dataLen, len(s.data)) || int(m.dataLen) != bitpack.ByteLen(n, m.width) {
			return nil, &CorruptError{i, StageData, "bad data block"}
		}
		deltas := bitpack.Unpack(s.data[m.dataOff:m.dataOff+m.dataLen], m.width, n)
		vals = make([]uint64, n)
		for j, d := range deltas {
			vals[j] = uint64(m.min) + d
		}
	default:
		return nil, &CorruptError{i, StageData, "unknown encoding"}
	}
	out := make([]Value, n)
	for j := range out {
		if nulls[j] == 1 {
			out[j] = Value{Null: true}
		} else {
			out[j] = Value{V: int64(vals[j])}
		}
	}
	return out, nil
}

func fits(off, length uint64, total int) bool {
	return off <= uint64(total) && length <= uint64(total)-off
}

// DecodeAll decodes every row of the segment in order.
func (s *Segment) DecodeAll() ([]Value, error) {
	out := make([]Value, 0, s.rows)
	for g := range s.groups {
		vals, err := s.DecodeGroup(g)
		if err != nil {
			return nil, err
		}
		out = append(out, vals...)
	}
	return out, nil
}

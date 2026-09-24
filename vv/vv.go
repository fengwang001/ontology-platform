package vv

import (
	"encoding/binary"
	"errors"
	"hash/crc32"
	"math"
	"sort"
)

const headerSize = 8

var (
	ErrOverflow           = errors.New("version vector counter overflow")
	ErrUnknownReplica     = errors.New("unknown replica id")
	ErrTruncatedHeader    = errors.New("truncated header")
	ErrTruncatedComponent  = errors.New("truncated component")
	ErrCRCMismatch        = errors.New("crc mismatch")
)

type Relation int

const (
	Equal Relation = iota
	Less
	Greater
	Concurrent
)

func (r Relation) String() string {
	switch r {
	case Equal:
		return "equal"
	case Less:
		return "less"
	case Greater:
		return "greater"
	default:
		return "concurrent"
	}
}

type Vector map[string]uint64

func New(entries map[string]uint64) Vector {
	v := make(Vector, len(entries))
	for id, count := range entries {
		if count != 0 {
			v[id] = count
		}
	}
	return v
}

func (v Vector) Clone() Vector {
	clone := make(Vector, len(v))
	for id, count := range v {
		clone[id] = count
	}
	return clone
}

func (v Vector) Get(id string) uint64 { return v[id] }

func (v Vector) Increment(id string) error {
	if v[id] == math.MaxUint64 {
		return ErrOverflow
	}
	v[id]++
	return nil
}

func IDs(vectors ...Vector) []string {
	seen := make(map[string]struct{})
	for _, vector := range vectors {
		for id := range vector {
			seen[id] = struct{}{}
		}
	}
	ids := make([]string, 0, len(seen))
	for id := range seen {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

func Compare(a, b Vector) Relation {
	relation, _ := CompareCounted(a, b)
	return relation
}

func CompareCounted(a, b Vector) (Relation, int) {
	less, greater, comparisons := false, false, 0
	for _, id := range IDs(a, b) {
		av, bv := a[id], b[id]
		comparisons++
		if av < bv {
			less = true
		} else if av > bv {
			greater = true
		}
	}
	switch {
	case less && greater:
		return Concurrent, comparisons
	case less:
		return Less, comparisons
	case greater:
		return Greater, comparisons
	default:
		return Equal, comparisons
	}
}

func Encode(v Vector) []byte {
	ids := IDs(v)
	buf := make([]byte, headerSize)
	binary.BigEndian.PutUint32(buf[:4], 0x56564531)
	binary.BigEndian.PutUint32(buf[4:8], uint32(len(ids)))
	for _, id := range ids {
		idBytes := []byte(id)
		count := make([]byte, 8)
		binary.BigEndian.PutUint64(count, v[id])
		lenBuf := make([]byte, 4)
		binary.BigEndian.PutUint32(lenBuf, uint32(len(idBytes)))
		buf = append(buf, lenBuf...)
		buf = append(buf, idBytes...)
		buf = append(buf, count...)
	}
	checksum := make([]byte, 4)
	binary.BigEndian.PutUint32(checksum, crc32.ChecksumIEEE(buf))
	return append(buf, checksum...)
}

func Decode(data []byte, known func(string) bool) (Vector, error) {
	if len(data) < headerSize {
		return nil, ErrTruncatedHeader
	}
	body, checksumBytes := data[:len(data)-4], data[len(data)-4:]
	if crc32.ChecksumIEEE(body) != binary.BigEndian.Uint32(checksumBytes) {
		return nil, ErrCRCMismatch
	}
	count := binary.BigEndian.Uint32(data[4:8])
	pos := headerSize
	v := make(Vector, count)
	for range count {
		if pos+4 > len(body) {
			return nil, ErrTruncatedComponent
		}
		idLen := int(binary.BigEndian.Uint32(body[pos : pos+4]))
		pos += 4
		if pos+idLen+8 > len(body) {
			return nil, ErrTruncatedComponent
		}
		id := string(body[pos : pos+idLen])
		pos += idLen
		if known != nil && !known(id) {
			return nil, ErrUnknownReplica
		}
		value := binary.BigEndian.Uint64(body[pos : pos+8])
		pos += 8
		if value != 0 {
			v[id] = value
		}
	}
	return v, nil
}

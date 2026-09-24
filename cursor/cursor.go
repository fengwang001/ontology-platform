// Package cursor 实现不透明分页游标的编码、解码、校验与方向标记。
package cursor

import (
	"encoding/binary"
	"errors"
	"hash/fnv"
	"math"

	"ontology/row"
)

// 四类可经 errors.Is 区分的哨兵错误。
var (
	// ErrChecksum：校验和不匹配（游标被篡改）。
	ErrChecksum = errors.New("cursor: checksum mismatch")
	// ErrIncomplete：字段不完整或长度越界（游标被截断/破坏）。
	ErrIncomplete = errors.New("cursor: incomplete fields")
	// ErrDirection：方向标记非法。
	ErrDirection = errors.New("cursor: invalid direction marker")
	// ErrMismatch：游标方向与翻页方向不一致（跨方向复用）。
	ErrMismatch = errors.New("cursor: direction mismatch")
)

const (
	version  byte = 1
	dirFwd        = 1
	dirBack       = 2
	hdrLen        = 1 + 1
	scoreLen      = 8
	lenLen        = 2
	sumLen        = 4
)

// Direction 标记游标产生的翻页方向。
type Direction byte

const (
	Forward  Direction = dirFwd
	Backward Direction = dirBack
)

// Cursor 是游标解码后的内容：方向 + 本页最后一个键 + 本页第一个键。
type Cursor struct {
	Dir   Direction
	Last  row.Row
	First row.Row
}

func appendKey(b []byte, r row.Row) []byte {
	var sb [scoreLen]byte
	binary.BigEndian.PutUint64(sb[:], math.Float64bits(r.Score))
	var lb [lenLen]byte
	binary.BigEndian.PutUint16(lb[:], uint16(len(r.ID)))
	b = append(b, sb[:]...)
	b = append(b, lb[:]...)
	b = append(b, r.ID...)
	return b
}

// Encode 把游标编码为不透明字节串（内含 FNV-1a 校验和）。
func Encode(c Cursor) ([]byte, error) {
	if c.Dir != Forward && c.Dir != Backward {
		return nil, ErrDirection
	}
	if !c.Last.Valid() || !c.First.Valid() {
		return nil, ErrIncomplete
	}
	if len(c.Last.ID) > math.MaxUint16 || len(c.First.ID) > math.MaxUint16 {
		return nil, ErrIncomplete
	}
	b := make([]byte, 0, hdrLen+2*(scoreLen+lenLen)+len(c.Last.ID)+len(c.First.ID)+sumLen)
	b = append(b, version, byte(c.Dir))
	b = appendKey(b, c.Last)
	b = appendKey(b, c.First)
	h := fnv.New32a()
	h.Write(b)
	var sum [sumLen]byte
	binary.BigEndian.PutUint32(sum[:], h.Sum32())
	return append(b, sum[:]...), nil
}

func readKey(b []byte, at int) (r row.Row, next int, err error) {
	if at+scoreLen+lenLen > len(b) {
		return row.Row{}, 0, ErrIncomplete
	}
	score := math.Float64frombits(binary.BigEndian.Uint64(b[at:]))
	n := int(binary.BigEndian.Uint16(b[at+scoreLen:]))
	at += scoreLen + lenLen
	if n < 0 || at+n > len(b) {
		return row.Row{}, 0, ErrIncomplete
	}
	r = row.Row{Score: score, ID: string(b[at : at+n])}
	if !r.Valid() {
		return row.Row{}, 0, ErrIncomplete
	}
	return r, at + n, nil
}

// Decode 解码字节串。空字节串合法，表示从头开始（Dir=Forward 的零游标）。
func Decode(b []byte) (Cursor, error) {
	if len(b) == 0 {
		return Cursor{Dir: Forward}, nil
	}
	if len(b) < hdrLen+2*(scoreLen+lenLen)+sumLen {
		return Cursor{}, ErrIncomplete
	}
	if b[0] != version {
		return Cursor{}, ErrIncomplete
	}
	var c Cursor
	var err error
	at := hdrLen
	if c.Last, at, err = readKey(b, at); err != nil {
		return Cursor{}, err
	}
	if c.First, at, err = readKey(b, at); err != nil {
		return Cursor{}, err
	}
	if at+sumLen != len(b) {
		return Cursor{}, ErrIncomplete
	}
	switch d := b[1]; d {
	case dirFwd, dirBack:
		c.Dir = Direction(d)
	default:
		return Cursor{}, ErrDirection
	}
	want := binary.BigEndian.Uint32(b[at:])
	h := fnv.New32a()
	h.Write(b[:at])
	if h.Sum32() != want {
		return Cursor{}, ErrChecksum
	}
	return c, nil
}

// EnsureDir 校验游标方向与请求方向一致；空游标仅允许正向。
func EnsureDir(c Cursor, want Direction) error {
	if c.Dir != want {
		return ErrMismatch
	}
	return nil
}

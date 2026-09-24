// Package resume encodes and decodes walk checkpoints into a deterministic,
// checksummed byte form, classifying tampered input by failure kind.
package resume

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"

	"ontology/graph"
	"ontology/walk"
)

// Decode error classes, distinguishable with errors.Is.
var (
	ErrIncomplete  = errors.New("resume: incomplete or malformed fields")
	ErrUnknownNode = errors.New("resume: checkpoint references unknown node")
	ErrChecksum    = errors.New("resume: checksum mismatch")
)

var magic = []byte{'W', 'C', '1'}

// Encode serializes c deterministically: magic, visited IDs, ready IDs,
// pending cursors, then a CRC32 over everything before it.
func Encode(c walk.Checkpoint) []byte {
	buf := append([]byte(nil), magic...)
	buf = binary.AppendUvarint(buf, uint64(len(c.Visited)))
	for _, id := range c.Visited {
		buf = appendStr(buf, id)
	}
	buf = binary.AppendUvarint(buf, uint64(len(c.Ready)))
	for _, id := range c.Ready {
		buf = appendStr(buf, id)
	}
	buf = binary.AppendUvarint(buf, uint64(len(c.Pending)))
	for _, cur := range c.Pending {
		buf = appendStr(buf, cur.Node)
		buf = binary.AppendUvarint(buf, uint64(cur.Edge))
	}
	return binary.BigEndian.AppendUint32(buf, crc32.ChecksumIEEE(buf))
}

func appendStr(b []byte, s string) []byte {
	b = binary.AppendUvarint(b, uint64(len(s)))
	return append(b, s...)
}

// Decode parses data back into a checkpoint. It fails fast in three stages:
// structural parse (ErrIncomplete), node-existence check against g
// (ErrUnknownNode), and integrity check (ErrChecksum).
func Decode(data []byte, g *graph.Graph) (walk.Checkpoint, error) {
	c, err := parse(data)
	if err != nil {
		return walk.Checkpoint{}, err
	}
	ids := append(append([]string(nil), c.Visited...), c.Ready...)
	for _, cur := range c.Pending {
		ids = append(ids, cur.Node)
	}
	for _, id := range ids {
		if !g.Has(id) {
			return walk.Checkpoint{}, fmt.Errorf("%w: %q", ErrUnknownNode, id)
		}
	}
	body, sum := data[:len(data)-4], binary.BigEndian.Uint32(data[len(data)-4:])
	if crc32.ChecksumIEEE(body) != sum {
		return walk.Checkpoint{}, ErrChecksum
	}
	return c, nil
}

func parse(data []byte) (walk.Checkpoint, error) {
	var c walk.Checkpoint
	if len(data) < len(magic)+4 || !bytes.Equal(data[:len(magic)], magic) {
		return c, ErrIncomplete
	}
	r := &reader{buf: data[:len(data)-4], off: len(magic)}
	var err error
	readIDs := func(dst *[]string) bool {
		n, ok := r.count()
		if !ok {
			return false
		}
		for i := uint64(0); i < n; i++ {
			id, ok := r.str()
			if !ok {
				return false
			}
			*dst = append(*dst, id)
		}
		return true
	}
	if !readIDs(&c.Visited) || !readIDs(&c.Ready) {
		return c, ErrIncomplete
	}
	n, ok := r.count()
	if !ok {
		return c, ErrIncomplete
	}
	for i := uint64(0); i < n; i++ {
		id, ok1 := r.str()
		edge, ok2 := r.uvarint()
		if !ok1 || !ok2 {
			return c, ErrIncomplete
		}
		c.Pending = append(c.Pending, walk.Cursor{Node: id, Edge: int(edge)})
	}
	if r.off != len(r.buf) || err != nil {
		return c, ErrIncomplete
	}
	return c, nil
}

type reader struct {
	buf []byte
	off int
}

func (r *reader) uvarint() (uint64, bool) {
	v, n := binary.Uvarint(r.buf[r.off:])
	if n <= 0 {
		return 0, false
	}
	r.off += n
	return v, true
}

// count reads a length prefix, rejecting values that cannot fit in the
// remaining bytes (each element needs at least one length byte).
func (r *reader) count() (uint64, bool) {
	n, ok := r.uvarint()
	if !ok || n > uint64(len(r.buf)-r.off) {
		return 0, false
	}
	return n, true
}

func (r *reader) str() (string, bool) {
	n, ok := r.uvarint()
	if !ok || n > uint64(len(r.buf)-r.off) {
		return "", false
	}
	s := string(r.buf[r.off : r.off+int(n)])
	r.off += int(n)
	return s, true
}

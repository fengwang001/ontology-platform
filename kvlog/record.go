// Package kvlog implements an append-only log-structured key/value engine
// with crash recovery, hint files, on-read self-healing and segment merging.
package kvlog

import (
	"encoding/binary"
	"errors"
	"hash/crc32"
)

// Record layout (all integers little-endian):
//
//	offset  size  field
//	0       4     CRC32-Castagnoli over bytes [4:headerLen+klen+vlen]
//	4       8     global write sequence number (strictly increasing, >= 1)
//	12      4     key length
//	16      4     value length
//	20      1     flags: 1 = tombstone (delete marker), value length must be 0
//	21      klen  key bytes (non-empty)
//	21+klen vlen  value bytes (empty for tombstones)
const (
	recordHeaderLen = 21

	flagTombstone byte = 1

	maxKeyLen   = 1 << 24
	maxValueLen = 1 << 28
)

var crcTable = crc32.MakeTable(crc32.Castagnoli)

// putUint helpers -----------------------------------------------------------

func putU64(b []byte, v uint64) { binary.LittleEndian.PutUint64(b, v) }
func putU32(b []byte, v uint32) { binary.LittleEndian.PutUint32(b, v) }
func u64(b []byte) uint64       { return binary.LittleEndian.Uint64(b) }
func u32(b []byte) uint32       { return binary.LittleEndian.Uint32(b) }

// record is an in-memory representation of one log entry.
type record struct {
	seq   uint64
	key   []byte
	value []byte
	tomb  bool
}

// encodedLen returns the on-disk size of the record.
func (r *record) encodedLen() int {
	return recordHeaderLen + len(r.key) + len(r.value)
}

// appendTo encodes the record into dst and returns the extended slice.
func (r *record) appendTo(dst []byte) []byte {
	total := recordHeaderLen + len(r.key) + len(r.value)
	start := len(dst)
	buf := make([]byte, total)
	putU64(buf[4:], r.seq)
	putU32(buf[12:], uint32(len(r.key)))
	putU32(buf[16:], uint32(len(r.value)))
	if r.tomb {
		buf[20] = flagTombstone
	}
	copy(buf[recordHeaderLen:], r.key)
	copy(buf[recordHeaderLen+len(r.key):], r.value)
	crc := crc32.Checksum(buf[4:], crcTable)
	putU32(buf[0:], crc)
	return append(dst, buf...)[:start+total]
}

// frame describes one record located inside a segment byte slice.
type frame struct {
	offset int
	length int
	rec    record
}

// scanOutcome classifies the result of scanning segment bytes.
type scanOutcome int

const (
	// scanOK means every byte belongs to a well-formed, verified record.
	scanOK scanOutcome = iota
	// scanCorrupt means a non-tail record failed verification.
	scanCorrupt
	// scanTornTail means only the final record is incomplete or fails
	// verification; validBytes is the truncation point.
	scanTornTail
)

type scanResult struct {
	outcome    scanOutcome
	frames     []frame
	validBytes int
	badOffset  int
}

// scanRecords parses data from offset zero. When allowTornTail is false a
// malformed final record is reported as corruption (sealed segments); when
// true it is reported as a torn tail (active segment only).
func scanRecords(data []byte, allowTornTail bool) scanResult {
	res := scanResult{}
	off := 0
	for off < len(data) {
		if len(data)-off < recordHeaderLen {
			if allowTornTail {
				res.outcome = scanTornTail
				res.validBytes = off
				return res
			}
			res.outcome = scanCorrupt
			res.badOffset = off
			return res
		}
		hdr := data[off : off+recordHeaderLen]
		klen := int(u32(hdr[12:]))
		vlen := int(u32(hdr[16:]))
		flags := hdr[20]
		total := recordHeaderLen + klen + vlen
		if total > len(data)-off {
			// Declared length runs past the end of the segment.
			if allowTornTail {
				res.outcome = scanTornTail
				res.validBytes = off
				return res
			}
			res.outcome = scanCorrupt
			res.badOffset = off
			return res
		}
		if klen == 0 || klen > maxKeyLen || vlen > maxValueLen ||
			(flags&^flagTombstone) != 0 ||
			(flags&flagTombstone != 0 && vlen != 0) {
			res.outcome = scanCorrupt
			res.badOffset = off
			return res
		}
		chunk := data[off : off+total]
		if u32(chunk[0:]) != crc32.Checksum(chunk[4:], crcTable) {
			if allowTornTail {
				res.outcome = scanTornTail
				res.validBytes = off
				return res
			}
			res.outcome = scanCorrupt
			res.badOffset = off
			return res
		}
		f := frame{
			offset: off,
			length: total,
			rec: record{
				seq:   u64(chunk[4:]),
				key:   append([]byte(nil), chunk[recordHeaderLen:recordHeaderLen+klen]...),
				value: append([]byte(nil), chunk[recordHeaderLen+klen:]...),
				tomb:  flags&flagTombstone != 0,
			},
		}
		res.frames = append(res.frames, f)
		off += total
	}
	res.validBytes = off
	res.outcome = scanOK
	return res
}

// errEmptyKey is used internally while building records.
var errEmptyKey = errors.New("kvlog: key must be non-empty")

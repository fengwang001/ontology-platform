package kvlog

import (
	"bytes"
	"hash/crc32"
)

// Hint file layout:

//	magic   = "KVLH"            4 bytes
//	version = 1                 1 byte
//	segment id                 4 bytes LE
//	valid bytes                8 bytes LE
//	count of key entries       4 bytes LE
//	entries, each:
//	  seq                       8 bytes LE
//	  offset                    8 bytes LE
//	  length                    4 bytes LE
//	  flags (1 = tombstone)     1 byte
//	  key length                4 bytes LE
//	  key bytes                 klen
//	checksum (CRC32C of all preceding bytes)  4 bytes LE
//
// Hint entries never contain values: adopting a hint costs O(distinct keys)
// and never reads the segment data.

var hintMagic = []byte("KVLH")

const hintVersion byte = 1

// hintEntry points at the newest record of one key within one segment.
type hintEntry struct {
	seq    uint64
	offset int64
	length int
	tomb   bool
	key    []byte
}

type hintFile struct {
	segmentID  int
	validBytes int64
	entries    []hintEntry
}

func encodeHint(h *hintFile) []byte {
	size := len(hintMagic) + 1 + 4 + 8 + 4
	for _, e := range h.entries {
		size += 8 + 8 + 4 + 1 + 4 + len(e.key)
	}
	buf := make([]byte, 0, size+4)
	buf = append(buf, hintMagic...)
	buf = append(buf, hintVersion)
	buf = u32append(buf, uint32(h.segmentID))
	buf = u64append(buf, uint64(h.validBytes))
	buf = u32append(buf, uint32(len(h.entries)))
	for _, e := range h.entries {
		buf = u64append(buf, e.seq)
		buf = u64append(buf, uint64(e.offset))
		buf = u32append(buf, uint32(e.length))
		var flags byte
		if e.tomb {
			flags = 1
		}
		buf = append(buf, flags)
		buf = u32append(buf, uint32(len(e.key)))
		buf = append(buf, e.key...)
	}
	buf = u32append(buf, crc32.Checksum(buf, crcTable))
	return buf
}

// decodeHint parses a hint file. A malformed or tampered hint returns
// (nil, false) — callers must fall back to a full segment scan.
func decodeHint(raw []byte, wantSegmentID int) (*hintFile, bool) {
	if len(raw) < 4+1+4+8+4+4 {
		return nil, false
	}
	payload, sum := raw[:len(raw)-4], u32(raw[len(raw)-4:])
	if crc32.Checksum(payload, crcTable) != sum {
		return nil, false
	}
	p := 0
	if !bytes.Equal(payload[p:p+4], hintMagic) {
		return nil, false
	}
	p += 4
	if payload[p] != hintVersion {
		return nil, false
	}
	p++
	segID := int(u32(payload[p:]))
	p += 4
	if segID != wantSegmentID {
		return nil, false
	}
	h := &hintFile{segmentID: segID}
	h.validBytes = int64(u64(payload[p:]))
	p += 8
	count := int(u32(payload[p:]))
	p += 4
	for i := 0; i < count; i++ {
		if p+8+8+4+1+4 > len(payload) {
			return nil, false
		}
		e := hintEntry{}
		e.seq = u64(payload[p:])
		p += 8
		e.offset = int64(u64(payload[p:]))
		p += 8
		e.length = int(u32(payload[p:]))
		p += 4
		flags := payload[p]
		p++
		if flags&^1 != 0 {
			return nil, false
		}
		e.tomb = flags&1 != 0
		klen := int(u32(payload[p:]))
		p += 4
		if klen == 0 || p+klen > len(payload) {
			return nil, false
		}
		e.key = append([]byte(nil), payload[p:p+klen]...)
		p += klen
		if e.seq == 0 {
			return nil, false
		}
		h.entries = append(h.entries, e)
	}
	if p != len(payload) {
		return nil, false
	}
	return h, true
}

func u32append(b []byte, v uint32) []byte {
	var buf [4]byte
	putU32(buf[:], v)
	return append(b, buf[:]...)
}

func u64append(b []byte, v uint64) []byte {
	var buf [8]byte
	putU64(buf[:], v)
	return append(b, buf[:]...)
}

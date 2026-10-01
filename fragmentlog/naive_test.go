package fragmentlog

import (
	"encoding/binary"
	"fmt"
	"hash/crc32"
)

// This file holds deliberately naive, spec-literal reference
// implementations used to cross-check the real Writer and Reader.

// naiveAppend appends one record to buf following the fragmentation rules
// and returns the new buffer plus the record's first-fragment offset.
func naiveAppend(blockSize int, buf []byte, rec []byte) ([]byte, int64) {
	rem := blockSize - len(buf)%blockSize
	if rem < HeaderSize {
		buf = append(buf, make([]byte, rem)...)
		rem = blockSize
	}
	start := int64(len(buf))
	written := 0
	for first := true; first || written < len(rec); {
		isFirst := first
		first = false
		if rem == 0 {
			rem = blockSize
		}
		n := rem - HeaderSize
		if n > len(rec)-written {
			n = len(rec) - written
		}
		var typ byte
		switch {
		case isFirst && written+n == len(rec):
			typ = TypeFull
		case isFirst:
			typ = TypeFirst
		case written+n == len(rec):
			typ = TypeLast
		default:
			typ = TypeMiddle
		}
		hdr := make([]byte, HeaderSize)
		binary.LittleEndian.PutUint32(hdr[0:4],
			crc32.ChecksumIEEE(append([]byte{typ}, rec[written:written+n]...)))
		binary.LittleEndian.PutUint16(hdr[4:6], uint16(n))
		hdr[6] = typ
		buf = append(buf, hdr...)
		buf = append(buf, rec[written:written+n]...)
		written += n
		rem -= HeaderSize + n
	}
	return buf, start
}

// event is one step of a full read: a delivered record, a corruption
// error, or the final EOF.
type event struct {
	kind string // "record", "error", "eof"
	rec  []byte
	off  int64
	cls  error // corruption class for "error" events
	why  string
}

func (e event) String() string {
	switch e.kind {
	case "record":
		return fmt.Sprintf("record(off=%d len=%d)", e.off, len(e.rec))
	case "error":
		return fmt.Sprintf("error(%v off=%d: %s)", e.cls, e.off, e.why)
	default:
		return "eof"
	}
}

func eventsEqual(a, b event) bool {
	if a.kind != b.kind || a.off != b.off || a.cls != b.cls {
		return false
	}
	if a.kind == "record" {
		if len(a.rec) != len(b.rec) {
			return false
		}
		for i := range a.rec {
			if a.rec[i] != b.rec[i] {
				return false
			}
		}
	}
	return true
}

// resyncPos returns the next block boundary after hdrOff's block, clamped
// to the stream end (a Reader skipping bytes stops at EOF).
func resyncPos(blockSize int, hdrOff, length int) int {
	pos := (hdrOff/blockSize + 1) * blockSize
	if pos > length {
		pos = length
	}
	return pos
}

// naiveReadAll replays the read-side rules directly over a byte slice and
// returns the full event sequence, ending with an "eof" event.
func naiveReadAll(blockSize int, data []byte) []event {
	var events []event
	pos := 0
	var asm []byte
	inRec := false
	asmStart := 0
	for {
		rem := blockSize - pos%blockSize
		if rem < HeaderSize {
			// Padding: skip it; running out of bytes here ends the stream.
			if pos+rem > len(data) {
				pos = len(data)
				if inRec {
					events = append(events, event{kind: "error", cls: ErrTruncated,
						off: int64(pos), why: "stream ends in padding with record unfinished"})
				}
				events = append(events, event{kind: "eof"})
				return events
			}
			pos += rem
			continue
		}
		if pos+HeaderSize > len(data) {
			// Fewer than a header's bytes remain.
			if pos == len(data) {
				if inRec {
					events = append(events, event{kind: "error", cls: ErrTruncated,
						off: int64(pos), why: "stream ends with record unfinished"})
				}
				events = append(events, event{kind: "eof"})
				return events
			}
			events = append(events, event{kind: "error", cls: ErrTruncated,
				off: int64(pos), why: "partial fragment header"})
			events = append(events, event{kind: "eof"})
			return events
		}
		hdrOff := pos
		length := int(binary.LittleEndian.Uint16(data[pos+4 : pos+6]))
		typ := data[pos+6]
		// 1. Length overflow, judged from the header alone.
		if HeaderSize+length > rem {
			events = append(events, event{kind: "error", cls: ErrLength, off: int64(hdrOff),
				why: fmt.Sprintf("length %d exceeds %d bytes left in block", length, rem-HeaderSize)})
			pos = resyncPos(blockSize, hdrOff, len(data))
			inRec, asm = false, nil
			continue
		}
		// 2. Data bytes missing: truncation.
		if pos+HeaderSize+length > len(data) {
			events = append(events, event{kind: "error", cls: ErrTruncated, off: int64(hdrOff),
				why: "data bytes missing"})
			events = append(events, event{kind: "eof"})
			return events
		}
		frag := data[pos+HeaderSize : pos+HeaderSize+length]
		pos += HeaderSize + length
		// 3. Checksum.
		want := binary.LittleEndian.Uint32(data[hdrOff : hdrOff+4])
		got := crc32.ChecksumIEEE(append([]byte{typ}, frag...))
		if want != got {
			events = append(events, event{kind: "error", cls: ErrChecksum, off: int64(hdrOff),
				why: "crc mismatch"})
			pos = resyncPos(blockSize, hdrOff, len(data))
			inRec, asm = false, nil
			continue
		}
		// 4. Type sequence.
		bad := ""
		switch {
		case typ < TypeFull || typ > TypeLast:
			bad = fmt.Sprintf("type %d out of range", typ)
		case inRec && (typ == TypeFull || typ == TypeFirst):
			bad = "new record while previous unfinished"
		case !inRec && (typ == TypeMiddle || typ == TypeLast):
			bad = "middle/last without first"
		}
		if bad != "" {
			events = append(events, event{kind: "error", cls: ErrSequence, off: int64(hdrOff), why: bad})
			pos = resyncPos(blockSize, hdrOff, len(data))
			inRec, asm = false, nil
			continue
		}
		switch typ {
		case TypeFull:
			events = append(events, event{kind: "record", rec: append([]byte(nil), frag...), off: int64(hdrOff)})
		case TypeFirst:
			inRec, asmStart = true, hdrOff
			asm = append([]byte(nil), frag...)
		case TypeMiddle:
			asm = append(asm, frag...)
		case TypeLast:
			events = append(events, event{kind: "record", rec: append(asm, frag...), off: int64(asmStart)})
			inRec, asm = false, nil
		}
	}
}

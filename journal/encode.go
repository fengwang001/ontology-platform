package journal

import (
	"encoding/binary"
	"hash/crc32"
)

// Frame layout on the byte log:
//
//	uvarint payloadLen | payload | uint32 big-endian CRC32(payload)
//
// payload layout:
//
//	uvarint seq | byte phase | uvarint len + stepID | uvarint len + note
//
// A crash can tear a frame at any byte. On replay a frame is accepted only
// if it is fully present and its CRC matches; anything else at the tail is
// treated as a torn write and discarded.

func encodeRecord(r Record) []byte {
	var payload []byte
	payload = binary.AppendUvarint(payload, r.Seq)
	payload = append(payload, byte(r.Phase))
	payload = binary.AppendUvarint(payload, uint64(len(r.StepID)))
	payload = append(payload, r.StepID...)
	payload = binary.AppendUvarint(payload, uint64(len(r.Note)))
	payload = append(payload, r.Note...)

	var frame []byte
	frame = binary.AppendUvarint(frame, uint64(len(payload)))
	frame = append(frame, payload...)
	var crc [4]byte
	binary.BigEndian.PutUint32(crc[:], crc32.ChecksumIEEE(payload))
	frame = append(frame, crc[:]...)
	return frame
}

// decodeFrame parses one frame at the head of buf. It returns the record,
// the number of bytes consumed, and whether a complete valid frame was
// found. ok=false means the tail is torn or corrupt and must be dropped.
func decodeFrame(buf []byte) (rec Record, n int, ok bool) {
	payloadLen, hlen := binary.Uvarint(buf)
	if hlen <= 0 || payloadLen > uint64(len(buf)) {
		return rec, 0, false
	}
	total := hlen + int(payloadLen) + 4
	if len(buf) < total {
		return rec, 0, false
	}
	payload := buf[hlen : hlen+int(payloadLen)]
	crc := binary.BigEndian.Uint32(buf[hlen+int(payloadLen) : total])
	if crc != crc32.ChecksumIEEE(payload) {
		return rec, 0, false
	}
	rest := payload
	rec.Seq, rest = takeUvarint(rest)
	if rest == nil {
		return rec, 0, false
	}
	if len(rest) < 1 {
		return rec, 0, false
	}
	rec.Phase = Phase(rest[0])
	rest = rest[1:]
	if rec.StepID, rest = takeString(rest); rest == nil {
		return rec, 0, false
	}
	if rec.Note, rest = takeString(rest); rest == nil {
		return rec, 0, false
	}
	if len(rest) != 0 {
		return rec, 0, false
	}
	return rec, total, true
}

func takeUvarint(buf []byte) (uint64, []byte) {
	v, n := binary.Uvarint(buf)
	if n <= 0 {
		return 0, nil
	}
	return v, buf[n:]
}

func takeString(buf []byte) (string, []byte) {
	l, rest := takeUvarint(buf)
	if rest == nil || uint64(len(rest)) < l {
		return "", nil
	}
	return string(rest[:l]), rest[l:]
}

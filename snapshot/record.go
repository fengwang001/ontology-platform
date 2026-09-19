package snapshot

import (
	"encoding/binary"
	"fmt"
	"hash/crc32"
)

// Record body layout (version 2):
//
//	keyLen uint16 | key bytes | num uint64 | valueLen uint16 | value bytes
//
// Version 1 bodies omit the trailing valueLen/value part.
func encodeBody(version uint16, r Record) []byte {
	body := binary.BigEndian.AppendUint16(nil, uint16(len(r.Key)))
	body = append(body, r.Key...)
	body = binary.BigEndian.AppendUint64(body, uint64(r.Num))
	if version >= VersionV2 {
		body = binary.BigEndian.AppendUint16(body, uint16(len(r.Value)))
		body = append(body, r.Value...)
	}
	return body
}

func decodeBody(version uint16, body []byte) (Record, error) {
	var r Record
	if len(body) < 2 {
		return r, fmt.Errorf("body too short for key length: %d bytes", len(body))
	}
	keyLen := int(binary.BigEndian.Uint16(body))
	body = body[2:]
	if len(body) < keyLen {
		return r, fmt.Errorf("key length %d exceeds remaining body (%d bytes)", keyLen, len(body))
	}
	r.Key = string(body[:keyLen])
	body = body[keyLen:]
	if len(body) < 8 {
		return r, fmt.Errorf("body too short for numeric value: %d bytes left", len(body))
	}
	r.Num = int64(binary.BigEndian.Uint64(body))
	body = body[8:]
	if version >= VersionV2 {
		if len(body) < 2 {
			return r, fmt.Errorf("body too short for value length: %d bytes left", len(body))
		}
		valueLen := int(binary.BigEndian.Uint16(body))
		body = body[2:]
		if len(body) < valueLen {
			return r, fmt.Errorf("value length %d exceeds remaining body (%d bytes)", valueLen, len(body))
		}
		r.Value = string(body[:valueLen])
		body = body[valueLen:]
	}
	if len(body) != 0 {
		return r, fmt.Errorf("%d trailing bytes in record body", len(body))
	}
	return r, nil
}

// frameRecord wraps a body as: len uint32 | body | crc32(body).
func frameRecord(body []byte) []byte {
	frame := binary.BigEndian.AppendUint32(nil, uint32(len(body)))
	frame = append(frame, body...)
	return binary.BigEndian.AppendUint32(frame, crc32.ChecksumIEEE(body))
}

// parseRecordAt parses the framed record starting at region[off].
// It returns the record and the offset just past it. The returned
// error wraps one of ErrTruncated, ErrLengthOverflow,
// ErrRecordChecksum, or ErrRecordParse.
func parseRecordAt(region []byte, off int, version uint16) (Record, int, error) {
	remaining := len(region) - off
	if remaining < lenSize {
		return Record{}, 0, fmt.Errorf("%w: length prefix needs %d bytes, %d remain",
			ErrTruncated, lenSize, remaining)
	}
	bodyLen := int(binary.BigEndian.Uint32(region[off:]))
	if bodyLen > maxRecordSize {
		return Record{}, 0, fmt.Errorf("%w: length prefix claims %d bytes, max is %d",
			ErrLengthOverflow, bodyLen, maxRecordSize)
	}
	end := off + lenSize + bodyLen + crcSize
	if end > len(region) {
		return Record{}, 0, fmt.Errorf("%w: record spans %d bytes, only %d remain",
			ErrTruncated, lenSize+bodyLen+crcSize, remaining)
	}
	body := region[off+lenSize : off+lenSize+bodyLen]
	stored := binary.BigEndian.Uint32(region[off+lenSize+bodyLen:])
	if crc32.ChecksumIEEE(body) != stored {
		return Record{}, 0, fmt.Errorf("%w: stored crc %#08x", ErrRecordChecksum, stored)
	}
	rec, err := decodeBody(version, body)
	if err != nil {
		return Record{}, 0, fmt.Errorf("%w: %v", ErrRecordParse, err)
	}
	return rec, end, nil
}

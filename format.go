package snapshot

import "encoding/binary"

// Format constants.
const (
	// Magic identifies a snapshot file.
	Magic = "ONTSNAP1"
	// CurrentVersion is the version written by Write.
	CurrentVersion uint16 = 2
	// MinCompatibleVersion is the oldest version Read accepts.
	MinCompatibleVersion uint16 = 1

	magicLen   = 8
	versionLen = 2
	countLen   = 4
	crcLen     = 4
	// headerSize is the fixed byte size of magic+version+count+regionCRC.
	headerSize = magicLen + versionLen + countLen + crcLen

	lenPrefixLen = 4
	recCRCLen    = 4
	recFrameOver = lenPrefixLen + recCRCLen
	// maxBodySize rejects implausibly large length prefixes. A real snapshot
	// body claiming this many bytes far exceeds any remaining file.
	maxBodySize = 64 << 20
)

var byteOrder = binary.LittleEndian

// Record is one snapshot entry keyed by a primary key string.
type Record struct {
	Key   string
	Value int64
	Note  string // absent in version 1 files; defaults to ""
}

func putUint16(b []byte, v uint16) { byteOrder.PutUint16(b, v) }
func putUint32(b []byte, v uint32) { byteOrder.PutUint32(b, v) }
func putUint64(b []byte, v uint64) { byteOrder.PutUint64(b, v) }

func uint16At(b []byte) uint16 { return byteOrder.Uint16(b) }
func uint32At(b []byte) uint32 { return byteOrder.Uint32(b) }
func uint64At(b []byte) uint64 { return byteOrder.Uint64(b) }

// encodeBody serializes a record body for the given format version.
// Layout: keyLen(u32) key value(i64) [version>=2: noteLen(u32) note]
func encodeBody(r Record, version uint16) []byte {
	key := []byte(r.Key)
	note := []byte(r.Note)
	size := 4 + len(key) + 8
	if version >= 2 {
		size += 4 + len(note)
	}
	buf := make([]byte, size)
	putUint32(buf, uint32(len(key)))
	copy(buf[4:], key)
	putUint64(buf[4+len(key):], uint64(r.Value))
	if version >= 2 {
		base := 4 + len(key) + 8
		putUint32(buf[base:], uint32(len(note)))
		copy(buf[base+4:], note)
	}
	return buf
}

// decodeBody parses a record body according to the file version.
func decodeBody(body []byte, version uint16) (Record, error) {
	if len(body) < 12 {
		return Record{}, ErrRecordParse
	}
	keyLen := uint32At(body)
	if 4+int(keyLen)+8 > len(body) {
		return Record{}, ErrRecordParse
	}
	r := Record{
		Key:   string(body[4 : 4+keyLen]),
		Value: int64(uint64At(body[4+keyLen:])),
	}
	pos := 4 + int(keyLen) + 8
	if version >= 2 {
		if pos+4 > len(body) {
			return Record{}, ErrRecordParse
		}
		noteLen := uint32At(body[pos:])
		pos += 4
		if pos+int(noteLen) != len(body) {
			return Record{}, ErrRecordParse
		}
		r.Note = string(body[pos : pos+int(noteLen)])
	} else if pos != len(body) {
		return Record{}, ErrRecordParse
	}
	return r, nil
}

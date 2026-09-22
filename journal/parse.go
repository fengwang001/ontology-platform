package journal

import (
	"encoding/binary"

	"ontology/change"
)

// parse walks data frame by frame. It returns every fully verified record
// before the first damaged position together with the classified tail
// error. A complete, undamaged file returns a nil error.
func parse(data []byte) ([]change.Change, error) {
	if len(data) < HeaderLen {
		return nil, ErrHeaderIncomplete
	}
	if string(data[:8]) != string(magic[:]) {
		return nil, ErrBadMagic
	}
	if binary.LittleEndian.Uint64(data[8:16]) != FormatVersion {
		return nil, ErrUnsupportedVersion
	}

	pos := HeaderLen
	var out []change.Change
	for pos < len(data) {
		tail := data[pos:]
		if len(tail) < 4 {
			return out, ErrLengthPrefixIncomplete
		}
		bodyLen := int(binary.LittleEndian.Uint32(tail[:4]))
		bodyEnd := 4 + bodyLen
		crcEnd := bodyEnd + 4
		if len(tail) < bodyEnd {
			return out, ErrBodyIncomplete
		}
		if len(tail) < crcEnd {
			// Whole body present, checksum cut off: CRC cannot match.
			return out, ErrCRCMismatch
		}
		want := binary.LittleEndian.Uint32(tail[bodyEnd:crcEnd])
		if crcOf(tail[:bodyEnd]) != want {
			return out, ErrCRCMismatch
		}
		c, err := change.Decode(tail[4:bodyEnd])
		if err != nil {
			return out, ErrCRCMismatch
		}
		out = append(out, c)
		pos += crcEnd
	}
	return out, nil
}

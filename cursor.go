package ontology

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"math"
)

// Cursor wire format (before base64url encoding):
//
//	version(1) || sessionID(8, BE) || position(8, BE) || HMAC-SHA256 tag(16)
//
// The position is an index into the session's immutable key snapshot, so a
// cursor never contains any primary key in plaintext.
const (
	cursorVersion    = 1
	cursorPayloadLen = 17
	cursorTagLen     = 16
	cursorRawLen     = cursorPayloadLen + cursorTagLen
)

func (s *Store) encodeCursor(id uint64, pos int) string {
	payload := make([]byte, cursorPayloadLen)
	payload[0] = cursorVersion
	binary.BigEndian.PutUint64(payload[1:9], id)
	binary.BigEndian.PutUint64(payload[9:17], uint64(pos))
	tag := s.sign(payload)
	raw := append(payload, tag...)
	return base64.RawURLEncoding.EncodeToString(raw)
}

func (s *Store) decodeCursor(cursor string) (uint64, int, error) {
	raw, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil || len(raw) != cursorRawLen {
		return 0, 0, ErrInvalidCursor
	}
	payload, tag := raw[:cursorPayloadLen], raw[cursorPayloadLen:]
	if payload[0] != cursorVersion {
		return 0, 0, ErrInvalidCursor
	}
	if !hmac.Equal(tag, s.sign(payload)) {
		return 0, 0, ErrInvalidCursor
	}
	id := binary.BigEndian.Uint64(payload[1:9])
	pos := binary.BigEndian.Uint64(payload[9:17])
	if pos > math.MaxInt {
		return 0, 0, ErrInvalidCursor
	}
	return id, int(pos), nil
}

func (s *Store) sign(payload []byte) []byte {
	mac := hmac.New(sha256.New, s.macKey[:])
	mac.Write(payload)
	return mac.Sum(nil)[:cursorTagLen]
}

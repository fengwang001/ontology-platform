package ontology

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"errors"
)

// Sentinel errors returned by Scan. They are distinguishable with errors.Is.
var (
	// ErrInvalidCursor means the cursor string is forged, truncated,
	// tampered or otherwise structurally invalid.
	ErrInvalidCursor = errors.New("ontology: invalid cursor")
	// ErrSessionInvalid means the cursor is well-formed and authentic, but
	// the traversal session it addresses has been explicitly invalidated
	// (or never existed). This is distinct from ErrInvalidCursor.
	ErrSessionInvalid = errors.New("ontology: traversal session invalid")
	// ErrInvalidLimit means limit was negative.
	ErrInvalidLimit = errors.New("ontology: limit must not be negative")
)

const (
	cursorVersion = 1
	cursorIDLen   = 8
	cursorBodyLen = 1 + cursorIDLen + 8 // version + session id + index
	cursorMACLen  = sha256.Size
)

// newSecret returns a random per-store HMAC key so cursors cannot be forged
// without access to the running process.
func newSecret() []byte {
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		panic(err)
	}
	return secret
}

func sign(secret, body []byte) []byte {
	m := hmac.New(sha256.New, secret)
	m.Write(body)
	return m.Sum(nil)
}

// encodeCursor builds an opaque, authenticated cursor. The payload never
// contains object keys: only a random session id and a positional index.
func (s *Store) encodeCursor(sessionID uint64, index int) string {
	body := make([]byte, cursorBodyLen)
	body[0] = cursorVersion
	binary.BigEndian.PutUint64(body[1:1+cursorIDLen], sessionID)
	binary.BigEndian.PutUint64(body[1+cursorIDLen:], uint64(index))

	raw := make([]byte, 0, cursorBodyLen+cursorMACLen)
	raw = append(raw, body...)
	raw = append(raw, sign(s.secret, body)...)
	return base64.RawURLEncoding.EncodeToString(raw)
}

// decodeCursor validates the cursor and resolves its session.
// A malformed/tampered cursor yields ErrInvalidCursor; an authentic cursor
// pointing at an invalidated session yields ErrSessionInvalid.
func (s *Store) decodeCursor(token string) (*Session, int, error) {
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil || len(raw) != cursorBodyLen+cursorMACLen {
		return nil, 0, ErrInvalidCursor
	}
	body, mac := raw[:cursorBodyLen], raw[cursorBodyLen:]
	if !hmac.Equal(mac, sign(s.secret, body)) {
		return nil, 0, ErrInvalidCursor
	}
	if body[0] != cursorVersion {
		return nil, 0, ErrInvalidCursor
	}

	sessionID := binary.BigEndian.Uint64(body[1 : 1+cursorIDLen])
	index := int(binary.BigEndian.Uint64(body[1+cursorIDLen:]))

	s.sessions.mu.Lock()
	sess, ok := s.sessions.byID[sessionID]
	s.sessions.mu.Unlock()
	if !ok || sess.invalidated {
		// Authentic token, unusable session: a distinct error category.
		return nil, 0, ErrSessionInvalid
	}
	return sess, index, nil
}

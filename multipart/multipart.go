// Package multipart frames byte ranges as a multipart/byteranges body.
// It deliberately does not use mime/multipart: the framing is small
// enough to write by hand, and boundary safety needs direct control.
package multipart

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"

	"ontology/coalesce"
)

// boundaryPrefix makes generated boundaries easy to recognise in dumps.
const boundaryPrefix = "ontology-"

// boundaryRandomBytes is the number of random bytes in a boundary; hex
// encoded, that is 32 characters of entropy (128 bits).
const boundaryRandomBytes = 16

// NewBoundary returns a fresh random boundary token. r may be nil, in
// which case crypto/rand is used; tests pass a scripted reader.
func NewBoundary(r io.Reader) (string, error) {
	if r == nil {
		r = rand.Reader
	}
	var buf [boundaryRandomBytes]byte
	if _, err := io.ReadFull(r, buf[:]); err != nil {
		return "", fmt.Errorf("multipart: reading boundary randomness: %w", err)
	}
	return boundaryPrefix + hex.EncodeToString(buf[:]), nil
}

// Conflicts reports whether the boundary token appears anywhere in the
// payload bytes. Framing lines prepend "--" to the token, so any dangerous
// occurrence of the framed delimiter also contains the bare token; checking
// the bare token is therefore the conservative test.
func Conflicts(boundary string, payload []byte) bool {
	return bytes.Contains(payload, []byte(boundary))
}

// Build assembles the full multipart/byteranges body for the given ranges.
// payload must be the concatenation of the range contents in range order.
// size is the total resource length, used in Content-Range headers.
func Build(boundary string, rs []coalesce.Range, payload []byte, size int64, contentType string) []byte {
	var b bytes.Buffer
	off := 0
	for _, r := range rs {
		n := int(r.Len())
		fmt.Fprintf(&b, "--%s\r\n", boundary)
		fmt.Fprintf(&b, "Content-Type: %s\r\n", contentType)
		fmt.Fprintf(&b, "Content-Range: bytes %d-%d/%d\r\n", r.From, r.To, size)
		b.WriteString("\r\n")
		b.Write(payload[off : off+n])
		b.WriteString("\r\n")
		off += n
	}
	fmt.Fprintf(&b, "--%s--\r\n", boundary)
	return b.Bytes()
}

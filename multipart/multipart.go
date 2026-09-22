// Package multipart frames byte ranges as a multipart/byteranges body:
// boundary generation, per-part headers, and the closing boundary. It does
// no I/O; callers assemble the body in memory.
package multipart

import (
	"bytes"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strings"

	"ontology/coalesce"
)

// Boundary shape: BoundaryPrefix plus boundaryHex random hex characters.
// A fixed, documented length lets callers compute the exact framing size
// before a boundary has been generated.
const (
	BoundaryPrefix = "ontology-"
	boundaryHex    = 32
	// BoundaryLen is the exact length of every generated boundary.
	BoundaryLen = len(BoundaryPrefix) + boundaryHex
)

// ErrBoundaryExhausted reports that no collision-free boundary was found
// within the allowed number of attempts.
var ErrBoundaryExhausted = errors.New("multipart: boundary generation retries exhausted")

// ChooseBoundary returns a random boundary that does not occur anywhere in
// content, so the framing can never be mistaken for payload. It tries at
// most maxTries candidates drawn from rng.
func ChooseBoundary(content []byte, maxTries int, rng io.Reader) (string, error) {
	if maxTries < 1 {
		maxTries = 1
	}
	raw := make([]byte, boundaryHex/2)
	for try := 0; try < maxTries; try++ {
		if _, err := io.ReadFull(rng, raw); err != nil {
			return "", fmt.Errorf("multipart: boundary randomness: %w", err)
		}
		candidate := BoundaryPrefix + hex.EncodeToString(raw)
		if !bytes.Contains(content, []byte(candidate)) {
			return candidate, nil
		}
	}
	return "", ErrBoundaryExhausted
}

// AppendPartHeader appends one part's framing header to dst.
func AppendPartHeader(dst []byte, boundary string, r coalesce.Range, total int64) []byte {
	dst = append(dst, "--"...)
	dst = append(dst, boundary...)
	dst = append(dst, "\r\nContent-Type: application/octet-stream\r\n"...)
	return fmt.Appendf(dst, "Content-Range: bytes %d-%d/%d\r\n\r\n", r.First, r.Last, total)
}

// AppendFinal appends the closing boundary to dst.
func AppendFinal(dst []byte, boundary string) []byte {
	dst = append(dst, "--"...)
	dst = append(dst, boundary...)
	return append(dst, "--\r\n"...)
}

// Assemble builds the full multipart body from the concatenated content of
// the ranges (in list order).
func Assemble(boundary string, ranges []coalesce.Range, total int64, content []byte) []byte {
	var body []byte
	off := int64(0)
	for _, r := range ranges {
		body = AppendPartHeader(body, boundary, r, total)
		body = append(body, content[off:off+r.Len()]...)
		body = append(body, '\r', '\n')
		off += r.Len()
	}
	return AppendFinal(body, boundary)
}

// FramingSize returns the exact number of bytes Assemble adds on top of the
// raw content for a boundary of boundaryLen bytes. It lets callers enforce
// response size limits before reading any content.
func FramingSize(boundaryLen int, ranges []coalesce.Range, total int64) int64 {
	pad := strings.Repeat("\x00", boundaryLen)
	var n int64
	for _, r := range ranges {
		n += int64(len(AppendPartHeader(nil, pad, r, total))) + 2 // + trailing CRLF
	}
	return n + int64(len(AppendFinal(nil, pad)))
}

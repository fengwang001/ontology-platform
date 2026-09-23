// Package qp implements a Quoted-Printable (RFC 2045) encoder and a
// strict streaming decoder. Line-level rules come from package qpline.
package qp

import "ontology/qpline"

// Encode applies quoted-printable encoding. Every input newline ("\n"
// or "\r\n") is emitted as CRLF; a lone '\r' escapes as =0D. Lines
// (excluding CRLF) are at most 76 characters; the soft-break '=' counts
// toward that limit and an =XX escape is never split across lines.
func Encode(src []byte) []byte {
	e := &encoder{}
	start := 0
	for i := 0; i < len(src); i++ {
		e.checked++ // pass 1: line boundaries and trailing whitespace
		if src[i] != '\n' {
			continue
		}
		line := src[start:i]
		if n := len(line); n > 0 && line[n-1] == '\r' {
			line = line[:n-1]
		}
		e.out = append(e.out, qpline.EncodeLine(line, true)...)
		e.out = append(e.out, '\r', '\n')
		start = i + 1
	}
	// Pass 2 over every byte happens inside EncodeLine (one check each).
	e.out = append(e.out, qpline.EncodeLine(src[start:], true)...)
	return e.out
}

type encoder struct {
	out     []byte
	checked int // input bytes examined; invariant: checked <= 2*len(src)
}

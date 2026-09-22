package serve

import (
	"bytes"
	"io"
	"ontology/source"
	"testing"
)

// scriptRand replays 16-byte chunks in order, then repeats the last one.
type scriptRand struct {
	chunks [][]byte
	pos    int
}

func (s *scriptRand) Read(p []byte) (int, error) {
	idx := s.pos / 16
	if idx >= len(s.chunks) {
		idx = len(s.chunks) - 1
	}
	n := copy(p, s.chunks[idx][s.pos%16:])
	s.pos += n
	return n, nil
}

var _ io.Reader = (*scriptRand)(nil)

// TestBoundaryAvoidsPayloadContent builds a resource whose bytes contain
// something that looks exactly like a boundary token, forces the first
// generated boundary to be that token, and asserts the assembler retries
// and ends up with a non-conflicting boundary.
func TestBoundaryAvoidsPayloadContent(t *testing.T) {
	evil := []byte("EVILBOUNDARY0001") // 16 bytes -> first candidate token
	good := []byte("GOODBOUNDARY0002") // second candidate, not in payload
	evilToken := "ontology-" + hexOf(evil)
	goodToken := "ontology-" + hexOf(good)

	// Two non-adjacent ranges, each containing a full copy of evilToken.
	data := source.Bytes([]byte("<<" + evilToken + ">>" + evilToken + "<<"))
	header := "bytes=0-42, 45-87"
	rand := &scriptRand{chunks: [][]byte{evil, good}}
	a := assemble(t, Config{Rand: rand, MaxBoundaryTries: 4}, data, header)

	if a.Boundary() == evilToken {
		t.Fatal("boundary collides with payload content")
	}
	if a.Boundary() != goodToken {
		t.Fatalf("boundary = %q, want retry to pick %q", a.Boundary(), goodToken)
	}
	// The framed delimiter appears exactly as framing: 2 parts + closing.
	body := bodyOf(t, a)
	if bytes.Count(body, []byte("--"+a.Boundary())) != 3 {
		t.Fatal("boundary delimiter count wrong")
	}
	// The payload's look-alike token is still there, harmlessly framed.
	if !bytes.Contains(body, []byte(evilToken)) {
		t.Fatal("payload content must be preserved verbatim")
	}
}

func hexOf(b []byte) string {
	const digits = "0123456789abcdef"
	out := make([]byte, len(b)*2)
	for i, c := range b {
		out[i*2] = digits[c>>4]
		out[i*2+1] = digits[c&0xf]
	}
	return string(out)
}

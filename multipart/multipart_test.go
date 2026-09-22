package multipart

import (
	"bytes"
	"crypto/rand"
	"errors"
	"strings"
	"testing"

	"ontology/coalesce"
)

// fixedRand returns a reader that always yields the same bytes.
type fixedRand struct{ b byte }

func (r fixedRand) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = r.b
	}
	return len(p), nil
}

// seqRand yields a scripted first buffer, then crypto randomness.
type seqRand struct {
	first []byte
	done  bool
}

func (s *seqRand) Read(p []byte) (int, error) {
	if !s.done && len(p) <= len(s.first) {
		s.done = true
		return copy(p, s.first), nil
	}
	return rand.Read(p)
}

func TestChooseBoundaryAvoidsContent(t *testing.T) {
	// Content deliberately contains a boundary-looking string.
	trap := BoundaryPrefix + strings.Repeat("a", boundaryHex)
	content := []byte("payload --" + trap + "-- more payload")
	// Rig the first candidate to be exactly the trap, then fall back to
	// real randomness for the retry.
	rng := &seqRand{first: bytes.Repeat([]byte{0xaa}, boundaryHex/2)}
	b, err := ChooseBoundary(content, 4, rng)
	if err != nil {
		t.Fatalf("ChooseBoundary: %v", err)
	}
	if strings.Contains(string(content), b) {
		t.Fatalf("boundary %q occurs in content", b)
	}
	if len(b) != BoundaryLen {
		t.Fatalf("boundary length %d, want %d", len(b), BoundaryLen)
	}
}

func TestChooseBoundaryExhaustion(t *testing.T) {
	// Every candidate is 0x00...; make the content contain that exact
	// boundary so all attempts collide.
	trap := BoundaryPrefix + strings.Repeat("0", boundaryHex)
	_, err := ChooseBoundary([]byte(trap), 3, fixedRand{b: 0})
	if !errors.Is(err, ErrBoundaryExhausted) {
		t.Fatalf("err=%v, want ErrBoundaryExhausted", err)
	}
}

func TestAssembleLayout(t *testing.T) {
	ranges := []coalesce.Range{{First: 0, Last: 1}, {First: 5, Last: 6}}
	content := []byte("ABFG")
	body := Assemble("B", ranges, 10, content)
	want := "--B\r\n" +
		"Content-Type: application/octet-stream\r\n" +
		"Content-Range: bytes 0-1/10\r\n\r\n" +
		"AB\r\n" +
		"--B\r\n" +
		"Content-Type: application/octet-stream\r\n" +
		"Content-Range: bytes 5-6/10\r\n\r\n" +
		"FG\r\n" +
		"--B--\r\n"
	if string(body) != want {
		t.Fatalf("body:\n%q\nwant:\n%q", body, want)
	}
}

func TestFramingSizeMatchesAssemble(t *testing.T) {
	ranges := []coalesce.Range{{First: 0, Last: 1}, {First: 5, Last: 6}, {First: 1000, Last: 2000}}
	content := make([]byte, 2+2+1001)
	boundary := BoundaryPrefix + strings.Repeat("f", boundaryHex)
	body := Assemble(boundary, ranges, 99999, content)
	got := FramingSize(BoundaryLen, ranges, 99999)
	if int64(len(body)) != int64(len(content))+got {
		t.Fatalf("framing=%d, actual overhead=%d", got, len(body)-len(content))
	}
}

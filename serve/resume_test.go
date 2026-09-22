package serve

import (
	"bytes"
	"io"
	"testing"

	"ontology/source"
)

// chunkWriter accepts at most max bytes per Write call.
type chunkWriter struct {
	buf bytes.Buffer
	max int
}

func (w *chunkWriter) Write(p []byte) (int, error) {
	if len(p) > w.max {
		p = p[:w.max]
	}
	return w.buf.Write(p)
}

func drain(r *Response, w io.Writer) error {
	for {
		_, err := r.WriteSome(w)
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
	}
}

// zeroRand makes boundary generation deterministic so separately built
// responses are byte-identical and can be compared across chunkings.
type zeroRand struct{}

func (zeroRand) Read(p []byte) (int, error) { return len(p), nil }

// TestEverySplitPoint writes the same response with the output chunked at
// every possible size from 1 to the total length, and requires the result
// to be byte-identical to the one-shot write. A chunk boundary is exactly
// a write interruption, so this covers resuming from every breakpoint.
func TestEverySplitPoint(t *testing.T) {
	data := source.Bytes([]byte("the quick brown fox jumps over the lazy dog"))
	a := &Assembler{Src: data, Rand: zeroRand{}}
	const header = "bytes=0-9, 16-24, 30-42"
	r := buildOK(t, a, header)
	total := int(r.TotalBytes())

	var oneShot bytes.Buffer
	if err := drain(r, &oneShot); err != nil {
		t.Fatalf("one-shot: %v", err)
	}
	want := oneShot.Bytes()
	if r.Written() != int64(total) {
		t.Fatalf("written=%d, want %d", r.Written(), total)
	}

	for chunk := 1; chunk <= total; chunk++ {
		r2 := buildOK(t, a, header)
		w := &chunkWriter{max: chunk}
		if err := drain(r2, w); err != nil {
			t.Fatalf("chunk=%d: %v", chunk, err)
		}
		if !bytes.Equal(w.buf.Bytes(), want) {
			t.Fatalf("chunk=%d: bytes differ", chunk)
		}
		if r2.Written() != int64(total) {
			t.Fatalf("chunk=%d: written=%d", chunk, r2.Written())
		}
	}
}

// TestExplicitBreakpointResume interrupts the write at every possible
// breakpoint, switches to a different writer, and requires the
// concatenation to equal the uninterrupted stream.
func TestExplicitBreakpointResume(t *testing.T) {
	data := source.Bytes([]byte("0123456789abcdefghijklmnopqrstuvwxyz"))
	a := &Assembler{Src: data, Rand: zeroRand{}}
	const header = "bytes=0-5, 10-15, 20-35"

	var want bytes.Buffer
	if err := drain(buildOK(t, a, header), &want); err != nil {
		t.Fatal(err)
	}
	total := int(int64(len(want.Bytes())))

	for bp := 1; bp < total; bp++ {
		r := buildOK(t, a, header)
		// One bounded write: exactly the interruption at bp.
		w := &chunkWriter{max: bp}
		n, err := r.WriteSome(w)
		if err != nil {
			t.Fatalf("bp=%d: %v", bp, err)
		}
		if n != bp {
			t.Fatalf("bp=%d: first write n=%d", bp, n)
		}
		var second bytes.Buffer
		if err := drain(r, &second); err != nil {
			t.Fatalf("bp=%d resume: %v", bp, err)
		}
		got := append(w.buf.Bytes(), second.Bytes()...)
		if !bytes.Equal(got, want.Bytes()) {
			t.Fatalf("bp=%d: resumed stream differs", bp)
		}
	}
}

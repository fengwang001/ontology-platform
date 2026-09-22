package serve

import (
	"bytes"
	"errors"
	"ontology/source"
	"testing"
)

// fixedRand makes boundary generation deterministic across assemblers, so
// bodies assembled by different Assembler instances are comparable.
func fixedRand() *bytes.Reader {
	return bytes.NewReader([]byte("0123456789abcdef"))
}

// chunkWriter accepts at most max bytes per Write call, forcing the
// assembler to loop and resume.
type chunkWriter struct {
	max int
	buf bytes.Buffer
}

func (w *chunkWriter) Write(p []byte) (int, error) {
	if len(p) > w.max {
		p = p[:w.max]
	}
	return w.buf.Write(p)
}

// TestAllSplitPointsIdentical writes the same response with every possible
// chunk size from 1 to the total length and requires the produced byte
// sequence to be identical to the one-shot write in every case.
func TestAllSplitPointsIdentical(t *testing.T) {
	data := source.Bytes([]byte("0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZ"))
	header := "bytes=0-5, 10-19, 30-35"
	oneShot := assemble(t, Config{Rand: fixedRand()}, data, header)
	reference := bodyOf(t, oneShot)
	total := len(reference)
	if total < 10 {
		t.Fatalf("reference too small: %d", total)
	}
	for chunk := 1; chunk <= total; chunk++ {
		a := assemble(t, Config{Rand: fixedRand()}, data, header)
		w := &chunkWriter{max: chunk}
		for a.Written() < int64(total) {
			if _, err := a.WriteTo(w); err != nil {
				t.Fatalf("chunk=%d: WriteTo: %v", chunk, err)
			}
		}
		if !bytes.Equal(w.buf.Bytes(), reference) {
			t.Fatalf("chunk=%d: byte stream differs from one-shot", chunk)
		}
	}
}

// failAfterWriter accepts exactly budget bytes in total, then fails; used
// to prove resumption across different writers.
type failAfterWriter struct {
	budget int
	buf    bytes.Buffer
}

var errStop = bytes.ErrTooLarge // any distinguishable error

func (w *failAfterWriter) Write(p []byte) (int, error) {
	if w.budget == 0 {
		return 0, errStop
	}
	if len(p) > w.budget {
		p = p[:w.budget]
	}
	n, _ := w.buf.Write(p)
	w.budget -= n
	return n, nil
}

// TestResumeAcrossWriters stops mid-stream at every possible offset and
// resumes with a fresh writer; the concatenation must equal the one-shot.
func TestResumeAcrossWriters(t *testing.T) {
	data := source.Bytes([]byte("0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZ"))
	header := "bytes=0-5, 10-19, 30-35"
	reference := bodyOf(t, assemble(t, Config{Rand: fixedRand()}, data, header))
	for stop := 1; stop < len(reference); stop++ {
		a := assemble(t, Config{Rand: fixedRand()}, data, header)
		first := &failAfterWriter{budget: stop}
		if _, err := a.WriteTo(first); err != nil && !errors.Is(err, errStop) {
			t.Fatalf("stop=%d: first write: %v", stop, err)
		}
		var rest bytes.Buffer
		if _, err := a.WriteTo(&rest); err != nil {
			t.Fatalf("stop=%d: resume: %v", stop, err)
		}
		got := append(first.buf.Bytes(), rest.Bytes()...)
		if !bytes.Equal(got, reference) {
			t.Fatalf("stop=%d: resumed stream differs", stop)
		}
	}
}

// TestMergedRangesDegenerateToBareBytes: two requested ranges that are
// adjacent merge into one, and a single resulting range must be served as
// bare bytes, not multipart framing.
func TestMergedRangesDegenerateToBareBytes(t *testing.T) {
	data := source.Bytes([]byte("0123456789"))
	a := assemble(t, Config{}, data, "bytes=2-4, 5-7")
	if a.IsMultipart() {
		t.Fatal("ranges merged into one must not be multipart")
	}
	if got := string(bodyOf(t, a)); got != "234567" {
		t.Fatalf("got %q want %q", got, "234567")
	}
	if a.Boundary() != "" {
		t.Fatalf("bare body must have no boundary, got %q", a.Boundary())
	}
	rs := a.Ranges()
	if len(rs) != 1 || rs[0].From != 2 || rs[0].To != 7 {
		t.Fatalf("ranges = %v", rs)
	}
}

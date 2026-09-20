package chunked

import (
	"bytes"
	"errors"
	"testing"
)

// errSink returns a fixed error on the first write after failAt successful
// writes.
type errSink struct {
	buf    bytes.Buffer
	failAt int
	err    error
}

func (s *errSink) Write(p []byte) (int, error) {
	if s.failAt == 0 {
		return 0, s.err
	}
	s.failAt--
	return s.buf.Write(p)
}

// shortSink reports a successful short write.
type shortSink struct{ calls int }

func (s *shortSink) Write(p []byte) (int, error) {
	s.calls++
	if len(p) > 1 {
		return len(p) - 1, nil
	}
	return len(p), nil
}

// recorder counts sink writes and captures their bytes.
type recorder struct {
	bytes.Buffer
	calls int
}

func (r *recorder) Write(p []byte) (int, error) {
	r.calls++
	return r.Buffer.Write(p)
}

var errBoom = errors.New("boom")

func TestExactBytes(t *testing.T) {
	var sink bytes.Buffer
	w := New(&sink, 0)

	n, err := w.Write([]byte("hello"))
	if err != nil || n != 5 {
		t.Fatalf("Write = (%d, %v), want (5, nil)", n, err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	want := "5\r\nhello\r\n0\r\n\r\n"
	if got := sink.String(); got != want {
		t.Fatalf("wire bytes = %q, want %q", got, want)
	}
	if c := w.Chunks(); c != 1 {
		t.Fatalf("Chunks = %d, want 1", c)
	}
}

func TestLowercaseHexNoPadding(t *testing.T) {
	var sink bytes.Buffer
	w := New(&sink, 0)

	payload := bytes.Repeat([]byte{'x'}, 26)
	if _, err := w.Write(payload); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}

	want := "1a\r\n" + string(payload) + "\r\n0\r\n\r\n"
	if got := sink.String(); got != want {
		t.Fatalf("wire bytes = %q, want %q", got, want)
	}
}

func TestZeroLengthWriteProducesNoChunk(t *testing.T) {
	for _, p := range [][]byte{nil, {}} {
		var sink bytes.Buffer
		w := New(&sink, 4)

		n, err := w.Write(p)
		if n != 0 || err != nil {
			t.Fatalf("Write(%v) = (%d, %v), want (0, nil)", p, n, err)
		}
		if sink.Len() != 0 {
			t.Fatalf("sink received %q, want nothing", sink.String())
		}
		if w.Chunks() != 0 {
			t.Fatalf("Chunks = %d, want 0", w.Chunks())
		}
	}
}

func TestChunkingByLimit(t *testing.T) {
	var sink recorder
	w := New(&sink, 4)

	data := []byte("012345678")
	n, err := w.Write(data)
	if err != nil {
		t.Fatal(err)
	}
	if n != 9 {
		t.Fatalf("Write consumed %d, want 9", n)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}

	want := "4\r\n0123\r\n4\r\n4567\r\n1\r\n8\r\n0\r\n\r\n"
	if got := sink.String(); got != want {
		t.Fatalf("wire bytes = %q, want %q", got, want)
	}
	if c := w.Chunks(); c != 3 {
		t.Fatalf("Chunks = %d, want 3", c)
	}
}

func TestCloseIdempotent(t *testing.T) {
	var sink recorder
	w := New(&sink, 0)

	for i := 0; i < 3; i++ {
		if err := w.Close(); err != nil {
			t.Fatalf("Close #%d: %v", i+1, err)
		}
	}

	want := "0\r\n\r\n"
	if got := sink.String(); got != want {
		t.Fatalf("wire bytes = %q, want %q", got, want)
	}
	if sink.calls != 1 {
		t.Fatalf("sink writes = %d, want 1", sink.calls)
	}
}

func TestWriteAfterClose(t *testing.T) {
	var sink recorder
	w := New(&sink, 0)
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}

	n, err := w.Write([]byte("data"))
	if n != 0 || !errors.Is(err, ErrClosed) {
		t.Fatalf("Write after Close = (%d, %v), want (0, ErrClosed)", n, err)
	}
	if sink.calls != 1 {
		t.Fatalf("sink writes = %d, want only the terminal chunk", sink.calls)
	}
}

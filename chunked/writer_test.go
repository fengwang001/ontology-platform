package chunked

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"
)

// failSink always returns err on Write.
type failSink struct{ err error }

func (s failSink) Write([]byte) (int, error) { return 0, s.err }

// shortSink reports a short write with a nil error.
type shortSink struct{ n int }

func (s shortSink) Write(p []byte) (int, error) {
	if s.n < len(p) {
		return s.n, nil
	}
	return len(p), nil
}

// countingSink records how many Write calls it received.
type countingSink struct {
	bytes.Buffer
	calls int
}

func (s *countingSink) Write(p []byte) (int, error) {
	s.calls++
	return s.Buffer.Write(p)
}

// 1. Exact bytes: lowercase hex, no padding, correct framing.
func TestExactBytes(t *testing.T) {
	var buf bytes.Buffer
	w := New(&buf, 0)
	if n, err := w.Write([]byte("hello")); n != 5 || err != nil {
		t.Fatalf("Write = (%d, %v), want (5, nil)", n, err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close = %v", err)
	}
	if got, want := buf.String(), "5\r\nhello\r\n0\r\n\r\n"; got != want {
		t.Fatalf("bytes = %q, want %q", got, want)
	}
}

func TestHexLowercaseNoPadding(t *testing.T) {
	var buf bytes.Buffer
	w := New(&buf, 0)
	payload := strings.Repeat("x", 0x1a)
	if _, err := w.Write([]byte(payload)); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	want := "1a\r\n" + payload + "\r\n0\r\n\r\n"
	if got := buf.String(); got != want {
		t.Fatalf("bytes = %q, want %q", got, want)
	}
}

// 2. Zero-length writes produce no chunk.
func TestZeroLengthWrite(t *testing.T) {
	var buf bytes.Buffer
	w := New(&buf, 0)
	for _, p := range [][]byte{nil, {}} {
		n, err := w.Write(p)
		if n != 0 || err != nil {
			t.Fatalf("Write(%v) = (%d, %v), want (0, nil)", p, n, err)
		}
	}
	if w.Chunks() != 0 {
		t.Fatalf("Chunks = %d, want 0", w.Chunks())
	}
	if buf.Len() != 0 {
		t.Fatalf("sink got %q, want empty", buf.String())
	}
}

// 3. Oversized writes are split preserving order and content.
func TestSplitByMaxChunk(t *testing.T) {
	var buf bytes.Buffer
	w := New(&buf, 4)
	n, err := w.Write([]byte("abcdefghi")) // 9 bytes
	if n != 9 || err != nil {
		t.Fatalf("Write = (%d, %v), want (9, nil)", n, err)
	}
	if w.Chunks() != 3 {
		t.Fatalf("Chunks = %d, want 3", w.Chunks())
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	want := "4\r\nabcd\r\n4\r\nefgh\r\n1\r\ni\r\n0\r\n\r\n"
	if got := buf.String(); got != want {
		t.Fatalf("bytes = %q, want %q", got, want)
	}
}

// 4. Close is idempotent and emits the last chunk exactly once.
func TestCloseIdempotent(t *testing.T) {
	var buf bytes.Buffer
	w := New(&buf, 0)
	for i := 0; i < 3; i++ {
		if err := w.Close(); err != nil {
			t.Fatalf("Close #%d = %v", i+1, err)
		}
	}
	if got := buf.String(); got != "0\r\n\r\n" {
		t.Fatalf("bytes = %q, want one last chunk", got)
	}
}

// 5. Write after Close fails with ErrClosed and emits nothing.
func TestWriteAfterClose(t *testing.T) {
	var buf bytes.Buffer
	w := New(&buf, 0)
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	before := buf.Len()
	n, err := w.Write([]byte("x"))
	if n != 0 || !errors.Is(err, ErrClosed) {
		t.Fatalf("Write = (%d, %v), want (0, ErrClosed)", n, err)
	}
	if buf.Len() != before {
		t.Fatal("sink received bytes after Close")
	}
}

// 6. Sink failure is sticky and matches ErrSinkFail via errors.Is.
func TestSinkFailureSticky(t *testing.T) {
	sinkErr := errors.New("boom")
	w := New(failSink{err: sinkErr}, 0)

	n, err := w.Write([]byte("abc"))
	if n != 0 || !errors.Is(err, ErrSinkFail) || !errors.Is(err, sinkErr) {
		t.Fatalf("Write = (%d, %v), want (0, ErrSinkFail wrapping %v)", n, err, sinkErr)
	}
	if _, err := w.Write([]byte("abc")); !errors.Is(err, ErrSinkFail) {
		t.Fatalf("second Write = %v, want ErrSinkFail", err)
	}
	if err := w.Close(); !errors.Is(err, ErrSinkFail) {
		t.Fatalf("Close = %v, want ErrSinkFail", err)
	}
}

// 6b. After a sink failure the sink is never touched again.
func TestSinkNotTouchedAfterFailure(t *testing.T) {
	sink := &flakySink{}
	w := New(sink, 0)
	if _, err := w.Write([]byte("abc")); err == nil {
		t.Fatal("first Write should fail")
	}
	calls := sink.calls
	_, _ = w.Write([]byte("abc"))
	_ = w.Close()
	if sink.calls != calls {
		t.Fatalf("sink called %d more times after failure", sink.calls-calls)
	}
}

type flakySink struct{ calls int }

func (s *flakySink) Write(p []byte) (int, error) {
	s.calls++
	return 0, errors.New("fail")
}

// 7. A short write with nil error is treated as a failure.
func TestShortWriteIsFailure(t *testing.T) {
	w := New(shortSink{n: 2}, 0)
	if _, err := w.Write([]byte("hello")); !errors.Is(err, ErrSinkFail) {
		t.Fatalf("Write = %v, want ErrSinkFail", err)
	}
	if !errors.Is(w.Close(), ErrSinkFail) {
		t.Fatal("sticky error should persist after short write")
	}
}

// 8. Write does not mutate its argument; identical inputs give
// identical output.
func TestInputNotMutated(t *testing.T) {
	in := []byte("hello")
	orig := string(in)

	var b1, b2 bytes.Buffer
	w1, w2 := New(&b1, 2), New(&b2, 2)
	for _, w := range []*Writer{w1, w2} {
		if _, err := w.Write(in); err != nil {
			t.Fatal(err)
		}
		if err := w.Close(); err != nil {
			t.Fatal(err)
		}
	}
	if string(in) != orig {
		t.Fatalf("input mutated to %q", in)
	}
	if b1.String() != b2.String() {
		t.Fatalf("outputs differ: %q vs %q", b1.String(), b2.String())
	}
}

// Chunks counts data chunks only, across multiple writes.
func TestChunksCounter(t *testing.T) {
	var buf bytes.Buffer
	w := New(&buf, 3)
	if _, err := w.Write([]byte("abcdefg")); err != nil { // 3+3+1
		t.Fatal(err)
	}
	if _, err := w.Write([]byte("hi")); err != nil { // 1 more
		t.Fatal(err)
	}
	if got := w.Chunks(); got != 4 {
		t.Fatalf("Chunks = %d, want 4", got)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if got := w.Chunks(); got != 4 {
		t.Fatalf("Chunks after Close = %d, want 4 (last chunk excluded)", got)
	}
}

// A failure mid-way through a multi-chunk write reports the bytes
// consumed so far.
func TestPartialConsumptionOnFailure(t *testing.T) {
	sink := &failAfter{n: 1}
	w := New(sink, 2)
	n, err := w.Write([]byte("abcdef")) // chunks: ab, cd, ef
	if n != 2 || !errors.Is(err, ErrSinkFail) {
		t.Fatalf("Write = (%d, %v), want (2, ErrSinkFail)", n, err)
	}
}

type failAfter struct{ n int }

func (s *failAfter) Write(p []byte) (int, error) {
	if s.n == 0 {
		return 0, errors.New("fail")
	}
	s.n--
	return len(p), nil
}

var _ io.Writer = (*Writer)(nil)

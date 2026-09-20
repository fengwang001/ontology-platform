package chunked

import (
	"bytes"
	"errors"
	"testing"
)

func TestSinkErrorIsSticky(t *testing.T) {
	sink := &errSink{failAt: 0, err: errBoom}
	w := New(sink, 0)

	firstN, firstErr := w.Write([]byte("abc"))
	if firstN != 0 || !errors.Is(firstErr, ErrSinkFail) || !errors.Is(firstErr, errBoom) {
		t.Fatalf("first Write = (%d, %v), want (0, ErrSinkFail wrapping boom)", firstN, firstErr)
	}

	secondN, secondErr := w.Write([]byte("def"))
	if secondN != 0 || !errors.Is(secondErr, ErrSinkFail) {
		t.Fatalf("second Write = (%d, %v), want (0, ErrSinkFail)", secondN, secondErr)
	}
	if err := w.Close(); !errors.Is(err, ErrSinkFail) {
		t.Fatalf("Close = %v, want ErrSinkFail", err)
	}

	if sink.buf.Len() != 0 {
		t.Fatalf("sink received %q after failure, want nothing", sink.buf.String())
	}
}

func TestShortWriteIsFailure(t *testing.T) {
	sink := &shortSink{}
	w := New(sink, 0)

	n, err := w.Write([]byte("abc"))
	if n != 0 || !errors.Is(err, ErrSinkFail) || !errors.Is(err, ErrShortWrite) {
		t.Fatalf("Write = (%d, %v), want (0, ErrSinkFail wrapping ErrShortWrite)", n, err)
	}
	if err := w.Close(); !errors.Is(err, ErrSinkFail) {
		t.Fatalf("Close = %v, want sticky ErrSinkFail", err)
	}
	if sink.calls != 1 {
		t.Fatalf("sink writes after failure = %d, want 1", sink.calls)
	}
}

func TestInputNotModifiedAndReusable(t *testing.T) {
	var a, b bytes.Buffer
	w1, w2 := New(&a, 3), New(&b, 3)

	data := []byte("chunkme")
	if _, err := w1.Write(data); err != nil {
		t.Fatal(err)
	}
	if got := string(data); got != "chunkme" {
		t.Fatalf("input modified to %q", got)
	}
	if _, err := w2.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := w1.Close(); err != nil {
		t.Fatal(err)
	}
	if err := w2.Close(); err != nil {
		t.Fatal(err)
	}
	if a.String() != b.String() {
		t.Fatalf("streams differ: %q vs %q", a.String(), b.String())
	}

	want := "3\r\nchu\r\n3\r\nnkm\r\n1\r\ne\r\n0\r\n\r\n"
	if got := a.String(); got != want {
		t.Fatalf("wire bytes = %q, want %q", got, want)
	}
}

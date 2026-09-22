package chunked

import (
	"errors"
	"testing"
)

// checkTerminal verifies the limit rejection is terminal: later writes
// return the same sentinel and the decoded body is untouched.
func checkTerminal(t *testing.T, d *Decoder, want error, wantBody string) {
	t.Helper()
	if string(d.Body()) != wantBody {
		t.Fatalf("body after rejection = %q, want %q", d.Body(), wantBody)
	}
	if _, err := d.Write([]byte("0\r\n\r\n")); !errors.Is(err, want) {
		t.Fatalf("write after rejection: %v, want %v", err, want)
	}
	if string(d.Body()) != wantBody {
		t.Fatalf("body changed after rejection: %q", d.Body())
	}
	if d.Done() {
		t.Fatal("done after rejection")
	}
}

func TestMaxSizeLine(t *testing.T) {
	d := New(Config{MaxSizeLine: 4})
	// Rejected mid-line, before any LF arrives.
	if _, err := d.Write([]byte("12345")); !errors.Is(err, ErrSizeLineTooLong) {
		t.Fatalf("err %v", err)
	}
	var ce *Error
	_, err := d.Write([]byte("x"))
	if !errors.As(err, &ce) || ce.Offset != 4 {
		t.Fatalf("offset %v, want 4", err)
	}
	checkTerminal(t, d, ErrSizeLineTooLong, "")
}

func TestMaxChunk(t *testing.T) {
	d := New(Config{MaxChunk: 3})
	// The size line alone must trigger the rejection: no data buffered.
	n, err := d.Write([]byte("5\r\nhello\r\n0\r\n\r\n"))
	if !errors.Is(err, ErrChunkTooLarge) {
		t.Fatalf("err %v", err)
	}
	if n != 2 { // "5\r" consumed; LF is where the limit trips
		t.Fatalf("consumed %d, want 2", n)
	}
	var ce *Error
	if !errors.As(err, &ce) || ce.Offset != 2 {
		t.Fatalf("offset %v, want 2", err)
	}
	checkTerminal(t, d, ErrChunkTooLarge, "")
}

func TestMaxBody(t *testing.T) {
	d := New(Config{MaxBody: 4})
	if _, err := d.Write([]byte("3\r\nabc\r\n")); err != nil {
		t.Fatalf("first chunk: %v", err)
	}
	// Second chunk would push the total to 6 > 4: rejected at its
	// size line, before any of its data is consumed.
	_, err := d.Write([]byte("3\r\ndef\r\n0\r\n\r\n"))
	if !errors.Is(err, ErrBodyTooLarge) {
		t.Fatalf("err %v", err)
	}
	var ce *Error
	if !errors.As(err, &ce) || ce.Offset != 10 {
		t.Fatalf("offset %v, want 10", err)
	}
	checkTerminal(t, d, ErrBodyTooLarge, "abc")
}

func TestMaxTrailers(t *testing.T) {
	d := New(Config{MaxTrailers: 1})
	if _, err := d.Write([]byte("2\r\nhi\r\n0\r\nA: 1\r\n")); err != nil {
		t.Fatalf("first trailer: %v", err)
	}
	if d.Done() {
		t.Fatal("done before blank line")
	}
	_, err := d.Write([]byte("B: 2\r\n\r\n"))
	if !errors.Is(err, ErrTooManyTrailers) {
		t.Fatalf("err %v", err)
	}
	checkTerminal(t, d, ErrTooManyTrailers, "hi")
}

func TestLimitsArePerInstance(t *testing.T) {
	small := New(Config{MaxChunk: 2})
	large := New(Config{MaxChunk: 100})
	msg := []byte("5\r\nhello\r\n0\r\n\r\n")
	if _, err := small.Write(msg); !errors.Is(err, ErrChunkTooLarge) {
		t.Fatalf("small: %v", err)
	}
	if _, err := large.Write(msg); err != nil {
		t.Fatalf("large: %v", err)
	}
	if !large.Done() || string(large.Body()) != "hello" {
		t.Fatalf("large: done=%v body=%q", large.Done(), large.Body())
	}
}

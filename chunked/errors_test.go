package chunked

import (
	"bytes"
	"errors"
	"testing"

	"ontology/frame"
	"ontology/hexline"
)

func isErr(err, sentinel error) bool {
	return errors.Is(err, sentinel)
}

// failOffset decodes msg and returns the *Error produced.
func failOffset(t *testing.T, cfg Config, msg string) *Error {
	t.Helper()
	d := New(cfg)
	_, err := d.Write([]byte(msg))
	var cerr *Error
	if !errors.As(err, &cerr) {
		t.Fatalf("Write(%q) err=%v, want *Error", msg, err)
	}
	return cerr
}

func TestSixErrorsWithOffsets(t *testing.T) {
	cases := []struct {
		name    string
		cfg     Config
		msg     string
		sent    error
		offset  int
		useDone bool // trigger via Close instead of Write
	}{
		{"not hex", Config{}, "Z\r\n", hexline.ErrNotHex, 2, false},
		{"line too long", Config{MaxLineLen: 4}, "12345\r\n", hexline.ErrLineTooLong, 4, false},
		{"missing CRLF", Config{}, "1\r\naX", frame.ErrMissingCRLF, 4, false},
		{"half CRLF", Config{}, "1\r\na\r", ErrHalfCRLF, 5, true},
		{"unterminated quote", Config{}, "1;a=\"xy\r\n", hexline.ErrUnterminatedQuote, 8, false},
		{"too many trailers", Config{MaxTrailers: 1}, "0\r\nA: 1\r\nB: 2\r\n\r\n", ErrTooManyTrailers, 14, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			d := New(c.cfg)
			var err error
			if c.useDone {
				if _, werr := d.Write([]byte(c.msg)); werr != nil {
					t.Fatalf("Write: %v", werr)
				}
				err = d.Close()
			} else {
				_, err = d.Write([]byte(c.msg))
			}
			if !isErr(err, c.sent) {
				t.Fatalf("err=%v, want %v", err, c.sent)
			}
			var cerr *Error
			if !errors.As(err, &cerr) {
				t.Fatalf("err is %T, want *Error", err)
			}
			if cerr.Offset != c.offset {
				t.Errorf("offset=%d, want %d", cerr.Offset, c.offset)
			}
		})
	}
}

func TestSixErrorsMutuallyDistinct(t *testing.T) {
	sentinels := []error{
		hexline.ErrNotHex,
		hexline.ErrLineTooLong,
		frame.ErrMissingCRLF,
		ErrHalfCRLF,
		hexline.ErrUnterminatedQuote,
		ErrTooManyTrailers,
	}
	for i, a := range sentinels {
		for j, b := range sentinels {
			if i != j && errors.Is(a, b) {
				t.Errorf("sentinel %v matches %v", a, b)
			}
		}
	}
	// ErrHalfCRLF is a kind of incompleteness, but ErrIncomplete must
	// not swallow the other five.
	if !errors.Is(ErrHalfCRLF, ErrIncomplete) {
		t.Error("ErrHalfCRLF should match ErrIncomplete")
	}
}

func TestChunkBoundaryStrict(t *testing.T) {
	cases := []struct {
		name string
		msg  string
	}{
		{"one byte too many", "2\r\nabc\r\n0\r\n\r\n"},
		{"one byte too few", "3\r\nab\r\n0\r\n\r\n"},
		{"single LF", "1\r\na\n0\r\n\r\n"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cerr := failOffset(t, Config{}, c.msg)
			if !isErr(cerr, frame.ErrMissingCRLF) {
				t.Fatalf("err=%v, want ErrMissingCRLF", cerr)
			}
		})
	}
}

func TestLimitsRejectImmediatelyAndKeepBody(t *testing.T) {
	cases := []struct {
		name string
		cfg  Config
		msg  string
		sent error
		body string
	}{
		{"line length", Config{MaxLineLen: 3}, "1234\r\n", hexline.ErrLineTooLong, ""},
		{"chunk size", Config{MaxChunkSize: 3}, "3\r\nabc\r\n4\r\n", ErrChunkTooLarge, "abc"},
		{"body size", Config{MaxBodySize: 5}, "3\r\nabc\r\n3\r\n", ErrBodyTooLarge, "abc"},
		{"trailers", Config{MaxTrailers: 1}, "1\r\nz\r\n0\r\nA: 1\r\nB: 2\r\n\r\n", ErrTooManyTrailers, "z"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			d := New(c.cfg)
			_, err := d.Write([]byte(c.msg))
			if !isErr(err, c.sent) {
				t.Fatalf("err=%v, want %v", err, c.sent)
			}
			if got := d.Body(); !bytes.Equal(got, []byte(c.body)) {
				t.Fatalf("body=%q, want %q (must be preserved)", got, c.body)
			}
			// Terminal state: same error again, body still intact.
			if _, err2 := d.Write([]byte("0\r\n\r\n")); !isErr(err2, c.sent) {
				t.Fatalf("second Write err=%v, want %v", err2, c.sent)
			}
			if got := d.Body(); !bytes.Equal(got, []byte(c.body)) {
				t.Fatalf("body changed to %q after terminal error", got)
			}
			if d.Done() {
				t.Fatal("done after rejected input")
			}
		})
	}
}

func TestLimitCheckedBeforeBuffering(t *testing.T) {
	// A huge declared size must be rejected at the size line, without
	// waiting for (nonexistent) data bytes.
	d := New(Config{MaxChunkSize: 8})
	n, err := d.Write([]byte("ffffffffffff\r\n"))
	if !isErr(err, ErrChunkTooLarge) {
		t.Fatalf("err=%v, want ErrChunkTooLarge", err)
	}
	if n != len("ffffffffffff\r\n") {
		t.Fatalf("consumed %d, want size line only", n)
	}
}

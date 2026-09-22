package chunked

import (
	"errors"
	"testing"
)

// runCase feeds input (1 byte at a time, to prove splits don't affect
// error detection) and optionally closes, returning the first error.
func runCase(cfg Config, input string, close bool) error {
	d := New(cfg)
	for i := 0; i < len(input); i++ {
		if _, err := d.Write([]byte(input[i : i+1])); err != nil {
			return err
		}
	}
	if close {
		return d.Close()
	}
	return nil
}

var sixSentinels = []error{
	ErrBadSize, ErrSizeLineTooLong, ErrMissingCRLF,
	ErrHalfCRLF, ErrUnclosedQuote, ErrTooManyTrailers,
}

func TestSixErrorsDistinguishable(t *testing.T) {
	cases := []struct {
		name  string
		cfg   Config
		input string
		close bool
		want  error
		off   int64
	}{
		{"non-hex size", Config{}, "1Z\r\n", false, ErrBadSize, 1},
		{"size line too long", Config{MaxSizeLine: 4}, "12345\r\n", false, ErrSizeLineTooLong, 4},
		{"missing CRLF", Config{}, "1\r\naX", false, ErrMissingCRLF, 4},
		{"half CRLF at close", Config{}, "1\r\na\r", true, ErrHalfCRLF, 5},
		{"unclosed quote", Config{}, "1;a=\"x\r\n", false, ErrUnclosedQuote, 4},
		{"too many trailers", Config{MaxTrailers: 1}, "0\r\nA: 1\r\nB: 2\r\n\r\n", false, ErrTooManyTrailers, 14},
	}
	for _, c := range cases {
		err := runCase(c.cfg, c.input, c.close)
		if err == nil {
			t.Errorf("%s: no error", c.name)
			continue
		}
		if !errors.Is(err, c.want) {
			t.Errorf("%s: %v does not match %v", c.name, err, c.want)
		}
		for _, other := range sixSentinels {
			if other != c.want && errors.Is(err, other) {
				t.Errorf("%s: %v unexpectedly matches %v", c.name, err, other)
			}
		}
		var ce *Error
		if !errors.As(err, &ce) {
			t.Errorf("%s: not a *Error", c.name)
			continue
		}
		if ce.Offset != c.off {
			t.Errorf("%s: offset %d, want %d", c.name, ce.Offset, c.off)
		}
	}
}

func TestChunkBoundaryStrict(t *testing.T) {
	cases := []struct {
		name  string
		input string
		off   int64
	}{
		{"one byte too many", "2\r\nabc\r\n0\r\n\r\n", 5},
		{"one byte too few", "3\r\nab\r\n0\r\n\r\n", 6},
		{"bare LF instead of CRLF", "1\r\na\n", 4},
		{"bare CR then wrong byte", "1\r\na\rX", 5},
	}
	for _, c := range cases {
		err := runCase(Config{}, c.input, false)
		if !errors.Is(err, ErrMissingCRLF) {
			t.Errorf("%s: %v", c.name, err)
			continue
		}
		var ce *Error
		if errors.As(err, &ce) && ce.Offset != c.off {
			t.Errorf("%s: offset %d, want %d", c.name, ce.Offset, c.off)
		}
	}
}

func TestErrorIsTerminal(t *testing.T) {
	d := New(Config{})
	if _, err := d.Write([]byte("Z\r\n")); !errors.Is(err, ErrBadSize) {
		t.Fatalf("first err %v", err)
	}
	body := d.Body()
	for i := 0; i < 3; i++ {
		_, err := d.Write([]byte("1\r\na\r\n0\r\n\r\n"))
		if !errors.Is(err, ErrBadSize) {
			t.Fatalf("write %d after failure: %v", i, err)
		}
	}
	if d.Done() {
		t.Fatal("done after failure")
	}
	if string(d.Body()) != string(body) {
		t.Fatal("body changed after failure")
	}
}

func TestErrorOffsetsAreAbsolute(t *testing.T) {
	// First chunk fine, second size line has a bad digit.
	err := runCase(Config{}, "2\r\nok\r\n1Z\r\n", false)
	if !errors.Is(err, ErrBadSize) {
		t.Fatalf("err %v", err)
	}
	var ce *Error
	if !errors.As(err, &ce) || ce.Offset != 8 {
		t.Fatalf("offset %v, want 8", err)
	}
}

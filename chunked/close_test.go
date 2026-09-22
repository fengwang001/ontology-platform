package chunked

import (
	"errors"
	"testing"
)

func TestCloseStates(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  error
		state State
		off   int64
	}{
		{"in size line", "3\r", ErrIncomplete, StateSizeLine, 2},
		{"in chunk data", "3\r\nab", ErrIncomplete, StateChunkData, 5},
		{"waiting CRLF", "3\r\nabc", ErrIncomplete, StateCRLF, 6},
		{"half CRLF after data", "3\r\nabc\r", ErrHalfCRLF, StateCRLF, 7},
		{"in trailer", "0\r\nT: 1\r\n", ErrIncomplete, StateTrailer, 9},
		{"half CRLF in trailer", "0\r\n\r", ErrHalfCRLF, StateTrailer, 4},
	}
	for _, c := range cases {
		d := New(Config{})
		if _, err := d.Write([]byte(c.input)); err != nil {
			t.Errorf("%s: write: %v", c.name, err)
			continue
		}
		err := d.Close()
		if !errors.Is(err, c.want) {
			t.Errorf("%s: %v, want %v", c.name, err, c.want)
			continue
		}
		var ce *Error
		if !errors.As(err, &ce) {
			t.Errorf("%s: not a *Error", c.name)
			continue
		}
		if ce.State != c.state {
			t.Errorf("%s: state %v, want %v", c.name, ce.State, c.state)
		}
		if ce.Offset != c.off {
			t.Errorf("%s: offset %d, want %d", c.name, ce.Offset, c.off)
		}
		// The two incomplete flavors must not alias each other.
		other := ErrIncomplete
		if c.want == ErrIncomplete {
			other = ErrHalfCRLF
		}
		if errors.Is(err, other) {
			t.Errorf("%s: %v unexpectedly matches %v", c.name, err, other)
		}
	}
}

func TestCloseAfterComplete(t *testing.T) {
	d := New(Config{})
	if _, err := d.Write([]byte("2\r\nhi\r\n0\r\n\r\n")); err != nil {
		t.Fatal(err)
	}
	if !d.Done() {
		t.Fatal("not done")
	}
	if err := d.Close(); err != nil {
		t.Fatalf("close after complete: %v", err)
	}
}

func TestCloseIsTerminal(t *testing.T) {
	d := New(Config{})
	if _, err := d.Write([]byte("5\r\nab")); err != nil {
		t.Fatal(err)
	}
	first := d.Close()
	if !errors.Is(first, ErrIncomplete) {
		t.Fatalf("close: %v", first)
	}
	if _, err := d.Write([]byte("cde\r\n0\r\n\r\n")); !errors.Is(err, ErrIncomplete) {
		t.Fatalf("write after close: %v", err)
	}
	if err := d.Close(); !errors.Is(err, ErrIncomplete) {
		t.Fatalf("second close: %v", err)
	}
	if string(d.Body()) != "ab" {
		t.Fatalf("body %q", d.Body())
	}
}

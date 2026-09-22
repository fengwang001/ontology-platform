package frame

import (
	"bytes"
	"errors"
	"testing"

	"ontology/hexline"
)

func feedAll(f *Frame, p []byte, step int) ([]byte, error) {
	var body []byte
	emit := func(b []byte) { body = append(body, b...) }
	for i := 0; i < len(p); i += step {
		j := i + step
		if j > len(p) {
			j = len(p)
		}
		if _, err := f.Feed(p[i:j], int64(i), emit); err != nil {
			return body, err
		}
	}
	return body, nil
}

func TestSingleChunkAnySplit(t *testing.T) {
	msg := []byte("5;a=1\r\nhello\r\n")
	for step := 1; step <= len(msg); step++ {
		var f Frame
		body, err := feedAll(&f, msg, step)
		if err != nil {
			t.Fatalf("step %d: %v", step, err)
		}
		if !bytes.Equal(body, []byte("hello")) {
			t.Fatalf("step %d: body %q", step, body)
		}
		if f.State() != StateDone {
			t.Fatalf("step %d: state %v", step, f.State())
		}
		if f.Size() != 5 {
			t.Fatalf("step %d: size %d", step, f.Size())
		}
	}
}

func TestZeroSizeChunk(t *testing.T) {
	var f Frame
	body, err := feedAll(&f, []byte("0\r\n"), 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(body) != 0 || f.State() != StateDone || f.Size() != 0 {
		t.Fatalf("body=%q state=%v size=%d", body, f.State(), f.Size())
	}
}

func TestResetReusesFrame(t *testing.T) {
	var f Frame
	var body []byte
	emit := func(b []byte) { body = append(body, b...) }
	for _, chunk := range []string{"2\r\nab\r\n", "3\r\ncde\r\n"} {
		f.Reset()
		if _, err := f.Feed([]byte(chunk), 0, emit); err != nil {
			t.Fatal(err)
		}
		if f.State() != StateDone {
			t.Fatalf("state %v", f.State())
		}
	}
	if string(body) != "abcde" {
		t.Fatalf("body %q", body)
	}
}

func TestBadCRLFAfterData(t *testing.T) {
	cases := []struct {
		name string
		msg  string
		at   int64
	}{
		{"wrong byte", "3\r\nabcX", 6},
		{"bare LF", "3\r\nabc\n", 6},
		{"CR then wrong", "3\r\nabc\rX", 7},
		{"extra data byte", "2\r\nabc\r\n", 5},
		{"short data", "3\r\nab\r\n", 6},
	}
	for _, c := range cases {
		var f Frame
		_, err := feedAll(&f, []byte(c.msg), len(c.msg))
		if !errors.Is(err, ErrBadCRLF) {
			t.Fatalf("%s: err %v", c.name, err)
		}
		var fe *Error
		if !errors.As(err, &fe) || fe.At != c.at {
			t.Fatalf("%s: offset %v, want %d", c.name, err, c.at)
		}
	}
}

func TestLineTooLong(t *testing.T) {
	f := Frame{MaxLine: 4}
	_, err := feedAll(&f, []byte("12345\r\n"), 7)
	if !errors.Is(err, ErrLineTooLong) {
		t.Fatalf("err %v", err)
	}
	var fe *Error
	if !errors.As(err, &fe) || fe.At != 4 {
		t.Fatalf("offset %v, want 4", err)
	}
}

func TestSizeLineNeedsCRLF(t *testing.T) {
	var f Frame
	_, err := feedAll(&f, []byte("5\n"), 2)
	if !errors.Is(err, ErrBadCRLF) {
		t.Fatalf("err %v", err)
	}
}

func TestParseErrorOffsetUsesBase(t *testing.T) {
	var f Frame
	// base 100: line "1Z" starts at 100, bad byte 'Z' at 101.
	_, err := f.Feed([]byte("1Z\r\n"), 100, func([]byte) {})
	if !errors.Is(err, hexline.ErrBadSize) {
		t.Fatalf("err %v", err)
	}
	var fe *Error
	if !errors.As(err, &fe) || fe.At != 101 {
		t.Fatalf("offset %v, want 101", err)
	}
}

func TestOnSizeAbortsBeforeData(t *testing.T) {
	sentinel := errors.New("too big")
	f := Frame{OnSize: func(size uint64, at int64) error {
		if size != 3 || at != 2 {
			t.Errorf("OnSize(%d, %d)", size, at)
		}
		return sentinel
	}}
	n, err := f.Feed([]byte("3\r\nabc\r\n"), 0, func([]byte) {})
	if !errors.Is(err, sentinel) {
		t.Fatalf("err %v", err)
	}
	if n != 2 { // "3\r" consumed; the LF where OnSize failed is not
		t.Fatalf("consumed %d, want 2", n)
	}
}

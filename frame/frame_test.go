package frame

import (
	"errors"
	"testing"

	"ontology/hexline"
)

func feed(f *Frame, p []byte, dst []byte) ([]byte, bool, error) {
	out, n, done, err := f.Write(p, dst, nil)
	if err == nil && n != len(p) && !done {
		panic("short write without completion or error")
	}
	return out, done, err
}

func TestFrameByteByByte(t *testing.T) {
	f := New(16)
	var body []byte
	done := false
	for i := 0; i < len("3\r\nabc\r\n"); i++ {
		var err error
		body, done, err = feed(f, []byte("3\r\nabc\r\n"[i:i+1]), body)
		if err != nil {
			t.Fatalf("byte %d: %v", i, err)
		}
	}
	if !done || string(body) != "abc" {
		t.Fatalf("done=%v body=%q", done, body)
	}
	if size, ok := f.Size(); !ok || size != 3 {
		t.Fatalf("size=%d ok=%v", size, ok)
	}
}

func TestFrameWholeAndZero(t *testing.T) {
	f := New(16)
	body, done, err := feed(f, []byte("4\r\nWiki\r\n"), nil)
	if err != nil || !done || string(body) != "Wiki" {
		t.Fatalf("done=%v body=%q err=%v", done, body, err)
	}
	z := New(16)
	_, done, err = feed(z, []byte("0\r\n"), nil)
	if err != nil || !done {
		t.Fatalf("zero frame: done=%v err=%v", done, err)
	}
	if size, ok := z.Size(); !ok || size != 0 {
		t.Fatalf("zero size=%d ok=%v", size, ok)
	}
}

func TestFrameMissingCRLF(t *testing.T) {
	cases := []string{
		"1\r\naX",    // extra byte where CR expected
		"2\r\nab\n",  // single LF instead of CRLF
		"2\r\nab\rX", // CR then wrong byte
	}
	for _, in := range cases {
		f := New(16)
		_, _, err := feed(f, []byte(in), nil)
		if !errors.Is(err, ErrMissingCRLF) {
			t.Errorf("%q: err=%v, want ErrMissingCRLF", in, err)
		}
	}
}

func TestFrameShortDataUsesCRAsData(t *testing.T) {
	// Declared 3 but only 2 data bytes: the CR is consumed as data,
	// then the LF fails the CR check.
	f := New(16)
	_, _, err := feed(f, []byte("3\r\nab\r\n"), nil)
	if !errors.Is(err, ErrMissingCRLF) {
		t.Fatalf("err=%v, want ErrMissingCRLF", err)
	}
}

func TestFrameSizeLineErrors(t *testing.T) {
	f := New(16)
	_, _, err := feed(f, []byte("zz\r\n"), nil)
	if !errors.Is(err, hexline.ErrNotHex) {
		t.Errorf("not hex: %v", err)
	}
	f = New(2)
	_, _, err = feed(f, []byte("abc\r\n"), nil)
	if !errors.Is(err, hexline.ErrLineTooLong) {
		t.Errorf("too long: %v", err)
	}
}

func TestFrameCheckCallback(t *testing.T) {
	f := New(16)
	calls := 0
	check := func(size uint64) error {
		calls++
		if size > 2 {
			return errTooBig
		}
		return nil
	}
	_, n, _, err := f.Write([]byte("5\r\nhello\r\n"), nil, check)
	if !errors.Is(err, errTooBig) || calls != 1 {
		t.Fatalf("err=%v calls=%d", err, calls)
	}
	if want := len("5\r\n"); n != want {
		t.Fatalf("consumed %d, want %d (no data consumed)", n, want)
	}
}

var errTooBig = errors.New("too big")

func TestFrameHalfCR(t *testing.T) {
	f := New(16)
	if _, _, err := feed(f, []byte("1\r\na\r"), nil); err != nil {
		t.Fatal(err)
	}
	if !f.HalfCR() || f.State() != StateExpectLF {
		t.Errorf("after data CR: HalfCR=%v state=%v", f.HalfCR(), f.State())
	}
	g := New(16)
	if _, _, err := feed(g, []byte("1\r"), nil); err != nil {
		t.Fatal(err)
	}
	if !g.HalfCR() || g.State() != StateSizeLine {
		t.Errorf("size line CR: HalfCR=%v state=%v", g.HalfCR(), g.State())
	}
	h := New(16)
	if _, _, err := feed(h, []byte("1\r\na"), nil); err != nil {
		t.Fatal(err)
	}
	if h.HalfCR() || h.State() != StateExpectCR {
		t.Errorf("await CR: HalfCR=%v state=%v", h.HalfCR(), h.State())
	}
}

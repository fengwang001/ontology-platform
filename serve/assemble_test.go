package serve

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"

	"ontology/coalesce"
	"ontology/rangespec"
	"ontology/source"
)

func assemble(t *testing.T, cfg Config, src source.Source, header string) *Assembler {
	t.Helper()
	a := NewAssembler(cfg, src)
	if err := a.Assemble(header); err != nil {
		t.Fatalf("Assemble(%q): %v", header, err)
	}
	return a
}

func bodyOf(t *testing.T, a *Assembler) []byte {
	t.Helper()
	var buf bytes.Buffer
	if _, err := a.WriteTo(&buf); err != nil {
		t.Fatalf("WriteTo: %v", err)
	}
	return buf.Bytes()
}

func TestThreeFormsAndClipping(t *testing.T) {
	data := source.Bytes([]byte("0123456789")) // size 10
	cases := []struct {
		header string
		want   string
	}{
		{"bytes=2-5", "2345"},
		{"bytes=2-999", "23456789"}, // b clipped to end
		{"bytes=7-", "789"},
		{"bytes=-3", "789"},
		{"bytes=-999", "0123456789"}, // n > size takes everything
	}
	for _, c := range cases {
		a := assemble(t, Config{}, data, c.header)
		if got := string(bodyOf(t, a)); got != c.want {
			t.Errorf("%s: got %q want %q", c.header, got, c.want)
		}
		if a.IsMultipart() {
			t.Errorf("%s: single range must not be multipart", c.header)
		}
	}
}

func TestMinusZeroIsUnsatisfiableWithSize(t *testing.T) {
	a := NewAssembler(Config{}, source.Bytes([]byte("0123456789")))
	err := a.Assemble("bytes=-0")
	var ue *coalesce.UnsatisfiableError
	if !errors.As(err, &ue) {
		t.Fatalf("bytes=-0: want UnsatisfiableError, got %v", err)
	}
	if ue.Size != 10 {
		t.Fatalf("UnsatisfiableError.Size = %d, want 10", ue.Size)
	}
	var se *rangespec.SyntaxError
	if errors.As(err, &se) {
		t.Fatal("bytes=-0 must not be a syntax error")
	}
}

func TestSyntaxAndUnsatisfiableAreDistinguishable(t *testing.T) {
	a := NewAssembler(Config{}, source.Bytes([]byte("0123456789")))
	synErr := a.Assemble("bytes=2x-5")
	var se *rangespec.SyntaxError
	if !errors.As(synErr, &se) {
		t.Fatalf("want SyntaxError, got %v", synErr)
	}
	if se.Offset != 7 { // "bytes=2x-5": 'x' is at index 7
		t.Fatalf("SyntaxError.Offset = %d, want 7", se.Offset)
	}
	var ue *coalesce.UnsatisfiableError
	if errors.As(synErr, &ue) {
		t.Fatal("syntax error must not match UnsatisfiableError")
	}
	unsatErr := a.Assemble("bytes=100-200")
	if !errors.As(unsatErr, &ue) || ue.Size != 10 {
		t.Fatalf("want UnsatisfiableError{Size:10}, got %v", unsatErr)
	}
	if errors.As(unsatErr, &se) {
		t.Fatal("unsatisfiable must not match SyntaxError")
	}
}

func TestShortReadsAreFilled(t *testing.T) {
	data := []byte("0123456789abcdef")
	flaky := &source.Flaky{Src: source.Bytes(data), MaxChunk: 1}
	a := assemble(t, Config{}, flaky, "bytes=0-15")
	if got := bodyOf(t, a); !bytes.Equal(got, data) {
		t.Fatalf("got %q want %q", got, data)
	}
}

func TestEOFBeforeRangeFilled(t *testing.T) {
	flaky := &source.Flaky{
		Src:         source.Bytes(make([]byte, 100)),
		HasFakeSize: true,
		FakeSize:    100,
		HasTrunc:    true,
		TruncAt:     40,
	}
	a := NewAssembler(Config{}, flaky)
	err := a.Assemble("bytes=0-99")
	if !errors.Is(err, ErrShortData) {
		t.Fatalf("want ErrShortData, got %v", err)
	}
	if errors.Is(err, ErrTooManyRanges) || errors.Is(err, ErrResponseTooLarge) {
		t.Fatal("ErrShortData must be distinguishable from limit errors")
	}
}

func TestReadErrorPropagates(t *testing.T) {
	boom := errors.New("backend exploded")
	flaky := &source.Flaky{Src: source.Bytes([]byte("0123456789")), FailOnCall: 1, Err: boom}
	a := NewAssembler(Config{}, flaky)
	if err := a.Assemble("bytes=0-9"); !errors.Is(err, boom) {
		t.Fatalf("want boom, got %v", err)
	}
}

func TestWriteToBeforeAssemble(t *testing.T) {
	a := NewAssembler(Config{}, source.Bytes([]byte("x")))
	if _, err := a.WriteTo(io.Discard); !errors.Is(err, ErrNotAssembled) {
		t.Fatalf("want ErrNotAssembled, got %v", err)
	}
}

func TestMultipartBodyForTwoRanges(t *testing.T) {
	data := source.Bytes([]byte("0123456789"))
	a := assemble(t, Config{}, data, "bytes=0-1, 8-9")
	if !a.IsMultipart() {
		t.Fatal("two ranges must be multipart")
	}
	body := string(bodyOf(t, a))
	b := a.Boundary()
	for _, want := range []string{
		"--" + b + "\r\n",
		"Content-Range: bytes 0-1/10\r\n",
		"Content-Range: bytes 8-9/10\r\n",
		"\r\n01\r\n",
		"\r\n89\r\n",
		"--" + b + "--\r\n",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("body missing %q:\n%q", want, body)
		}
	}
}

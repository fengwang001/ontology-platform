package serve

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"

	"ontology/coalesce"
	"ontology/multipart"
	"ontology/rangespec"
	"ontology/source"
)

func buildOK(t *testing.T, a *Assembler, header string) *Response {
	t.Helper()
	r, err := a.Build(header)
	if err != nil {
		t.Fatalf("Build(%q): %v", header, err)
	}
	return r
}

func bodyOf(t *testing.T, r *Response) []byte {
	t.Helper()
	var buf bytes.Buffer
	for {
		_, err := r.WriteSome(&buf)
		if err == io.EOF {
			return buf.Bytes()
		}
		if err != nil {
			t.Fatalf("WriteSome: %v", err)
		}
	}
}

func TestThreeFormsAndClipping(t *testing.T) {
	data := source.Bytes([]byte("0123456789")) // len 10
	a := &Assembler{Src: data}
	cases := []struct {
		header string
		want   string
	}{
		{"bytes=2-4", "234"},        // a-b inclusive
		{"bytes=8-99", "89"},        // b clipped to end
		{"bytes=7-", "789"},         // a- to end
		{"bytes=-3", "789"},         // last 3 bytes
		{"bytes=-99", "0123456789"}, // n > total takes all
	}
	for _, c := range cases {
		r := buildOK(t, a, c.header)
		if r.Multipart() {
			t.Errorf("%s: single range must not be multipart", c.header)
		}
		if got := string(bodyOf(t, r)); got != c.want {
			t.Errorf("%s: body %q, want %q", c.header, got, c.want)
		}
	}
}

func TestMinusZeroUnsatisfiableWithTotal(t *testing.T) {
	a := &Assembler{Src: source.Bytes([]byte("0123456789"))}
	_, err := a.Build("bytes=-0")
	var ue *coalesce.UnsatisfiableError
	if !errors.As(err, &ue) {
		t.Fatalf("bytes=-0: not unsatisfiable: %v", err)
	}
	if ue.Total != 10 {
		t.Fatalf("Total=%d, want 10", ue.Total)
	}
	var se *rangespec.SyntaxError
	if errors.As(err, &se) {
		t.Fatalf("bytes=-0 must not be a syntax error")
	}
}

func TestErrorKindsDistinguishable(t *testing.T) {
	a := &Assembler{Src: source.Bytes([]byte("0123456789"))}
	_, synErr := a.Build("bytes=x-y")
	_, unsatErr := a.Build("bytes=50-60")
	var se *rangespec.SyntaxError
	var ue *coalesce.UnsatisfiableError
	if !errors.As(synErr, &se) || errors.As(synErr, &ue) {
		t.Fatalf("syntax error misclassified: %v", synErr)
	}
	if !errors.As(unsatErr, &ue) || errors.As(unsatErr, &se) {
		t.Fatalf("unsatisfiable misclassified: %v", unsatErr)
	}
	if ue.Total != 10 {
		t.Fatalf("unsatisfiable Total=%d, want 10", ue.Total)
	}
}

func TestShortReadsAreCompleted(t *testing.T) {
	a := &Assembler{Src: source.Short{Src: source.Bytes([]byte("0123456789")), Max: 1}}
	r := buildOK(t, a, "bytes=0-9")
	if got := string(bodyOf(t, r)); got != "0123456789" {
		t.Fatalf("body %q", got)
	}
}

func TestShortSourceOnPrematureEOF(t *testing.T) {
	// Source claims 10 bytes, delivers 3, then shrinks to 5 mid-read.
	short := source.Short{Src: source.Bytes([]byte("0123456789")), Max: 3}
	src := &source.ShrinkAfter{Src: short, N: 1, NewSize: 5}
	a := &Assembler{Src: src}
	_, err := a.Build("bytes=0-9")
	if !errors.Is(err, ErrShortSource) {
		t.Fatalf("err=%v, want ErrShortSource", err)
	}
}

func TestReadErrorPropagates(t *testing.T) {
	boom := errors.New("read exploded")
	src := source.FailAt{Src: source.Bytes([]byte("0123456789")), At: 0, Err: boom}
	a := &Assembler{Src: src}
	_, err := a.Build("bytes=0-9")
	if !errors.Is(err, boom) {
		t.Fatalf("err=%v, want %v", err, boom)
	}
}

func TestSingleRangeIsRawBytes(t *testing.T) {
	a := &Assembler{Src: source.Bytes([]byte("0123456789"))}
	r := buildOK(t, a, "bytes=2-5")
	if r.Multipart() {
		t.Fatal("single range must be raw bytes")
	}
	if got := string(bodyOf(t, r)); got != "2345" {
		t.Fatalf("body %q", got)
	}
}

func TestMergedToOneDegeneratesToRaw(t *testing.T) {
	// Two requested ranges that coalesce into one must degrade to raw
	// bytes, not multipart framing.
	a := &Assembler{Src: source.Bytes([]byte("0123456789"))}
	r := buildOK(t, a, "bytes=0-4, 5-9")
	if r.Multipart() {
		t.Fatal("merged-to-one ranges must be raw bytes")
	}
	if got := string(bodyOf(t, r)); got != "0123456789" {
		t.Fatalf("body %q", got)
	}
}

func TestMultipartFramingAndBoundarySafety(t *testing.T) {
	// Content contains a boundary-looking decoy.
	decoy := multipart.BoundaryPrefix + strings.Repeat("ab", 20)
	data := []byte("AA--" + decoy + "--BB")
	a := &Assembler{Src: source.Bytes(data)}
	r := buildOK(t, a, "bytes=0-3, 8-11")
	if !r.Multipart() {
		t.Fatal("two disjoint ranges must be multipart")
	}
	body := bodyOf(t, r)
	// Extract the boundary from the first line and verify it never occurs
	// inside the payload bytes.
	line := string(body[:bytes.IndexByte(body, '\r')])
	boundary := strings.TrimPrefix(line, "--")
	if boundary == "" || boundary == line {
		t.Fatalf("no boundary in first line %q", line)
	}
	if strings.Contains(string(data), boundary) {
		t.Fatalf("boundary %q collides with content", boundary)
	}
	if !bytes.Contains(body, []byte("Content-Range: bytes 0-3/")) {
		t.Fatalf("missing first Content-Range header:\n%s", body)
	}
	if !bytes.HasSuffix(body, []byte("--"+boundary+"--\r\n")) {
		t.Fatalf("missing closing boundary:\n%s", body)
	}
}

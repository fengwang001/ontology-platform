package chunked

import (
	"bytes"
	"testing"
)

func feed(t *testing.T, wire []byte, size int) (*Decoder, []byte, error) {
	t.Helper()
	d := New()
	off := 0
	var err error
	for off < len(wire) {
		end := off + size
		if end > len(wire) {
			end = len(wire)
		}
		var n int
		n, err = d.Write(wire[off:end])
		if err != nil {
			return d, d.Body(), err
		}
		if n == 0 {
			t.Fatalf("made no progress at offset %d", off)
		}
		off += n
	}
	if err == nil {
		err = d.Close()
	}
	return d, d.Body(), err
}

func sampleMessage() ([]byte, []byte) {
	var wire bytes.Buffer
	var body []byte
	chunks := []string{"Hello, ", "chunked", " world!"}
	for _, c := range chunks {
		wire.WriteString(hex(len(c)))
		wire.WriteString("\r\n")
		wire.WriteString(c)
		wire.WriteString("\r\n")
		body = append(body, c...)
	}
	wire.WriteString("0\r\n")
	wire.WriteString("Content-MD5: x\r\n")
	wire.WriteString("X-Trailer:  v \r\n")
	wire.WriteString("\r\n")
	return wire.Bytes(), body
}

func hex(n int) string {
	const digits = "0123456789abcdef"
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{digits[n%16]}, b...)
		n /= 16
	}
	return string(b)
}

func TestAllSplitPoints(t *testing.T) {
	wire, want := sampleMessage()
	var ref []byte
	for size := 1; size <= len(wire); size++ {
		d, got, err := feed(t, wire, size)
		if err != nil {
			t.Fatalf("size %d: %v", size, err)
		}
		if !d.Done() {
			t.Fatalf("size %d: not done", size)
		}
		if !bytes.Equal(got, want) {
			t.Fatalf("size %d: body %q want %q", size, got, want)
		}
		if ref == nil {
			ref = append([]byte(nil), got...)
		} else if !bytes.Equal(got, ref) {
			t.Fatalf("size %d: body differs from reference", size)
		}
	}
}

func TestQuotedExtensionsSkipped(t *testing.T) {
	wire := []byte("4;foo=\"a;b=c\\\"d\";bar=z\r\nWiki\r\n0\r\n\r\n")
	d, body, err := feed(t, wire, 3)
	if err != nil {
		t.Fatal(err)
	}
	if !d.Done() || string(body) != "Wiki" {
		t.Fatalf("done=%v body=%q", d.Done(), body)
	}
}

func TestTrailerBlankLineCompletes(t *testing.T) {
	wire := []byte("4\r\nWiki\r\n0\r\nX: y\r\n")
	d := New()
	n, err := d.Write(wire)
	if err != nil || n != len(wire) {
		t.Fatalf("n=%d err=%v", n, err)
	}
	if d.Done() {
		t.Fatal("done before blank trailer line")
	}
	if got := d.Body(); string(got) != "Wiki" {
		t.Fatalf("partial body %q", got)
	}
	if cerr := d.Close(); cerr == nil {
		t.Fatal("Close before completion should fail")
	} else if ce, _ := AsError(cerr); ce.Kind != KindIncompleteTrailers {
		t.Fatalf("got %v", cerr)
	}

	// After Close terminalized this decoder, use a fresh one for completion.
	d2 := New()
	if _, err := d2.Write(wire); err != nil {
		t.Fatal(err)
	}
	n, err = d2.Write([]byte("\r\n"))
	if err != nil || n != 2 || !d2.Done() {
		t.Fatalf("n=%d err=%v done=%v", n, err, d2.Done())
	}
	if err := d2.Close(); err != nil {
		t.Fatalf("close after done: %v", err)
	}
}

func TestWriteAfterDoneRejected(t *testing.T) {
	wire := []byte("0\r\n\r\n")
	d, _, err := feed(t, wire, 2)
	if err != nil {
		t.Fatal(err)
	}
	n, err := d.Write([]byte("X"))
	if n != 0 {
		t.Fatalf("consumed %d bytes after done", n)
	}
	ce, ok := AsError(err)
	if !ok || ce.Kind != KindAlreadyDone {
		t.Fatalf("got %v", err)
	}
	if !d.Done() || len(d.Body()) != 0 {
		t.Fatal("state changed after terminal error")
	}
	n2, err2 := d.Write([]byte("YY"))
	if n2 != 0 || err2 != err {
		t.Fatalf("second write: n=%d sameErr=%v", n2, err2 == err)
	}
}

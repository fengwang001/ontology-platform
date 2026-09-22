package chunked

import (
	"bytes"
	"testing"
)

// testMessage exercises extensions with quoted semicolons/equals,
// multiple chunks, and trailers.
const testMessage = "4;q=\"a;b=c\";x=1\r\nWiki\r\n" +
	"5\r\npedia\r\n" +
	"0;done=yes\r\n" +
	"X-One: 1\r\n" +
	"X-Two: 2\r\n" +
	"\r\n"

var testBody = []byte("Wikipedia")

// feedAll writes msg to d in segments cut at the given positions.
func feedAll(t *testing.T, d *Decoder, msg []byte, cuts ...int) {
	t.Helper()
	prev := 0
	total := 0
	for _, cut := range append(cuts, len(msg)) {
		if cut == prev {
			continue
		}
		n, err := d.Write(msg[prev:cut])
		if err != nil {
			t.Fatalf("Write(%q): %v", msg[prev:cut], err)
		}
		total += n
		prev = cut
	}
	if total != len(msg) {
		t.Fatalf("consumed %d of %d bytes", total, len(msg))
	}
}

func decode(t *testing.T, msg []byte, cuts ...int) *Decoder {
	t.Helper()
	d := New(Config{})
	feedAll(t, d, msg, cuts...)
	if !d.Done() {
		t.Fatalf("not done after full message")
	}
	return d
}

func TestEveryTwoPartSplit(t *testing.T) {
	msg := []byte(testMessage)
	for cut := 0; cut <= len(msg); cut++ {
		d := decode(t, msg, cut)
		if !bytes.Equal(d.Body(), testBody) {
			t.Fatalf("cut %d: body %q", cut, d.Body())
		}
	}
}

func TestFixedSegmentSizes(t *testing.T) {
	msg := []byte(testMessage)
	for seg := 1; seg <= len(msg); seg++ {
		var cuts []int
		for c := seg; c < len(msg); c += seg {
			cuts = append(cuts, c)
		}
		d := decode(t, msg, cuts...)
		if !bytes.Equal(d.Body(), testBody) {
			t.Fatalf("segment %d: body %q", seg, d.Body())
		}
	}
}

func TestByteByByteEqualsWhole(t *testing.T) {
	msg := []byte(testMessage)
	whole := decode(t, msg)
	bytewise := New(Config{})
	for i := range msg {
		if _, err := bytewise.Write(msg[i : i+1]); err != nil {
			t.Fatalf("byte %d: %v", i, err)
		}
	}
	if !bytewise.Done() || !bytes.Equal(bytewise.Body(), whole.Body()) {
		t.Fatalf("bytewise body %q", bytewise.Body())
	}
}

func TestNotDoneBeforeFinalEmptyLine(t *testing.T) {
	// Everything except the final CRLF of the trailer section.
	prefix := testMessage[:len(testMessage)-2]
	d := New(Config{})
	if _, err := d.Write([]byte(prefix)); err != nil {
		t.Fatal(err)
	}
	if d.Done() {
		t.Fatal("done before trailer-terminating empty line")
	}
	if got := d.Body(); !bytes.Equal(got, testBody) {
		t.Fatalf("partial body %q", got)
	}
	if _, err := d.Write([]byte("\r\n")); err != nil {
		t.Fatal(err)
	}
	if !d.Done() {
		t.Fatal("not done after empty line")
	}
}

func TestWriteAfterDone(t *testing.T) {
	d := decode(t, []byte(testMessage))
	body := d.Body()
	n, err := d.Write([]byte("x"))
	if n != 0 || err == nil {
		t.Fatalf("Write after done: n=%d err=%v", n, err)
	}
	if !isErr(err, ErrClosed) {
		t.Fatalf("err=%v, want ErrClosed", err)
	}
	if !d.Done() || !bytes.Equal(d.Body(), body) {
		t.Fatal("Done/Body changed after closed write")
	}
}

func TestTrailingBytesNotConsumed(t *testing.T) {
	d := New(Config{})
	msg := []byte("1\r\na\r\n0\r\n\r\nEXTRA")
	n, err := d.Write(msg)
	if err != nil {
		t.Fatal(err)
	}
	if !d.Done() {
		t.Fatal("not done")
	}
	if want := len(msg) - len("EXTRA"); n != want {
		t.Fatalf("consumed %d, want %d", n, want)
	}
	if _, err := d.Write([]byte("E")); !isErr(err, ErrClosed) {
		t.Fatalf("err=%v, want ErrClosed", err)
	}
}

func TestTrailersNotInBody(t *testing.T) {
	d := decode(t, []byte("2\r\nhi\r\n0\r\nSecret: xyz\r\n\r\n"))
	if got := d.Body(); !bytes.Equal(got, []byte("hi")) {
		t.Fatalf("body %q contains trailer bytes", got)
	}
}

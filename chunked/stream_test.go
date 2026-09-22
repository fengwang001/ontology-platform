package chunked

import (
	"bytes"
	"errors"
	"testing"
)

// richMessage exercises extensions, multiple chunks and trailers.
func richMessage() []byte {
	return []byte(`5;a=1;b="x;y=z\"w"` + "\r\nhello\r\n" +
		"A\r\n0123456789\r\n" +
		"0\r\nX-One: 1\r\nX-Two: 2\r\n\r\n")
}

func richBody() []byte { return []byte("hello0123456789") }

func feedPieces(t *testing.T, d *Decoder, msg []byte, step int) {
	t.Helper()
	for i := 0; i < len(msg); i += step {
		j := i + step
		if j > len(msg) {
			j = len(msg)
		}
		if _, err := d.Write(msg[i:j]); err != nil {
			t.Fatalf("step %d, write [%d:%d]: %v", step, i, j, err)
		}
	}
}

func TestEverySplitPointIdentical(t *testing.T) {
	msg := richMessage()
	want := richBody()
	for cut := 0; cut <= len(msg); cut++ {
		d := New(Config{})
		if _, err := d.Write(msg[:cut]); err != nil {
			t.Fatalf("cut %d first half: %v", cut, err)
		}
		if _, err := d.Write(msg[cut:]); err != nil {
			t.Fatalf("cut %d second half: %v", cut, err)
		}
		if !d.Done() {
			t.Fatalf("cut %d: not done", cut)
		}
		if !bytes.Equal(d.Body(), want) {
			t.Fatalf("cut %d: body %q", cut, d.Body())
		}
	}
}

func TestEveryPieceSizeIdentical(t *testing.T) {
	msg := richMessage()
	want := richBody()
	for step := 1; step <= len(msg); step++ {
		d := New(Config{})
		feedPieces(t, d, msg, step)
		if !d.Done() {
			t.Fatalf("step %d: not done", step)
		}
		if !bytes.Equal(d.Body(), want) {
			t.Fatalf("step %d: body %q", step, d.Body())
		}
	}
}

func TestConsumedNeverAskedTwice(t *testing.T) {
	msg := richMessage()
	d := New(Config{})
	total := 0
	for i := 0; i < len(msg); i++ {
		n, err := d.Write(msg[i : i+1])
		if err != nil {
			t.Fatalf("byte %d: %v", i, err)
		}
		if n != 1 {
			t.Fatalf("byte %d: consumed %d", i, n)
		}
		total += n
	}
	if total != len(msg) || !d.Done() {
		t.Fatalf("total %d, done %v", total, d.Done())
	}
}

func TestTrailerNotDoneUntilBlankLine(t *testing.T) {
	d := New(Config{})
	if _, err := d.Write([]byte("3\r\nabc\r\n0\r\nX-A: 1\r\n")); err != nil {
		t.Fatal(err)
	}
	if d.Done() {
		t.Fatal("done before blank line")
	}
	if _, err := d.Write([]byte("\r")); err != nil {
		t.Fatal(err)
	}
	if d.Done() {
		t.Fatal("done after lone CR")
	}
	if _, err := d.Write([]byte("\n")); err != nil {
		t.Fatal(err)
	}
	if !d.Done() {
		t.Fatal("not done after blank line")
	}
	if string(d.Body()) != "abc" {
		t.Fatalf("body %q", d.Body())
	}
}

func TestTrailerContentNotInBody(t *testing.T) {
	d := New(Config{})
	feedPieces(t, d, []byte("2\r\nhi\r\n0\r\nSecret: xyz\r\n\r\n"), 3)
	if !d.Done() || string(d.Body()) != "hi" {
		t.Fatalf("done=%v body=%q", d.Done(), d.Body())
	}
}

func TestWriteAfterDoneRejected(t *testing.T) {
	d := New(Config{})
	feedPieces(t, d, []byte("1\r\na\r\n0\r\n\r\n"), 2)
	if !d.Done() {
		t.Fatal("not done")
	}
	body := d.Body()
	n, err := d.Write([]byte("x"))
	if !errors.Is(err, ErrClosed) {
		t.Fatalf("err %v", err)
	}
	if n != 0 {
		t.Fatalf("consumed %d", n)
	}
	if !d.Done() || !bytes.Equal(d.Body(), body) {
		t.Fatal("state changed after ErrClosed")
	}
}

func TestSurplusBytesInCompletingWrite(t *testing.T) {
	d := New(Config{})
	n, err := d.Write([]byte("1\r\na\r\n0\r\n\r\nJUNK"))
	if !errors.Is(err, ErrClosed) {
		t.Fatalf("err %v", err)
	}
	if n != len("1\r\na\r\n0\r\n\r\n") {
		t.Fatalf("consumed %d", n)
	}
	if !d.Done() || string(d.Body()) != "a" {
		t.Fatalf("done=%v body=%q", d.Done(), d.Body())
	}
}

func TestQuotedExtensionEndToEnd(t *testing.T) {
	msg := `5;note="a;b=c\"d";flag=on` + "\r\nhello\r\n0\r\n\r\n"
	d := New(Config{})
	feedPieces(t, d, []byte(msg), 1)
	if !d.Done() || string(d.Body()) != "hello" {
		t.Fatalf("done=%v body=%q", d.Done(), d.Body())
	}
}

func TestInstancesAreIsolated(t *testing.T) {
	d1 := New(Config{})
	d2 := New(Config{})
	feedPieces(t, d1, []byte("2\r\naa\r\n0\r\n\r\n"), 1)
	feedPieces(t, d2, []byte("3\r\nbbb\r\n0\r\n\r\n"), 1)
	if string(d1.Body()) != "aa" || string(d2.Body()) != "bbb" {
		t.Fatalf("bodies %q / %q", d1.Body(), d2.Body())
	}
}

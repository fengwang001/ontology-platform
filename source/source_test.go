package source

import (
	"errors"
	"io"
	"testing"
)

func TestBytesReadAt(t *testing.T) {
	b := Bytes("hello world")
	buf := make([]byte, 5)
	n, err := b.ReadAt(buf, 6)
	if err != nil || string(buf[:n]) != "world"[0:5] {
		t.Fatalf("got %q n=%d err=%v", buf[:n], n, err)
	}
	// Partial read at the tail reports io.EOF.
	n, err = b.ReadAt(buf, 8)
	if n != 3 || !errors.Is(err, io.EOF) {
		t.Fatalf("tail: n=%d err=%v", n, err)
	}
	if _, err = b.ReadAt(buf, 11); !errors.Is(err, io.EOF) {
		t.Fatalf("past end: %v", err)
	}
}

func TestFlakyShortRead(t *testing.T) {
	f := &Flaky{Src: Bytes("abcdefghij"), MaxChunk: 3}
	buf := make([]byte, 10)
	n, err := f.ReadAt(buf, 0)
	if err != nil || n != 3 || string(buf[:n]) != "abc" {
		t.Fatalf("short read: n=%d %q %v", n, buf[:n], err)
	}
}

func TestFlakyErrorInjection(t *testing.T) {
	boom := errors.New("disk on fire")
	f := &Flaky{Src: Bytes("abcdef"), FailOnCall: 2, Err: boom}
	buf := make([]byte, 2)
	if _, err := f.ReadAt(buf, 0); err != nil {
		t.Fatalf("first call should succeed: %v", err)
	}
	if _, err := f.ReadAt(buf, 2); !errors.Is(err, boom) {
		t.Fatalf("second call should fail with boom, got %v", err)
	}
	if _, err := f.ReadAt(buf, 4); err != nil {
		t.Fatalf("third call should succeed again: %v", err)
	}
}

func TestFlakySizeChangeMidRead(t *testing.T) {
	// Reports 100 bytes but the data shrank to 40: reads past 40 hit EOF.
	f := &Flaky{
		Src:         Bytes(make([]byte, 100)),
		HasFakeSize: true,
		FakeSize:    100,
		HasTrunc:    true,
		TruncAt:     40,
	}
	if f.Size() != 100 {
		t.Fatalf("Size = %d, want 100", f.Size())
	}
	buf := make([]byte, 50)
	n, err := f.ReadAt(buf, 0)
	if n != 40 || !errors.Is(err, io.EOF) {
		t.Fatalf("truncated read: n=%d err=%v", n, err)
	}
	if _, err := f.ReadAt(buf, 40); !errors.Is(err, io.EOF) {
		t.Fatalf("read at shrink point: %v", err)
	}
}

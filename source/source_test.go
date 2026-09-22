package source

import (
	"errors"
	"io"
	"testing"
)

func TestBytesReadAt(t *testing.T) {
	b := Bytes("hello")
	buf := make([]byte, 3)
	n, err := b.ReadAt(buf, 1)
	if n != 3 || err != nil || string(buf) != "ell" {
		t.Fatalf("n=%d err=%v buf=%q", n, err, buf)
	}
	n, err = b.ReadAt(buf, 4)
	if n != 1 || err != io.EOF || buf[0] != 'o' {
		t.Fatalf("tail: n=%d err=%v", n, err)
	}
	if n, err = b.ReadAt(buf, 5); n != 0 || err != io.EOF {
		t.Fatalf("eof: n=%d err=%v", n, err)
	}
	if b.Size() != 5 {
		t.Fatalf("size=%d", b.Size())
	}
}

func TestShortInjectsShortReads(t *testing.T) {
	s := Short{Src: Bytes("abcdef"), Max: 2}
	buf := make([]byte, 6)
	n, err := s.ReadAt(buf, 0)
	if n != 2 || err != nil || string(buf[:2]) != "ab" {
		t.Fatalf("n=%d err=%v buf=%q", n, err, buf[:n])
	}
	if s.Size() != 6 {
		t.Fatalf("size=%d", s.Size())
	}
}

func TestFailAtInjectsError(t *testing.T) {
	boom := errors.New("disk on fire")
	s := FailAt{Src: Bytes("abcdef"), At: 3, Err: boom}
	buf := make([]byte, 3)
	if n, err := s.ReadAt(buf, 0); n != 3 || err != nil {
		t.Fatalf("before: n=%d err=%v", n, err)
	}
	if n, err := s.ReadAt(buf, 3); n != 0 || !errors.Is(err, boom) {
		t.Fatalf("at: n=%d err=%v", n, err)
	}
}

func TestShrinkAfterChangesLengthMidRead(t *testing.T) {
	s := &ShrinkAfter{Src: Bytes("abcdef"), N: 1, NewSize: 2}
	buf := make([]byte, 4)
	if n, err := s.ReadAt(buf, 0); n != 4 || err != nil {
		t.Fatalf("first: n=%d err=%v", n, err)
	}
	// Second read sees the shrunken length: only 2 bytes remain from off 0.
	n, err := s.ReadAt(buf, 0)
	if n != 2 || err != io.EOF {
		t.Fatalf("shrunk: n=%d err=%v", n, err)
	}
	if n, err := s.ReadAt(buf, 2); n != 0 || err != io.EOF {
		t.Fatalf("past new size: n=%d err=%v", n, err)
	}
	// Size still reports the original length (caller snapshotted it).
	if s.Size() != 6 {
		t.Fatalf("size=%d", s.Size())
	}
}

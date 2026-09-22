package source

import (
	"context"
	"errors"
	"io"
	"testing"
)

func TestMemoryLengthAndReadAt(t *testing.T) {
	src := NewMemory([]byte("abcdef"))
	length, err := src.Length(context.Background())
	if err != nil || length != 6 {
		t.Fatalf("length=%d err=%v", length, err)
	}

	buf := make([]byte, 3)
	n, err := src.ReadAt(context.Background(), buf, 2)
	if err != nil || n != 3 || string(buf) != "cde" {
		t.Fatalf("n=%d buf=%q err=%v", n, buf, err)
	}
}

func TestMemoryShortRead(t *testing.T) {
	src := NewMemory([]byte("abcdef"))
	src.SetReadLimit(2)
	buf := make([]byte, 4)
	n, err := src.ReadAt(context.Background(), buf, 0)
	if err != nil || n != 2 || string(buf[:n]) != "ab" {
		t.Fatalf("n=%d err=%v", n, err)
	}

	n, err = src.ReadAt(context.Background(), buf, 6)
	if err != io.EOF || n != 0 {
		t.Fatalf("EOF read n=%d err=%v", n, err)
	}
}

func TestMemoryReadError(t *testing.T) {
	injected := errors.New("boom")
	src := NewMemory([]byte("abcdef"))
	src.SetReadError(injected)
	n, err := src.ReadAt(context.Background(), make([]byte, 2), 0)
	if !errors.Is(err, injected) || n != 0 {
		t.Fatalf("n=%d err=%v", n, err)
	}
}

func TestMemoryCanChangeLengthBetweenReads(t *testing.T) {
	src := NewMemory([]byte("abcdefghij"))
	src.SetReadLimit(2)
	src.SetBeforeRead(func(m *Memory) {
		if m.Calls() == 0 {
			m.Replace([]byte("XYZ"))
		}
	})

	buf := make([]byte, 2)
	if n, err := src.ReadAt(context.Background(), buf, 0); err != nil || string(buf) != "XY" || n != 2 {
		t.Fatalf("first n=%d buf=%q err=%v", n, buf, err)
	}
	buf = make([]byte, 4)
	n, err := src.ReadAt(context.Background(), buf, 2)
	if err != nil || n != 1 || string(buf[:n]) != "Z" {
		t.Fatalf("second n=%d buf=%q err=%v", n, buf[:n], err)
	}
}

func TestErrShortEOFIsDistinguishable(t *testing.T) {
	src := NewMemory([]byte("abc"))
	_, err := src.ReadAt(context.Background(), make([]byte, 1), 5)
	if !errors.Is(err, ErrShortEOF) {
		t.Fatalf("err=%v", err)
	}
}

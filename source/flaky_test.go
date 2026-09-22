package source_test

import (
	"errors"
	"io"
	"testing"

	"ontology/source"
)

func payload(n byte) []byte {
	b := make([]byte, 20)
	for i := range b {
		b[i] = n + byte(i)
	}
	return b
}

func TestMemoryReadAt(t *testing.T) {
	m := source.NewMemory(payload(0))
	buf := make([]byte, 5)
	n, err := m.ReadAt(buf, 10)
	if n != 5 || err != nil {
		t.Fatalf("n=%d err=%v", n, err)
	}
	if buf[0] != payload(0)[10] {
		t.Fatal("data mismatch")
	}

	// 越过末尾：返回部分 + io.EOF。
	buf = make([]byte, 10)
	n, err = m.ReadAt(buf, 15)
	if n != 5 || !errors.Is(err, io.EOF) {
		t.Fatalf("partial tail: n=%d err=%v", n, err)
	}
}

func TestFlakyShortReads(t *testing.T) {
	f := source.NewFlaky(source.NewMemory(payload(1)))
	f.MaxChunk = 3
	buf := make([]byte, 10)

	got := 0
	for got < 10 {
		n, err := f.ReadAt(buf[got:], int64(got))
		if err != nil {
			t.Fatalf("unexpected err %v", err)
		}
		if n > 3 {
			t.Fatalf("short read violated: n=%d", n)
		}
		got += n
	}
	for i := 0; i < 10; i++ {
		if buf[i] != payload(1)[i] {
			t.Fatalf("byte %d mismatch", i)
		}
	}
}

func TestFlakyInjectedError(t *testing.T) {
	f := source.NewFlaky(source.NewMemory(payload(2)))
	f.FailCall = 1
	buf := make([]byte, 4)
	if _, err := f.ReadAt(buf, 0); err != nil {
		t.Fatal(err)
	}
	_, err := f.ReadAt(buf, 0)
	if !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("want injected error, got %v", err)
	}
}

func TestFlakyLengthShrink(t *testing.T) {
	f := source.NewFlaky(source.NewMemory(payload(3)))
	if f.Length() != 20 {
		t.Fatalf("initial length %d", f.Length())
	}
	f.NewLength = 10
	f.ShrinkCall = 1
	buf := make([]byte, 10)
	_, _ = f.ReadAt(buf, 0) // 第 0 次，长度仍 20
	if f.Length() != 20 {
		t.Fatal("length changed too early")
	}
	_, _ = f.ReadAt(buf, 0) // 第 1 次起长度变为 10
	if f.Length() != 10 {
		t.Fatalf("length after shrink = %d", f.Length())
	}
	n, err := f.ReadAt(buf, 15)
	if n != 0 || !errors.Is(err, io.EOF) {
		t.Fatalf("post-shrink read past end: n=%d err=%v", n, err)
	}
}

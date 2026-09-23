package durable_test

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"ontology/durable"
)

func TestOpenAndAdvance(t *testing.T) {
	dir := t.TempDir()
	c, err := durable.Open(dir, 42)
	if err != nil {
		t.Fatal(err)
	}
	if got := c.Value(); got != 42 {
		t.Fatalf("first start: got %d want 42", got)
}
	cases := []struct {
		n       uint64
		wantFrom uint64
		wantVal  uint64
	}{
		{8, 42, 50},
		{1, 50, 51},
		{1000, 51, 1051},
	}
	for _, tc := range cases {
		from, err := c.Advance(tc.n)
		if err != nil || from != tc.wantFrom || c.Value() != tc.wantVal {
			t.Fatalf("Advance(%d): from=%d val=%d err=%v", tc.n, from, c.Value(), err)
		}
	}
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	// 重新打开必须读到持久值，而不是起点 7。
	c2, err := durable.Open(dir, 7)
	if err != nil {
		t.Fatal(err)
	}
	defer c2.Close()
	if c2.Value() != 1051 {
		t.Fatalf("reopen: got %d want 1051", c2.Value())
	}
}

func TestAdvanceOverflow(t *testing.T) {
	c, err := durable.Open(t.TempDir(), ^uint64(0)-4)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	cases := []struct {
		n      uint64
		wantIs error
	}{
		{4, nil},
		{1, durable.ErrOverflow},
		{1<<63 - 1, durable.ErrOverflow},
	}
	for _, tc := range cases {
		_, err := c.Advance(tc.n)
		if !errors.Is(err, tc.wantIs) {
			t.Fatalf("Advance(%d): err=%v want %v", tc.n, err, tc.wantIs)
		}
	}
	if c.Value() != ^uint64(0) {
		t.Fatalf("overflow mutated value: %d", c.Value())
	}
}

func TestWriteFailure(t *testing.T) {
	dir := t.TempDir()
	c, err := durable.Open(dir, 100)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.SetFailHook(func() error { return errors.New("disk full") })
	if _, err := c.Advance(10); !errors.Is(err, durable.ErrWriteFailed) {
		t.Fatalf("want ErrWriteFailed, got %v", err)
	}
	if c.Value() != 100 {
		t.Fatalf("in-memory value changed after failed write: %d", c.Value())
	}
	// 原文件此前不存在；写失败后重新打开仍必须从 100 起，且能正常写入。
	c2, err := durable.Open(dir, 100)
	if err != nil {
		t.Fatal(err)
	}
	defer c2.Close()
	if _, err := c2.Advance(5); err != nil {
		t.Fatalf("recover write: %v", err)
	}
	if c2.Value() != 105 {
		t.Fatalf("got %d want 105", c2.Value())
	}
}

func TestTruncationRejectsAll(t *testing.T) {
	// 先制造一个完整的计数器文件。
	dir := t.TempDir()
	c, err := durable.Open(dir, 123456)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Advance(200); err != nil {
		t.Fatal(err)
	}
	c.Close()
	data, err := os.ReadFile(filepath.Join(dir, "counter.dat"))
	if err != nil {
		t.Fatal(err)
	}
	if len(data) != 16 {
		t.Fatalf("file size = %d, want 16", len(data))
	}
	// 逐字节截断：1..15 每个点都必须拒绝启动，且绝不能回退到起点 0。
	for n := 1; n < len(data); n++ {
		d := filepath.Join(t.TempDir(), "x")
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(d, "counter.dat"), data[:n], 0o644); err != nil {
			t.Fatal(err)
		}
		bad, err := durable.Open(d, 0)
		switch {
		case err == nil:
			if bad.Value() == 0 {
				t.Fatalf("truncate %d: silently reset to 0", n)
			}
			bad.Close()
			t.Fatalf("truncate %d: opened with value %d, want reject", n, bad.Value())
		case n < 4 && !errors.Is(err, durable.ErrHeaderIncomplete):
			t.Fatalf("truncate %d: %v, want ErrHeaderIncomplete", n, err)
		case n >= 4 && n < 12 && !errors.Is(err, durable.ErrValueIncomplete):
			t.Fatalf("truncate %d: %v, want ErrValueIncomplete", n, err)
		case n >= 12 && !errors.Is(err, durable.ErrCRCMismatch):
			t.Fatalf("truncate %d: %v, want ErrCRCMismatch", n, err)
		}
	}
}

func TestCorruptByte(t *testing.T) {
	dir := t.TempDir()
	c, _ := durable.Open(dir, 5)
	c.Advance(10)
	c.Close()
	p := filepath.Join(dir, "counter.dat")
	raw, _ := os.ReadFile(p)
	// 翻转每个字节（逐字节遍历），魔数损坏属头部类，其余为值/CRC 类，全部拒绝。
	for i := range raw {
		b := append([]byte(nil), raw...)
		b[i] ^= 0xFF
		d := filepath.Join(t.TempDir(), "d")
		os.MkdirAll(d, 0o755)
		os.WriteFile(filepath.Join(d, "counter.dat"), b, 0o644)
		bad, err := durable.Open(d, 0)
		if err == nil {
			v := bad.Value()
			bad.Close()
			t.Fatalf("flip byte %d: opened value %d (must not reset)", i, v)
		}
		if !errors.Is(err, durable.ErrHeaderIncomplete) &&
			!errors.Is(err, durable.ErrValueIncomplete) &&
			!errors.Is(err, durable.ErrCRCMismatch) {
			t.Fatalf("flip byte %d: unclassified error %v", i, err)
		}
	}
}

func TestConcurrentAdvanceNoOverlap(t *testing.T) {
	dir := t.TempDir()
	const goroutines, perN, size = 8, 50, uint64(10)
	type seg struct{ from, to uint64 }
	var wg sync.WaitGroup
	segs := make(chan seg, goroutines*perN)
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c, err := durable.Open(dir, 0)
			if err != nil {
				t.Error(err)
				return
			}
			defer c.Close()
			for i := 0; i < perN; i++ {
				from, err := c.Advance(size)
				if err != nil {
					t.Error(err)
					return
				}
				segs <- seg{from, from + size}
			}
		}()
	}
	wg.Wait()
	close(segs)
	seen := make(map[uint64]bool, goroutines*perN)
	for s := range segs {
		for v := s.from; v < s.to; v++ {
			if seen[v] {
				t.Fatalf("overlap at %d", v)
			}
			seen[v] = true
		}
	}
	if len(seen) != goroutines*perN*int(size) {
		t.Fatalf("covered %d values, want %d", len(seen), goroutines*perN*int(size))
	}
}

package pipeline

import (
	"errors"
	"fmt"
	"testing"
)

func ingestN(t *testing.T, p *Pipeline, n int, keyAt func(i int) string) {
	t.Helper()
	for i := 0; i < n; i++ {
		if _, err := p.Ingest(keyAt(i), []byte(fmt.Sprintf("v%d", i))); err != nil {
			t.Fatalf("ingest %d: %v", i, err)
		}
	}
}

func checkOutput(t *testing.T, dir string, n int) Stats {
	t.Helper()
	p, err := Open(Config{Dir: dir, MemoryByte: 1 << 20})
	if err != nil {
		t.Fatal(err)
	}
	if !p.IsFinalized() {
		t.Fatal("not finalized after open")
	}
	out, err := p.ReadOutput()
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != n {
		t.Fatalf("output count %d want %d", len(out), n)
	}
	for i := 1; i < len(out); i++ {
		if out[i].Key < out[i-1].Key {
			t.Fatalf("key order broken at %d", i)
		}
		if out[i].Key == out[i-1].Key && out[i].Seq < out[i-1].Seq {
			t.Fatalf("equal-key arrival order broken at %d", i)
		}
	}
	return p.Stats()
}

func checkOutputStats(t *testing.T, dir string, n int) Stats {
	t.Helper()
	p, err := Open(Config{Dir: dir, MemoryByte: 1 << 20})
	if err != nil {
		t.Fatal(err)
	}
	out, err := p.ReadOutput()
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != n {
		t.Fatalf("output count %d want %d", len(out), n)
	}
	for i := 1; i < len(out); i++ {
		if out[i].Key < out[i-1].Key {
			t.Fatalf("key order broken at %d", i)
		}
		if out[i].Key == out[i-1].Key && out[i].Seq < out[i-1].Seq {
			t.Fatalf("equal-key arrival order broken at %d", i)
		}
	}
	return p.Stats()
}

func TestSmokeSmall(t *testing.T) {
	dir := t.TempDir()
	p, err := Open(Config{Dir: dir, MemoryByte: 4096})
	if err != nil {
		t.Fatal(err)
	}
	ingestN(t, p, 200, func(i int) string { return fmt.Sprintf("k%04d", 199-i) })
	if err := p.Close(); err != nil {
		t.Fatal(err)
	}
	checkOutput(t, dir, 200)
}

func TestOversizeRecordRejected(t *testing.T) {
	dir := t.TempDir()
	p, err := Open(Config{Dir: dir, MemoryByte: 128})
	if err != nil {
		t.Fatal(err)
	}
	big := make([]byte, 5000)
	if _, err := p.Ingest("big", big); !errors.Is(err, ErrRecordTooLarge) {
		t.Fatalf("want ErrRecordTooLarge, got %v", err)
	}
	if _, err := p.Ingest("ok", []byte("x")); err != nil {
		t.Fatal(err)
	}
	if err := p.Close(); err != nil {
		t.Fatal(err)
	}
	checkOutput(t, dir, 1)
}

func TestZeroAndSingle(t *testing.T) {
	for _, n := range []int{0, 1} {
		dir := t.TempDir()
		p, err := Open(Config{Dir: dir, MemoryByte: 4096})
		if err != nil {
			t.Fatal(err)
		}
		ingestN(t, p, n, func(i int) string { return "k" })
		if err := p.Close(); err != nil {
			t.Fatal(err)
		}
		checkOutput(t, dir, n)
	}
}

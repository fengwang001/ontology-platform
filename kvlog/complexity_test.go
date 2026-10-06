package kvlog

import (
	"os"
	"testing"
)

// TestHintRecoveryCostIsDistinctKeys proves the hint-adoption guarantee:
// recovery of a sealed segment with a valid hint reads ZERO bytes of the
// segment log regardless of record count or segment size.
func TestHintRecoveryCostIsDistinctKeys(t *testing.T) {
	dir := t.TempDir()
	e, err := Open(dir, Config{MaxSegmentBytes: 1 << 30, WriteHints: true})
	if err != nil {
		t.Fatal(err)
	}
	const distinct = 8
	// 2000 records but only 8 distinct keys -> one huge sealed segment.
	for i := 0; i < 2000; i++ {
		k := []byte{'k', byte('a' + i%distinct)}
		v := make([]byte, 64)
		for j := range v {
			v[j] = byte('x')
		}
		if _, err := e.Put(k, v); err != nil {
			t.Fatal(err)
		}
	}
	if err := e.forceSeal(); err != nil {
		t.Fatal(err)
	}
	info, _ := os.Stat(dir + "/00000001.log")
	segBytes := info.Size()
	if segBytes < 100000 {
		t.Fatalf("segment too small to be convincing: %d", segBytes)
	}
	e.Close()

	e2, err := Open(dir, Config{WriteHints: true})
	if err != nil {
		t.Fatal(err)
	}
	defer e2.Close()
	s := e2.segIndex[1]
	if s == nil || !s.hintAdopted {
		t.Fatal("segment was not recovered via hint")
	}
	// The .log file of segment 1 must never have been read during open.
	// bytesRead counts all segment-data reads; the only other segment is
	// the (empty) active segment 2.
	if e2.BytesRead() != 0 {
		t.Fatalf("hint recovery read %d segment bytes, want 0", e2.BytesRead())
	}
	if got := e2.dir2.len(); got != distinct {
		t.Fatalf("keys=%d want %d", got, distinct)
	}

	// Contrast: with the hint removed, recovery must read the whole log.
	os.Remove(dir + "/00000001.hint")
	e3, err := Open(dir, Config{WriteHints: true})
	if err != nil {
		t.Fatal(err)
	}
	defer e3.Close()
	if e3.BytesRead() < segBytes {
		t.Fatalf("hintless recovery read %d bytes, want >= %d",
			e3.BytesRead(), segBytes)
	}
}

// TestGetCostIsConstant proves one Get reads exactly the locator's frame
// bytes and does not scale with total keys or total segments.
func TestGetCostIsConstant(t *testing.T) {
	measure := func(totalKeys int) int64 {
		dir := t.TempDir()
		e, err := Open(dir, Config{MaxSegmentBytes: 120})
		if err != nil {
			t.Fatal(err)
		}
		for i := 0; i < totalKeys; i++ {
			k := []byte{'k'}
			k = append(k, []byte(itoa(i))...)
			if _, err := e.Put(k, []byte("V")); err != nil {
				t.Fatal(err)
			}
		}
		// Force several sealed segments.
		before := e.BytesRead()
		r, err := e.Get([]byte("k0"))
		if err != nil {
			t.Fatal(err)
		}
		if r.Status != StatusPresent || string(r.Value) != "V" {
			t.Fatalf("get k0 = %+v", r)
		}
		after := e.BytesRead()
		e.Close()
		return after - before
	}

	small := measure(20)
	large := measure(400)
	// One Get must read one frame (~30 bytes) regardless of store size.
	if small <= 0 || large <= 0 {
		t.Fatalf("no reads recorded small=%d large=%d", small, large)
	}
	if small != large {
		t.Fatalf("get read %d bytes at 20 keys but %d at 400 keys (must be constant)",
			small, large)
	}
}

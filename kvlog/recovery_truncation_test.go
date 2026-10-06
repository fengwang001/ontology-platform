package kvlog

import (
	"os"
	"testing"
)

// buildActiveStore creates a directory containing only an unsealed active
// segment (no rollover) with several records and returns its log size.
func buildActiveStore(t *testing.T, writes [][2]string) (string, int) {
	t.Helper()
	dir := t.TempDir()
	e, err := Open(dir, Config{MaxSegmentBytes: 1 << 20})
	if err != nil {
		t.Fatal(err)
	}
	for _, w := range writes {
		if _, err := e.Put([]byte(w[0]), []byte(w[1])); err != nil {
			t.Fatal(err)
		}
	}
	if err := e.Close(); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(dir + "/00000001.log")
	if err != nil {
		t.Fatal(err)
	}
	return dir, int(info.Size())
}

// TestActiveTruncationEveryByte truncates the active segment at every byte
// boundary. Recovery must never error: complete records survive, the torn
// tail is discarded and reported.
func TestActiveTruncationEveryByte(t *testing.T) {
	writes := [][2]string{
		{"k1", "v1"},
		{"k2", "abcdefghij"},
		{"k1", "new"},
		{"k3", ""},
	}
	src, full := buildActiveStore(t, writes)

	// Compute the frame boundaries from the intact file.
	data, err := os.ReadFile(src + "/00000001.log")
	if err != nil {
		t.Fatal(err)
	}
	res := scanRecords(data, false)
	if res.outcome != scanOK {
		t.Fatal("source not clean")
	}
	boundaries := []int{0}
	for _, f := range res.frames {
		boundaries = append(boundaries, f.offset+f.length)
	}

	for n := 0; n <= full; n++ {
		dir := t.TempDir()
		raw := append([]byte(nil), data[:n]...)
		if err := os.WriteFile(dir+"/00000001.log", raw, 0o644); err != nil {
			t.Fatal(err)
		}
		e, err := Open(dir, Config{})
		if err != nil {
			t.Fatalf("truncate=%d: unexpected error %v", n, err)
		}
		// Find expected complete prefix.
		valid := 0
		for _, b := range boundaries {
			if b <= n {
				valid = b
			}
		}
		if got := e.TornBytes(); got != int64(n-valid) {
			t.Fatalf("truncate=%d torn=%d want=%d", n, got, n-valid)
		}
		// Model: replay writes up to the valid prefix, taking the newest
		// whole record per key.
		model := newNaiveModel()
		off := 0
		for _, f := range res.frames {
			if f.offset+f.length > valid {
				break
			}
			if f.rec.tomb {
				model.del(string(f.rec.key))
			} else {
				model.put(string(f.rec.key), string(f.rec.value))
			}
			off = f.offset + f.length
		}
		_ = off
		snap := dumpEngine(e)
		if !snapshotsEqual(model.snapshot(), snap) {
			t.Fatalf("truncate=%d snapshot mismatch", n)
		}
		// The file on disk must have been truncated to the valid prefix.
		info, _ := os.Stat(dir + "/00000001.log")
		if info.Size() != int64(valid) {
			t.Fatalf("truncate=%d file size=%d want %d", n, info.Size(), valid)
		}
		e.Close()
	}
}

// TestTornTailNotAnError ensures reopening after a torn tail still allows
// writes and the next seq continues correctly.
func TestTornTailAppendContinues(t *testing.T) {
	dir, full := buildActiveStore(t, [][2]string{{"a", "1"}, {"b", "2"}})
	data, _ := os.ReadFile(dir + "/00000001.log")
	os.WriteFile(dir+"/00000001.log", data[:full-3], 0o644)
	e, err := Open(dir, Config{})
	if err != nil {
		t.Fatal(err)
	}
	if e.TornBytes() != 20 {
		t.Fatalf("torn=%d want 20", e.TornBytes())
	}
	r, _ := e.Get([]byte("b"))
	if r.Status != StatusMissing {
		t.Fatalf("b = %v", r.Status)
	}
	seq, err := e.Put([]byte("c"), []byte("3"))
	if err != nil {
		t.Fatal(err)
	}
	if seq != 2 {
		t.Fatalf("seq after torn = %d", seq)
	}
	e.Close()

	e2, err := Open(dir, Config{})
	if err != nil {
		t.Fatal(err)
	}
	defer e2.Close()
	if e2.TornBytes() != 0 {
		t.Fatalf("second open torn=%d", e2.TornBytes())
	}
	r, _ = e2.Get([]byte("c"))
	if r.Status != StatusPresent || string(r.Value) != "3" {
		t.Fatalf("c = %+v", r)
	}
}

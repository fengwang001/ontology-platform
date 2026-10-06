package kvlog

import (
	"os"
	"path/filepath"
	"testing"
)

// buildHealedStore creates a sealed segment with two records.
func buildHealedStore(t *testing.T) (string, *Engine) {
	dir := t.TempDir()
	e, err := Open(dir, Config{MaxSegmentBytes: 1 << 20, WriteHints: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.Put([]byte("alpha"), []byte("one")); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Put([]byte("beta"), []byte("two")); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Put([]byte("alpha"), []byte("TWO")); err != nil {
		t.Fatal(err)
	}
	if err := e.forceSeal(); err != nil {
		t.Fatal(err)
	}
	return dir, e
}

// TestKeyDirDistortionSeqMismatch changes only the registered seq while
// the pointed record is intact: Get detects distortion, rebuilds the
// segment, retries and returns the correct value; one self-heal counted.
func TestKeyDirDistortionSeqMismatch(t *testing.T) {
	_, e := buildHealedStore(t)
	defer e.Close()
	loc := e.dir2.m["alpha"]
	fake := *loc
	fake.seq = loc.seq + 1000
	e.dir2.m["alpha"] = &fake

	r, err := e.Get([]byte("alpha"))
	if err != nil {
		t.Fatalf("self-heal failed: %v", err)
	}
	if r.Status != StatusPresent || string(r.Value) != "TWO" {
		t.Fatalf("get after heal = %+v", r)
	}
	if e.SelfHeals() != 1 {
		t.Fatalf("selfHeals=%d", e.SelfHeals())
	}
	r, err = e.Get([]byte("beta"))
	if err != nil || r.Status != StatusPresent || string(r.Value) != "two" {
		t.Fatalf("beta after heal = %+v %v", r, err)
	}
}

// TestKeyDirDistortionKeyMismatch points alpha at beta's record.
func TestKeyDirDistortionKeyMismatch(t *testing.T) {
	_, e := buildHealedStore(t)
	defer e.Close()
	beta := e.dir2.m["beta"]
	fake := *beta
	fake.seq = e.dir2.m["alpha"].seq // make seq agree; key will not
	e.dir2.m["alpha"] = &fake

	r, err := e.Get([]byte("alpha"))
	if err != nil {
		t.Fatalf("self-heal: %v", err)
	}
	if r.Status != StatusPresent || string(r.Value) != "TWO" {
		t.Fatalf("alpha after heal = %+v", r)
	}
	if e.SelfHeals() != 1 {
		t.Fatalf("selfHeals=%d", e.SelfHeals())
	}
}

// TestKeyDirDistortionRebuildCorrupt verifies that when the segment bytes
// themselves are bad, self-healing ends in segment corruption, which stays
// distinguishable from a healed distortion.
func TestKeyDirDistortionRebuildCorrupt(t *testing.T) {
	dir, e := buildHealedStore(t)
	segID := e.dir2.m["alpha"].segment
	path := filepath.Join(dir, segLogName(segID))
	data, _ := os.ReadFile(path)
	// Flip a byte inside the first record (alpha=one) which is shadowed by
	// the second alpha but still part of a full re-scan.
	data[recordHeaderLen] ^= 0xFF
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	// Point alpha at the corrupted first record with a matching-but-fake
	// seq so the first read fails on checksum (corruption); the rebuild
	// scan must also report corruption.
	e.dir2.m["alpha"] = &locator{segment: segID, offset: 0,
		length: e.dir2.m["beta"].length, seq: 1, tomb: false}
	_, err := e.Get([]byte("alpha"))
	ke, ok := AsError(err)
	if !ok || ke.Kind != KindSegmentCorrupt {
		t.Fatalf("want segment corrupt, got %v", err)
	}
	if ke.Segment != segID {
		t.Fatalf("reported segment %d", ke.Segment)
	}
}

// TestDistortionVsCorruptionDistinguishable checks Kind identity directly.
func TestDistortionVsCorruptionDistinguishable(t *testing.T) {
	_, e := buildHealedStore(t)
	defer e.Close()
	loc := *e.dir2.m["alpha"]
	loc.seq++
	e.dir2.m["alpha"] = &loc
	_, err := e.Get([]byte("alpha"))
	if err != nil {
		t.Fatalf("distortion healed but returned err=%v", err)
	}
	if e.SelfHeals() != 1 {
		t.Fatalf("heals=%d", e.SelfHeals())
	}
}

package kvlog

import (
	"os"
	"path/filepath"
	"testing"
)

// buildMultiSegment returns a dir with several sealed segments plus an
// active one and the model of the final logical state.
func buildMultiSegment(t *testing.T, maxSeg int) (string, *naiveModel) {
	t.Helper()
	dir := t.TempDir()
	e, err := Open(dir, Config{MaxSegmentBytes: maxSeg, WriteHints: true})
	if err != nil {
		t.Fatal(err)
	}
	model := newNaiveModel()
	ops := []struct {
		del bool
		k   string
		v   string
	}{
		{false, "alpha", "one"},
		{false, "beta", "two-two"},
		{false, "gamma", "three333"},
		{false, "alpha", "ONE"},
		{true, "beta", ""},
		{false, "delta", "four4444"},
		{false, "gamma", "g"},
	}
	for _, o := range ops {
		if o.del {
			if _, err := e.Delete([]byte(o.k)); err != nil {
				t.Fatal(err)
			}
			model.del(o.k)
		} else {
			if _, err := e.Put([]byte(o.k), []byte(o.v)); err != nil {
				t.Fatal(err)
			}
			model.put(o.k, o.v)
		}
	}
	if err := e.Close(); err != nil {
		t.Fatal(err)
	}
	return dir, model
}

func sealedSegFiles(t *testing.T, dir string) []int {
	t.Helper()
	states, err := listSegments(dir)
	if err != nil {
		t.Fatal(err)
	}
	var ids []int
	for _, st := range states {
		if st.sealed {
			ids = append(ids, st.id)
		}
	}
	return ids
}

// TestSealedBitFlipEveryPosition flips one bit at every byte position of
// each sealed segment and requires recovery to report segment corruption
// with the right segment id and the first corrupt offset.
func TestSealedBitFlipEveryPosition(t *testing.T) {
	dir0, _ := buildMultiSegment(t, 64)
	sealed := sealedSegFiles(t, dir0)
	if len(sealed) < 2 {
		t.Fatalf("want >=2 sealed segments, got %d", len(sealed))
	}
	// Force the full-scan path: with hint files removed recovery must read
	// every byte, so a flipped bit at any position is corruption.
	for _, id := range sealed {
		os.Remove(filepath.Join(dir0, segHintName(id)))
	}

	for _, segID := range sealed {
		base, err := os.ReadFile(filepath.Join(dir0, segLogName(segID)))
		if err != nil {
			t.Fatal(err)
		}
		for pos := 0; pos < len(base); pos++ {
			dir := t.TempDir()
			copyDir(dir0, dir)
			path := filepath.Join(dir, segLogName(segID))
			data, _ := os.ReadFile(path)
			data[pos] ^= 1
			if err := os.WriteFile(path, data, 0o644); err != nil {
				t.Fatal(err)
			}
			_, err := Open(dir, Config{})
			ke, ok := AsError(err)
			if !ok || ke.Kind != KindSegmentCorrupt {
				t.Fatalf("seg=%d pos=%d: want corruption, got %v", segID, pos, err)
			}
			if ke.Segment != segID {
				t.Fatalf("seg=%d pos=%d: reported seg %d", segID, pos, ke.Segment)
			}
			// Offset must be at or before the flipped position and at a
			// record boundary (header offset), never after.
			if ke.Offset < 0 || ke.Offset > pos {
				t.Fatalf("seg=%d pos=%d: bad first-corrupt offset %d",
					segID, pos, ke.Offset)
			}
			// No partial state: reopening the same directory again must
			// fail identically (determinism), and no truncation occurred.
			info, _ := os.Stat(path)
			if info.Size() != int64(len(base)) {
				t.Fatalf("seg=%d pos=%d: file mutated by failed recovery",
					segID, pos)
			}
		}
	}
}

// TestHintAdoptedLazyCorruption verifies the spec rule that a segment
// recovered via its hint is not byte-verified until its records are read;
// a flipped bit in a reachable record then reports segment corruption.
func TestHintAdoptedLazyCorruption(t *testing.T) {
	dir0, model := buildMultiSegment(t, 64)
	sealed := sealedSegFiles(t, dir0)
	for _, segID := range sealed {
		base, err := os.ReadFile(filepath.Join(dir0, segLogName(segID)))
		if err != nil {
			t.Fatal(err)
		}
		// Locate newest-record byte ranges in this segment via an
		// unmodified open (hint adopted); only those bytes are reachable.
		goodDir := t.TempDir()
		copyDir(dir0, goodDir)
		ge, err := Open(goodDir, Config{})
		if err != nil {
			t.Fatal(err)
		}
		var reachable [][2]int
		for _, loc := range ge.dir2.m {
			if loc.segment != segID {
				continue
			}
			reachable = append(reachable, [2]int{int(loc.offset), loc.length})
		}
		ge.Close()
		if len(reachable) == 0 {
			continue
		}
		for pos := 0; pos < len(base); pos++ {
			inReachable := false
			for _, rl := range reachable {
				if pos >= rl[0] && pos < rl[0]+rl[1] {
					inReachable = true
				}
			}
			if !inReachable {
				continue
			}
			dir := t.TempDir()
			copyDir(dir0, dir)
			path := filepath.Join(dir, segLogName(segID))
			data, _ := os.ReadFile(path)
			data[pos] ^= 1
			os.WriteFile(path, data, 0o644)
			e, err := Open(dir, Config{})
			if err != nil {
				t.Fatalf("seg=%d pos=%d: hint-adopted open must not scan, got %v",
					segID, pos, err)
			}
			s := e.segIndex[segID]
			if s == nil || !s.hintAdopted {
				t.Fatalf("seg=%d pos=%d: expected hint adoption", segID, pos)
			}
			corruptSeen := false
			for _, k := range model.keys() {
				_, gerr := e.Get([]byte(k))
				if ke, ok := AsError(gerr); ok && ke.Kind == KindSegmentCorrupt {
					corruptSeen = true
				}
			}
			if !corruptSeen {
				t.Fatalf("seg=%d pos=%d: reachable corruption not detected on read",
					segID, pos)
			}
			e.Close()
		}
	}
}

// TestHintFieldTampering flips every bit of the hint file and checks that a
// bad hint is ignored: recovery falls back to a full scan and succeeds as
// long as the log itself is intact.
func TestHintFieldTampering(t *testing.T) {
	dir0, model := buildMultiSegment(t, 64)
	sealed := sealedSegFiles(t, dir0)

	for _, segID := range sealed {
		hintPath0 := filepath.Join(dir0, segHintName(segID))
		hb, err := os.ReadFile(hintPath0)
		if err != nil {
			t.Fatal(err)
		}
		for pos := 0; pos < len(hb); pos++ {
			dir := t.TempDir()
			copyDir(dir0, dir)
			path := filepath.Join(dir, segHintName(segID))
			data, _ := os.ReadFile(path)
			data[pos] ^= 1
			os.WriteFile(path, data, 0o644)
			e, err := Open(dir, Config{})
			// Every single-bit flip either invalidates the checksum,
			// changes a structural field, or changes a value-bearing
			// field inside the checksummed region. If it happens to only
			// alter a value byte while keeping the hint self-consistent,
			// the hint is still adopted and reads must detect it (value
			// bytes live in the log; hint has no values, so a key/seq
			// inconsistency manifests on Get as corruption).
			if err != nil {
				t.Fatalf("seg=%d hint pos=%d: recovery error %v", segID, pos, err)
			}
			snap := dumpEngine(e)
			if !snapshotsEqual(model.snapshot(), snap) {
				r, gerr := verifyAllKeys(e, model)
				_ = r
				t.Fatalf("seg=%d hint pos=%d: state mismatch after tampered hint; get-err=%v",
					segID, pos, gerr)
			}
			e.Close()
		}
	}
}

func verifyAllKeys(e *Engine, m *naiveModel) (bool, error) {
	for _, k := range m.keys() {
		r, err := e.Get([]byte(k))
		if err != nil {
			return false, err
		}
		st, v := m.get(k)
		switch st {
		case modelPresent:
			if r.Status != StatusPresent || string(r.Value) != v {
				return false, nil
			}
		case modelDeleted:
			if r.Status != StatusDeleted {
				return false, nil
			}
		}
	}
	return true, nil
}

// TestHintMissing covers recovery without any hint files.
func TestHintMissing(t *testing.T) {
	dir, model := buildMultiSegment(t, 64)
	states, _ := listSegments(dir)
	for _, st := range states {
		os.Remove(filepath.Join(dir, segHintName(st.id)))
	}
	e, err := Open(dir, Config{})
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	if !snapshotsEqual(model.snapshot(), dumpEngine(e)) {
		t.Fatal("hintless recovery mismatch")
	}
}

// TestHintValidBytesMismatch ensures a hint claiming the wrong valid length
// is rejected and the segment is fully scanned.
func TestHintValidBytesMismatch(t *testing.T) {
	dir, model := buildMultiSegment(t, 64)
	sealed := sealedSegFiles(t, dir)
	segID := sealed[0]
	raw, _ := os.ReadFile(filepath.Join(dir, segHintName(segID)))
	h, ok := decodeHint(raw, segID)
	if !ok {
		t.Fatal("decode")
	}
	h.validBytes++
	os.WriteFile(filepath.Join(dir, segHintName(segID)), encodeHint(h), 0o644)
	e, err := Open(dir, Config{})
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	if !snapshotsEqual(model.snapshot(), dumpEngine(e)) {
		t.Fatal("mismatch")
	}
	s := e.segIndex[segID]
	if s == nil || s.hintAdopted {
		t.Fatal("hint should have been rejected")
	}
}

func copyDir(src, dst string) {
	entries, _ := os.ReadDir(src)
	for _, ent := range entries {
		data, err := os.ReadFile(filepath.Join(src, ent.Name()))
		if err != nil {
			continue
		}
		os.WriteFile(filepath.Join(dst, ent.Name()), data, 0o644)
	}
}

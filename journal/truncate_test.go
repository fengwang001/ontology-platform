package journal

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"ontology/change"
)

func writeJournal(t *testing.T, n int) (string, []byte, []change.Change) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "log")
	w, err := Create(path)
	if err != nil {
		t.Fatal(err)
	}
	var recs []change.Change
	for i := 1; i <= n; i++ {
		c := change.Change{
			Version: uint64(i),
			Op:      change.OpInsert,
			Key:     key(i),
			From:    change.Row{Group: "g", GroupPresent: true, Value: float64(i)},
		}
		if err := w.Append(c); err != nil {
			t.Fatal(err)
		}
		recs = append(recs, c)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return path, data, recs
}

func key(i int) string {
	const digits = "0123456789"
	if i < 10 {
		return "k" + digits[i:i+1]
	}
	return key(i/10) + digits[i%10:i%10+1]
}

// TestEveryTruncationPoint cuts the 200-record journal one byte at a time
// and asserts each position is classified into exactly one of the four
// tail-damage categories, with replay applying exactly the intact prefix.
func TestEveryTruncationPoint(t *testing.T) {
	path, data, recs := writeJournal(t, 200)

	frameLen := 4 + len(change.Encode(recs[0])) + 4
	_ = frameLen
	// Cumulative end offsets of every frame (records vary in length).
	ends := make([]int, len(recs)+1)
	ends[0] = HeaderLen
	for i := range recs {
		ends[i+1] = ends[i] + frameSize(recs[i])
	}

	seen := map[string]bool{}
	// cut removes the last (len-cut) bytes so cut bytes remain.
	for cut := 1; cut < len(data); cut++ {
		if err := os.WriteFile(path, data[:cut], 0o600); err != nil {
			t.Fatal(err)
		}
		got, err := ReadAll(path)
		if err == nil {
			// A cut landing exactly on any frame boundary is intact.
			boundary := cut == HeaderLen
			for _, e := range ends[1:] {
				if e == cut {
					boundary = true
				}
			}
			if !boundary {
				t.Fatalf("cut=%d: nil error for torn tail", cut)
			}
			continue
		}
		class := Classify(err)
		if class == "" {
			t.Fatalf("cut=%d: unclassifiable error %v", cut, err)
		}
		seen[class] = true

		// Replayed records must equal the full-record intact prefix.
		complete := 0
		for complete < len(recs) && ends[complete+1] <= cut {
			complete++
		}
		if len(got) != complete {
			t.Fatalf("cut=%d class=%s: replayed %d, want %d",
				cut, class, len(got), complete)
		}
		if complete > 0 && got[complete-1].Version != recs[complete-1].Version {
			t.Fatalf("cut=%d: last intact record mismatch", cut)
		}
		assertCategoryRange(t, cut, class, ends, complete)
	}
	for _, class := range []string{
		"header-incomplete", "length-prefix-incomplete",
		"body-incomplete", "crc-mismatch",
	} {
		if !seen[class] {
			t.Fatalf("category never observed: %s", class)
		}
	}
}

// assertCategoryRange checks the byte-position interval rules for the
// frame that contains cut: length prefix (4 bytes), body, checksum (4).
func assertCategoryRange(t *testing.T, cut int, class string, ends []int, complete int) {
	t.Helper()
	switch {
	case cut < HeaderLen:
		if class != "header-incomplete" {
			t.Fatalf("cut=%d: want header", cut)
		}
	case cut == HeaderLen:
		// handled as intact empty-tail
	default:
		start := ends[complete]
		off := cut - start
		frameLen := ends[complete+1] - start
		switch {
		case off < 4:
			if class != "length-prefix-incomplete" {
				t.Fatalf("cut=%d off=%d: want length class, got %s", cut, off, class)
			}
		case off < frameLen-4:
			if class != "body-incomplete" {
				t.Fatalf("cut=%d off=%d: want body class, got %s", cut, off, class)
			}
		default:
			if class != "crc-mismatch" {
				t.Fatalf("cut=%d off=%d: want crc class, got %s", cut, off, class)
			}
		}
	}
}

func TestCRCMismatchCorruption(t *testing.T) {
	path, data, _ := writeJournal(t, 1)
	// Flip a body byte: full frame present, checksum must fail.
	data[HeaderLen+4] ^= 0xFF
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := ReadAll(path)
	if !errors.Is(err, ErrCRCMismatch) {
		t.Fatalf("want crc mismatch, got %v", err)
	}
}

func TestBadHeader(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "log")
	if err := os.WriteFile(path, []byte("XXXXXXXXXXXXXXXX"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadAll(path); !errors.Is(err, ErrBadMagic) {
		t.Fatalf("want bad magic, got %v", err)
	}
}

// TestTruncatedReplayEqualsIntactPrefix verifies the torn record never
// becomes visible, directly required by the recovery invariant.
func TestTruncatedReplayEqualsIntactPrefix(t *testing.T) {
	path, data, recs := writeJournal(t, 50)
	// Cut in the middle of frame 11 (offsets account for varying keys).
	start := HeaderLen
	for i := 0; i < 10; i++ {
		start += frameSize(recs[i])
	}
	cut := start + 7
	if err := os.WriteFile(path, data[:cut], 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := ReadAll(path)
	if err == nil {
		t.Fatal("expected tail error")
	}
	if len(got) != 10 {
		t.Fatalf("got %d intact records, want 10", len(got))
	}
}

func frameSize(c change.Change) int { return 4 + len(change.Encode(c)) + 4 }

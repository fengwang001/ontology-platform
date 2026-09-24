package journal

import (
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"ontology/change"
)

func buildLog(t *testing.T, n int) (string, []change.Change, []byte) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "log")
	w, err := Create(path)
	if err != nil {
		t.Fatal(err)
	}
	cs := make([]change.Change, n)
	for i := range cs {
		cs[i] = change.Insert(uint64(i+1), fmt.Sprintf("g%d", i%7), float64(i*3))
		if err := w.Append(cs[i]); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return path, cs, raw
}

// TestRoundTrip covers append/replay, ordering and count in one table.
func TestRoundTrip(t *testing.T) {
	path, cs, _ := buildLog(t, 200)
	cases := []struct {
		name string
		c    change.Change
	}{
		{"insert", change.Insert(1, "g", 3)},
		{"delete", change.Delete(2, "g", 3)},
		{"update", change.Update(3, "n", 1, "o", 2)},
		{"empty-group", change.Insert(4, "", 0)},
	}
	p := filepath.Join(t.TempDir(), "l")
	w, err := Create(p)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range cases {
		if err := w.Append(tc.c); err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	got := []change.Change{}
	n, err := Replay(p, func(c change.Change) { got = append(got, c) })
	if err != nil || n != len(cases) {
		t.Fatalf("replay n=%d err=%v", n, err)
	}
	for i, tc := range cases {
		if got[i] != tc.c {
			t.Errorf("%s roundtrip mismatch: %+v != %+v", tc.name, got[i], tc.c)
		}
	}
	_ = path
	_ = cs
}

// boundaries returns each full-record end offset (payload-relative).
func boundaries(raw []byte) []int {
	out := []int{}
	pos := 0
	data := raw[len(Magic):]
	for pos+4 <= len(data) {
		l := int(binary.BigEndian.Uint32(data[pos:]))
		if pos+4+l+4 > len(data) {
			break
		}
		pos += 4 + l + 4
		out = append(out, pos)
	}
	return out
}

// classifyCut derives the expected error for truncating at payload offset off.
func classifyCut(data []byte, off int) error {
	pos := 0
	for pos < off {
		l := int(binary.BigEndian.Uint32(data[pos:]))
		switch {
		case off <= pos:
			return ErrIncompleteLength
		case off < pos+4:
			return ErrIncompleteLength
		case off < pos+4+l:
			return ErrIncompleteBody
		case off < pos+4+l+4:
			return ErrCRCMismatch
		}
		pos += 4 + l + 4
	}
	return nil
}

// TestBytewiseTruncation classifies every truncation cut 1..len-1 in a loop.
func TestBytewiseTruncation(t *testing.T) {
	path, _, raw := buildLog(t, 200)
	data := raw[len(Magic):]
	ends := boundaries(raw)
	seen := map[error]int{}
	for cut := 1; cut < len(raw); cut++ {
		p := filepath.Join(t.TempDir(), fmt.Sprintf("c%d", cut))
		if err := os.WriteFile(p, raw[:cut], 0o600); err != nil {
			t.Fatal(err)
		}
		n, err := Replay(p, func(change.Change) {})
		var want error
		if cut < len(Magic) {
			want = ErrIncompleteHeader
		} else {
			want = classifyCut(data, cut-len(Magic))
		}
		if !errors.Is(err, want) {
			t.Fatalf("cut=%d err=%v want=%v", cut, err, want)
		}
		seen[want]++
		exp := 0
		for _, e := range ends {
			if e <= cut-len(Magic) {
				exp++
			}
		}
		if n != exp {
			t.Fatalf("cut=%d applied=%d expected=%d", cut, n, exp)
		}
	}
	for _, e := range []error{ErrIncompleteHeader, ErrIncompleteLength, ErrIncompleteBody, ErrCRCMismatch} {
		if seen[e] == 0 {
			t.Fatalf("classification never produced: %v (seen=%v)", e, seen)
		}
	}
}

// TestCorruptBody covers a single flipped payload byte -> CRC mismatch.
func TestCorruptBody(t *testing.T) {
	path, _, raw := buildLog(t, 1)
	bad := append([]byte(nil), raw...)
	bad[len(Magic)+4] ^= 0xFF
	p := filepath.Join(t.TempDir(), "bad")
	if err := os.WriteFile(p, bad, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Replay(p, func(change.Change) {}); !errors.Is(err, ErrCRCMismatch) {
		t.Fatalf("got %v want ErrCRCMismatch", err)
	}
}

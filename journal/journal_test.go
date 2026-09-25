package journal

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"ontology/change"
)

func buildLog(t *testing.T, nRec int) (string, []byte, []change.Change) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "wal")
	j, err := Create(path)
	if err != nil {
		t.Fatal(err)
	}
	recs := make([]change.Change, nRec)
	for i := range recs {
		recs[i] = change.Change{Version: uint64(i + 1), Op: change.Insert, Key: "g", Value: float64(i)}
		if err := j.Append(recs[i]); err != nil {
			t.Fatal(err)
		}
	}
	j.Close()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return path, data, recs
}

func classify(data []byte) (int, error) {
	n, err := ReplayBytes(data)
	return n, err
}

func TestRoundTrip(t *testing.T) {
	path, _, recs := buildLog(t, 200)
	var got []change.Change
	n, err := Replay(path, func(c change.Change) { got = append(got, c) })
	if err != nil || n != len(recs) {
		t.Fatalf("Replay n=%d err=%v", n, err)
	}
	for i := range recs {
		if got[i] != recs[i] {
			t.Fatalf("record %d mismatch: %+v != %+v", i, got[i], recs[i])
		}
	}
}

// TestEveryTruncation walks every cut byte: the intact prefix replays and the
// cut point is always classified into exactly one of the four error kinds.
func TestEveryTruncation(t *testing.T) {
	_, data, recs := buildLog(t, 200)
	frameLen := 4 + recs[0].EncodedLen() + 8
	seen := map[error]int{}
	type cut struct {
		at        int
		err       error
		delivered int
	}
	cuts := make([]cut, 0, len(data)-1)
	for at := 1; at < len(data); at++ {
		n, err := classify(data[:at])
		cuts = append(cuts, cut{at, err, n})
		seen[err]++
	}
	for _, e := range []error{ErrShortHeader, ErrShortLength, ErrShortRecord, ErrCRC} {
		if _, ok := seen[e]; !ok {
			t.Fatalf("error class never observed at any cut: %v; seen=%v", e, seen)
		}
	}
	for _, c := range cuts {
		var want int
		switch {
		case errors.Is(c.err, ErrShortHeader):
			if c.at >= len(header) {
				t.Fatalf("at=%d header error past header", c.at)
			}
		case errors.Is(c.err, ErrShortLength):
			off := c.at - len(header)
			rem := off % frameLen
			if rem < 1 || rem > 3 {
				t.Fatalf("at=%d length cut but rem=%d", c.at, rem)
			}
			want = off / frameLen
		case errors.Is(c.err, ErrShortRecord):
			off := c.at - len(header)
			rem := off % frameLen
			if rem < 4 || rem >= frameLen-8 {
				t.Fatalf("at=%d record cut but rem=%d", c.at, rem)
			}
			want = off / frameLen
		case errors.Is(c.err, ErrCRC):
			off := c.at - len(header)
			rem := off % frameLen
			if rem < frameLen-4 {
				t.Fatalf("at=%d crc cut but rem=%d", c.at, rem)
			}
			want = off / frameLen // committed prefix only; torn frame not delivered
		default:
			t.Fatalf("at=%d unclassified err=%v", c.at, c.err)
		}
		if c.delivered != want {
			t.Fatalf("at=%d delivered=%d want=%d", c.at, c.delivered, want)
		}
	}
}

// TestCorruptWholeFrame flips a byte strictly inside a complete frame; the
// frame boundary is intact so the failure class is CRC mismatch.
func TestCorruptWholeFrame(t *testing.T) {
	_, data, _ := buildLog(t, 200)
	frameLen := 4 + change.Change{Version: 1, Op: change.Insert, Key: "g", Value: 0}.EncodedLen() + 8
	bad := append([]byte(nil), data...)
	bad[len(header)+5] ^= 0xFF
	n, err := classify(bad)
	if !errors.Is(err, ErrCRC) || n != 1 {
		t.Fatalf("whole-frame corruption: n=%d err=%v", n, err)
	}
}

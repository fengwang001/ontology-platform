package replay

import (
	"errors"
	"math/rand"
	"os"
	"path/filepath"
	"testing"

	"ontology/event"
	"ontology/segment"
	"ontology/sparse"
)

func fill(t *testing.T, every, segLimit uint64, n int) *Log {
	t.Helper()
	l, err := Open(t.TempDir(), every, segLimit)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < n; i++ {
		if _, err := l.Append([]byte{byte(i), byte(i >> 8)}); err != nil {
			t.Fatal(err)
		}
	}
	return l
}

func seqs(evs []event.Event) []uint64 {
	out := make([]uint64, len(evs))
	for i, e := range evs {
		out[i] = e.Seq
	}
	return out
}

func wantRange(lo, hi uint64) []uint64 {
	var out []uint64
	for s := lo; s <= hi; s++ {
		out = append(out, s)
	}
	return out
}

func eqSeqs(got, want []uint64) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

func TestLocatePositionsAndBoundaries(t *testing.T) {
	l := fill(t, 4, 16, 48) // 3 segments of 16, anchors every 4
	defer l.Close()
	cases := []struct {
		name        string
		from, to    uint64
		want        []uint64
		wantSkipped uint64
		wantErr     error
	}{
		{"from exactly on anchor", 16, 19, wantRange(16, 19), 0, nil},
		{"from between anchors", 18, 20, wantRange(18, 20), 2, nil},
		{"from below min seq clamps to start", 0, 2, wantRange(0, 2), 0, nil},
		{"to beyond max replays to end", 46, 1000, wantRange(46, 47), 2, nil},
		{"range equals exactly one segment", 16, 31, wantRange(16, 31), 0, nil},
		{"range spans three segments", 3, 40, wantRange(3, 40), 3, nil},
		{"single event", 7, 7, wantRange(7, 7), 3, nil},
		{"from greater than to", 9, 3, nil, 0, ErrRange},
		{"empty range beyond end", 100, 200, nil, 0, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, c, err := l.Replay(tc.from, tc.to)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("err = %v, want %v", err, tc.wantErr)
			}
			if tc.wantErr != nil {
				return
			}
			if !eqSeqs(seqs(got), tc.want) {
				t.Fatalf("seqs = %v, want %v", seqs(got), tc.want)
			}
			if c.Skipped != tc.wantSkipped {
				t.Fatalf("skipped = %d, want %d", c.Skipped, tc.wantSkipped)
			}
			if c.IndexInvalid {
				t.Fatalf("unexpected IndexInvalid")
			}
		})
	}
}

func TestEmptyLogAndEmptyPayload(t *testing.T) {
	l, err := Open(t.TempDir(), 4, 8)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	got, _, err := l.Replay(0, 100)
	if err != nil || len(got) != 0 {
		t.Fatalf("empty log replay = %v, %v", got, err)
	}
	if _, err := l.Append(nil); err != nil {
		t.Fatal(err)
	}
	got, _, err = l.Replay(0, 0)
	if err != nil || len(got) != 1 {
		t.Fatalf("single empty-payload replay = %+v, %v", got, err)
	}
	if len(got[0].Payload) != 0 {
		t.Fatalf("payload = %x, want empty", got[0].Payload)
	}
}

func TestSkippedBoundRandom(t *testing.T) {
	const n, every = 100000, 128
	l := fill(t, every, n, n)
	defer l.Close()
	rng := rand.New(rand.NewSource(1))
	for i := 0; i < 200; i++ {
		from := uint64(rng.Intn(n))
		got, c, err := l.Replay(from, from+9)
		if err != nil {
			t.Fatal(err)
		}
		if !eqSeqs(seqs(got), wantRange(from, from+9)) {
			t.Fatalf("from=%d seqs wrong", from)
		}
		if c.Skipped >= every {
			t.Fatalf("from=%d skipped=%d, want < %d", from, c.Skipped, every)
		}
	}
}

func TestBytesReadBound(t *testing.T) {
	const every = 128
	l := fill(t, every, 100000, 100000)
	defer l.Close()
	recSize := segment.RecordSize(event.EncodedLen(2))
	got, c, err := l.Replay(5000, 5009)
	if err != nil || len(got) != 10 {
		t.Fatalf("got %d events, err %v", len(got), err)
	}
	encodedSum := int64(10 * event.EncodedLen(2))
	bound := encodedSum + int64(every)*recSize
	if c.BytesRead > bound {
		t.Fatalf("BytesRead = %d, bound = %d", c.BytesRead, bound)
	}
}

func TestTamperedIndexFallsBack(t *testing.T) {
	l := fill(t, 4, 16, 32) // segment 0 finalized with index on disk
	l.Close()
	idxPath := filepath.Join(l.dir, "seg-000000.log.idx")
	idx, err := sparse.ReadFile(idxPath)
	if err != nil {
		t.Fatal(err)
	}
	idx.Anchors[2].Offset += 7 // point into the middle of an event
	if err := sparse.WriteFile(idxPath, idx); err != nil {
		t.Fatal(err)
	}
	got, c, err := l.Replay(8, 15) // lo=8 uses anchors[2]
	if err != nil {
		t.Fatal(err)
	}
	if !eqSeqs(seqs(got), wantRange(8, 15)) {
		t.Fatalf("seqs = %v", seqs(got))
	}
	if !c.IndexInvalid {
		t.Fatalf("expected IndexInvalid flag")
	}
	if err := os.Remove(idxPath); err != nil {
		t.Fatal(err)
	}
}

package replay

import (
	"errors"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"testing"

	"ontology/event"
	"ontology/segment"
	"ontology/sparse"
)

const recLen = 4 + 13 + 4 // payload "x": len4 + event13 + crc4

func writeSeg(t *testing.T, dir string, first, n uint64) string {
	t.Helper()
	p := filepath.Join(dir, fmt.Sprintf("%020d.seg", first))
	w, err := segment.Create(p, first)
	if err != nil {
		t.Fatal(err)
	}
	for i := uint64(0); i < n; i++ {
		if err := w.Append(event.Event{Seq: first + i, Payload: []byte("x")}); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return p
}

func collect(t *testing.T, dir string, from, to uint64) ([]uint64, Stats) {
	t.Helper()
	var seqs []uint64
	st, err := Replay(dir, from, to, func(e event.Event) error {
		seqs = append(seqs, e.Seq)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return seqs, st
}

func wantSeqs(from, to uint64) []uint64 {
	var s []uint64
	for i := from; i <= to; i++ {
		s = append(s, i)
	}
	return s
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

func TestLocatePositions(t *testing.T) {
	dir := t.TempDir()
	seg := writeSeg(t, dir, 100, 1000) // seq 100..1099
	if err := sparse.Build(seg, seg+".idx", 10); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name        string
		from, to    uint64
		wantFirst   uint64
		wantSkipped uint64
	}{
		{"from equals anchor", 500, 509, 500, 0},
		{"from between anchors", 507, 509, 507, 7},
		{"from below first anchor", 0, 105, 100, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			seqs, st := collect(t, dir, tc.from, tc.to)
			if !eqSeqs(seqs, wantSeqs(tc.wantFirst, tc.to)) {
				t.Fatalf("seqs head=%v len=%d", seqs[:3], len(seqs))
			}
			if st.Skipped() != tc.wantSkipped {
				t.Fatalf("skipped=%d want %d", st.Skipped(), tc.wantSkipped)
			}
		})
	}
}

func TestLocateBoundAndBytes(t *testing.T) {
	dir := t.TempDir()
	const n = 100000
	const N = 128
	seg := writeSeg(t, dir, 0, n)
	if err := sparse.Build(seg, seg+".idx", N); err != nil {
		t.Fatal(err)
	}
	rng := rand.New(rand.NewSource(1))
	for i := 0; i < 200; i++ {
		from := uint64(rng.Intn(n - 1))
		to := from + uint64(rng.Intn(500))
		seqs, st := collect(t, dir, from, to)
		if st.Skipped() >= N {
			t.Fatalf("from=%d skipped=%d >= N=%d", from, st.Skipped(), N)
		}
		lo := uint64(0)
		if from >= N-1 {
			lo = from - (N - 1)
		}
		maxBytes := uint64(recLen) * (to - lo + 1)
		if st.BytesRead() > maxBytes {
			t.Fatalf("from=%d bytes=%d > bound %d", from, st.BytesRead(), maxBytes)
		}
		if !eqSeqs(seqs, wantSeqs(from, to)) {
			t.Fatalf("from=%d to=%d wrong seqs", from, to)
		}
	}
}

func TestCrossThreeSegments(t *testing.T) {
	dir := t.TempDir()
	var prev segment.Header
	for k := uint64(0); k < 3; k++ {
		p := writeSeg(t, dir, k*100, 100)
		f, _ := os.Open(p)
		h, err := segment.ReadHeader(f)
		f.Close()
		if err != nil {
			t.Fatal(err)
		}
		if k > 0 && prev.FirstSeq+prev.Count != h.FirstSeq {
			t.Fatalf("discontinuity before seg %d", k)
		}
		prev = h
	}
	seqs, _ := collect(t, dir, 50, 249)
	if !eqSeqs(seqs, wantSeqs(50, 249)) {
		t.Fatalf("cross-segment replay wrong, len=%d", len(seqs))
	}
	seqs, _ = collect(t, dir, 0, 99) // 恰好一整段
	if !eqSeqs(seqs, wantSeqs(0, 99)) {
		t.Fatal("exact one segment wrong")
	}
}

func TestEdgeSemantics(t *testing.T) {
	dir := t.TempDir()
	writeSeg(t, dir, 10, 90) // seq 10..99
	cases := []struct {
		name     string
		from, to uint64
		wantN    int
		wantErr  error
	}{
		{"from greater than to", 50, 40, 0, ErrInvalidRange},
		{"to beyond max", 90, 99999, 10, nil},
		{"from below min clamps", 0, 12, 3, nil},
		{"empty range outside", 1000, 2000, 0, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			n := 0
			_, err := Replay(dir, tc.from, tc.to, func(event.Event) error { n++; return nil })
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("err=%v want %v", err, tc.wantErr)
			}
			if err == nil && n != tc.wantN {
				t.Fatalf("got %d events want %d", n, tc.wantN)
			}
		})
	}
	t.Run("empty log", func(t *testing.T) {
		n := 0
		_, err := Replay(t.TempDir(), 0, 100, func(event.Event) error { n++; return nil })
		if err != nil || n != 0 {
			t.Fatalf("empty log: err=%v n=%d", err, n)
		}
	})
}

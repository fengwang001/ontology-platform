package replay

import (
	"bytes"
	"math/rand"
	"os"
	"path/filepath"
	"testing"

	"ontology/event"
	"ontology/segment"
	"ontology/sparse"
)

func buildLog(t *testing.T, dir string, maxEvents, every, total, payloadSize int) {
	t.Helper()
	w, err := segment.NewWriter(dir, maxEvents, every)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < total; i++ {
		if _, err := w.Append(bytes.Repeat([]byte{byte(i)}, payloadSize)); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
}

func seqsOf(evs []event.Event) []uint64 {
	out := make([]uint64, len(evs))
	for i, e := range evs {
		out[i] = e.Seq
	}
	return out
}

func wantRange(from, to uint64) []uint64 {
	var out []uint64
	for i := from; i <= to; i++ {
		out = append(out, i)
	}
	return out
}

func TestLocatePositions(t *testing.T) {
	const n = 4
	cases := []struct {
		name        string
		total       int
		dropFirst   bool // 删掉首段，使 from 小于最小序号
		from, to    uint64
		wantSkipped int
		wantSeqs    []uint64
	}{
		{"on anchor", 20, false, 8, 12, 0, wantRange(8, 12)},
		{"between anchors", 20, false, 9, 13, 1, wantRange(9, 13)},
		{"below min seq clamps", 20, true, 2, 14, 0, wantRange(10, 14)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			buildLog(t, dir, 10, n, tc.total, 15)
			if tc.dropFirst {
				os.Remove(filepath.Join(dir, segment.SegmentName(0)))
			}
			r := New(dir)
			evs, rep, err := r.Replay(tc.from, tc.to)
			if err != nil {
				t.Fatal(err)
			}
			if r.skipped != tc.wantSkipped {
				t.Fatalf("skipped = %d, want %d", r.skipped, tc.wantSkipped)
			}
			got := seqsOf(evs)
			if len(got) != len(tc.wantSeqs) {
				t.Fatalf("seqs = %v, want %v", got, tc.wantSeqs)
			}
			for i := range got {
				if got[i] != tc.wantSeqs[i] {
					t.Fatalf("seqs = %v, want %v", got, tc.wantSeqs)
				}
			}
			if rep.Skipped != r.skipped {
				t.Fatalf("report skipped mismatch")
			}
		})
	}
}

func TestSkipAndByteBounds(t *testing.T) {
	dir := t.TempDir()
	const total, every, payload = 100000, 128, 15
	buildLog(t, dir, total, every, total, payload)
	recSize := int64(4 + 8 + payload + 4)
	rng := rand.New(rand.NewSource(1))
	r := New(dir)
	for i := 0; i < 200; i++ {
		from := uint64(rng.Intn(total - 100))
		to := from + 49
		evs, rep, err := r.Replay(from, to)
		if err != nil {
			t.Fatal(err)
		}
		if len(evs) != 50 || evs[0].Seq != from || evs[49].Seq != to {
			t.Fatalf("from=%d: wrong range", from)
		}
		if r.skipped >= every {
			t.Fatalf("from=%d: skipped %d >= %d, index not used", from, r.skipped, every)
		}
		if r.bytesRead != int64(50+r.skipped)*recSize {
			t.Fatalf("from=%d: bytesRead %d", from, r.bytesRead)
		}
		if rep.BytesRead > (50+every-1)*recSize { // 区间长度 + 一个锚点区间
			t.Fatalf("from=%d: bytesRead %d over bound", from, rep.BytesRead)
		}
	}
}

func TestCrossSegment(t *testing.T) {
	dir := t.TempDir()
	buildLog(t, dir, 4, 2, 12, 15) // 三段：[0,3] [4,7] [8,11]
	r := New(dir)
	cases := []struct {
		name     string
		from, to uint64
		want     []uint64
	}{
		{"whole log", 0, 11, wantRange(0, 11)},
		{"across boundary", 3, 8, wantRange(3, 8)},
		{"exactly one segment", 4, 7, wantRange(4, 7)},
		{"to beyond max", 9, 999, wantRange(9, 11)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			evs, _, err := r.Replay(tc.from, tc.to)
			if err != nil {
				t.Fatal(err)
			}
			got := seqsOf(evs)
			if len(got) != len(tc.want) {
				t.Fatalf("got %v want %v", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("got %v want %v", got, tc.want)
				}
			}
		})
	}
}

func TestTamperedIndexFallback(t *testing.T) {
	dir := t.TempDir()
	buildLog(t, dir, 100, 4, 20, 15)
	segPath := filepath.Join(dir, segment.SegmentName(0))
	idx, err := sparse.ReadFile(segment.IndexPath(segPath))
	if err != nil {
		t.Fatal(err)
	}
	idx.Anchors[2].Offset += 3 // 指向事件中间
	if err := sparse.WriteFile(segment.IndexPath(segPath), idx); err != nil {
		t.Fatal(err)
	}
	r := New(dir)
	evs, rep, err := r.Replay(9, 14) // 命中被篡改的锚点（seq=8）
	if err != nil {
		t.Fatal(err)
	}
	got := seqsOf(evs)
	want := wantRange(9, 14)
	for i := range want {
		if i >= len(got) || got[i] != want[i] {
			t.Fatalf("fallback result wrong: %v", got)
		}
	}
	if !rep.IndexInvalid {
		t.Fatal("expected IndexInvalid=true")
	}
}

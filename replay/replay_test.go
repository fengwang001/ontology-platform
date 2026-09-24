package replay

import (
	"errors"
	"fmt"
	"math/rand"
	"path/filepath"
	"sync"
	"testing"

	"ontology/event"
	"ontology/segment"
	"ontology/sparse"
)

func buildLog(t *testing.T, dir string, interval uint64, ranges ...[2]uint64) {
	t.Helper()
	for _, rg := range ranges {
		first, last := rg[0], rg[1]
		w, err := segment.Create(dir, first)
		if err != nil {
			t.Fatalf("create: %v", err)
		}
		for s := first; s <= last; s++ {
			if err := w.Append([]byte(fmt.Sprintf("payload-%d", s))); err != nil {
				t.Fatalf("append: %v", err)
			}
		}
		if err := w.Close(); err != nil {
			t.Fatalf("close: %v", err)
		}
		p := filepath.Join(dir, segment.Name(first))
		idx, err := sparse.Build(p, interval)
		if err != nil {
			t.Fatalf("index: %v", err)
		}
		if err := idx.Save(sparse.IndexPath(p)); err != nil {
			t.Fatalf("save index: %v", err)
		}
	}
}

func assertContiguous(t *testing.T, evs []event.Event, wantFirst, wantLast uint64) {
	t.Helper()
	if uint64(len(evs)) != wantLast-wantFirst+1 {
		t.Fatalf("count=%d, want %d", len(evs), wantLast-wantFirst+1)
	}
	for i, e := range evs {
		if e.Seq != wantFirst+uint64(i) {
			t.Fatalf("event %d seq=%d, want %d", i, e.Seq, wantFirst+uint64(i))
		}
	}
}

func TestLocatePositions(t *testing.T) {
	dir := t.TempDir()
	buildLog(t, dir, 128, [2]uint64{50, 549})
	cases := []struct {
		name      string
		from      uint64
		wantFirst uint64
		wantSkip  int
	}{
		{"equal to anchor (256)", 256, 256, 0},
		{"between anchors (300)", 300, 300, 44},
		{"below first anchor", 10, 50, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := New(dir, 128)
			evs, rep, err := r.Replay(tc.from, tc.from)
			if err != nil {
				t.Fatalf("replay: %v", err)
			}
			if len(evs) != 1 || evs[0].Seq != tc.wantFirst {
				t.Fatalf("got %+v, want single seq %d", evs, tc.wantFirst)
			}
			if rep.Skipped != tc.wantSkip {
				t.Fatalf("skipped=%d, want %d", rep.Skipped, tc.wantSkip)
			}
		})
	}
}

func TestSkipBound(t *testing.T) {
	dir := t.TempDir()
	const n = 100000
	buildLog(t, dir, 128, [2]uint64{0, n - 1})
	rng := rand.New(rand.NewSource(1))
	r := New(dir, 128)
	for i := 0; i < 200; i++ {
		from := uint64(rng.Intn(n))
		evs, rep, err := r.Replay(from, from+10)
		if err != nil {
			t.Fatalf("replay %d: %v", from, err)
		}
		if rep.Skipped >= 128 {
			t.Fatalf("from=%d skipped=%d >= interval 128 (index unused?)", from, rep.Skipped)
		}
		assertContiguous(t, evs, from, from+10)
	}
}

func TestBytesReadBound(t *testing.T) {
	dir := t.TempDir()
	const total = 5000
	const interval = uint64(128)
	const payloadLen = 8
	w, err := segment.Create(dir, 0)
	if err != nil {
		t.Fatal(err)
	}
	for s := uint64(0); s < total; s++ {
		if err := w.Append([]byte(fmt.Sprintf("%08d", s))); err != nil {
			t.Fatal(err)
		}
	}
	w.Close()
	p := filepath.Join(dir, segment.Name(0))
	idx, err := sparse.Build(p, interval)
	if err != nil {
		t.Fatal(err)
	}
	if err := idx.Save(sparse.IndexPath(p)); err != nil {
		t.Fatal(err)
	}
	rec := event.EncodedSize(payloadLen)
	rng := rand.New(rand.NewSource(7))
	r := New(dir, interval)
	for i := 0; i < 50; i++ {
		from := uint64(rng.Intn(total - 50))
		to := from + uint64(rng.Intn(50))
		_, rep, err := r.Replay(from, to)
		if err != nil {
			t.Fatalf("replay: %v", err)
		}
		bound := int(to-from+1)*rec + int(interval)*rec
		if rep.BytesRead > bound {
			t.Fatalf("read %d bytes, bound %d (from=%d to=%d)", rep.BytesRead, bound, from, to)
		}
	}
}

func TestBoundaries(t *testing.T) {
	dir := t.TempDir()
	buildLog(t, dir, 4,
		[2]uint64{0, 9},
		[2]uint64{10, 19},
		[2]uint64{20, 29},
	)
	cases := []struct {
		name      string
		dir       string
		from, to  uint64
		wantFirst uint64
		wantLast  uint64
		wantErr   error
	}{
		{"empty log", t.TempDir(), 0, 100, 0, 0, nil},
		{"single event", dir, 15, 15, 15, 15, nil},
		{"from greater than to", dir, 20, 19, 0, 0, ErrInvalidRange},
		{"from below min", dir, 0, 2, 0, 2, nil},
		{"to above max", dir, 27, 1000, 27, 29, nil},
		{"from above max", dir, 1000, 1001, 0, 0, nil},
		{"exactly one segment", dir, 10, 19, 10, 19, nil},
		{"across three segments", dir, 5, 25, 5, 25, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := New(tc.dir, 4)
			evs, _, err := r.Replay(tc.from, tc.to)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("err=%v, want %v", err, tc.wantErr)
			}
			if tc.wantErr != nil {
				return
			}
			if tc.wantFirst == 0 && tc.wantLast == 0 {
				if len(evs) != 0 {
					t.Fatalf("want empty, got %d events", len(evs))
				}
				return
			}
			assertContiguous(t, evs, tc.wantFirst, tc.wantLast)
		})
	}
}

func TestEmptyPayload(t *testing.T) {
	dir := t.TempDir()
	w, err := segment.Create(dir, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := w.Append(nil); err != nil {
		t.Fatal(err)
	}
	w.Close()
	p := filepath.Join(dir, segment.Name(0))
	idx, err := sparse.Build(p, 4)
	if err != nil {
		t.Fatal(err)
	}
	idx.Save(sparse.IndexPath(p))
	r := New(dir, 4)
	evs, _, err := r.Replay(0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(evs) != 1 || len(evs[0].Payload) != 0 {
		t.Fatalf("got %+v", evs)
	}
}

func TestTamperedIndexFallback(t *testing.T) {
	dir := t.TempDir()
	buildLog(t, dir, 16, [2]uint64{0, 999})
	p := filepath.Join(dir, segment.Name(0))
	idx, err := sparse.Load(sparse.IndexPath(p))
	if err != nil {
		t.Fatal(err)
	}
	target := idx.Anchors[10] // anchor at seq 160
	idx.Anchors[10].Offset = target.Offset + 5
	if err := idx.Save(sparse.IndexPath(p)); err != nil {
		t.Fatal(err)
	}
	r := New(dir, 16)
	evs, rep, err := r.Replay(165, 170)
	if err != nil {
		t.Fatal(err)
	}
	if !rep.IndexInvalid {
		t.Fatal("expected report to flag the index as invalid")
	}
	assertContiguous(t, evs, 165, 170)
}

func TestConcurrentAppendAndReplay(t *testing.T) {
	dir := t.TempDir()
	w, err := segment.Create(dir, 0)
	if err != nil {
		t.Fatal(err)
	}
	const total = 2000
	var writerDone sync.WaitGroup
	writerDone.Add(1)
	go func() {
		defer writerDone.Done()
		defer w.Close()
		for s := uint64(0); s < total; s++ {
			if err := w.Append([]byte(fmt.Sprintf("p-%d", s))); err != nil {
				t.Errorf("append: %v", err)
				return
			}
		}
	}()
	for i := 0; i < 50; i++ {
		r := New(dir, 128)
		evs, _, err := r.Replay(0, total)
		if err != nil {
			t.Fatalf("replay: %v", err)
		}
		if len(evs) == 0 {
			continue
		}
		for j, e := range evs {
			if e.Seq != uint64(j) {
				t.Fatalf("non-prefix or gap at %d: seq=%d (len=%d)", j, e.Seq, len(evs))
			}
		}
	}
	writerDone.Wait()
}

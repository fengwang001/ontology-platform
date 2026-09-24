package importer

import (
	"bytes"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"ontology/batch"
	"ontology/progress"
	"ontology/store"
)

func mkBatch(t *testing.T, id string, n int) *batch.Batch {
	t.Helper()
	keys := make([]string, n)
	for i := range keys {
		keys[i] = fmt.Sprintf("%s-k%06d", id, i)
	}
	b, err := batch.New(id, keys)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestWriteFailurePositions(t *testing.T) {
	b := mkBatch(t, "b", 100)
	ref := store.New()
	if err := New(ref, t.TempDir(), 10).Run(b); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		k            int64
		wantFrontier int
	}{
		{1, 0}, {50, 40}, {100, 90},
	}
	for _, c := range cases {
		t.Run(fmt.Sprintf("k=%d", c.k), func(t *testing.T) {
			st := store.New()
			st.FailAt(c.k)
			dir := t.TempDir()
			if err := New(st, dir, 10).Run(b); err == nil {
				t.Fatal("expected injected failure")
			}
			p, _ := progress.Load(dir, "b")
			if p.Frontier() != c.wantFrontier {
				t.Fatalf("progress frontier=%d, want %d", p.Frontier(), c.wantFrontier)
			}
			im2 := New(st, dir, 10)
			if err := im2.Run(b); err != nil {
				t.Fatal(err)
			}
			if im2.ResumeRewrites() > 10 {
				t.Fatalf("resume rewrites=%d > N=10", im2.ResumeRewrites())
			}
			if !bytes.Equal(st.Bytes(), ref.Bytes()) {
				t.Fatal("final store differs from failure-free run")
			}
		})
	}
}

func TestResumeLargeBatch(t *testing.T) {
	b := mkBatch(t, "bulk", 100000)
	st := store.New()
	st.FailAt(70000)
	dir := t.TempDir()
	if err := New(st, dir, 1000).Run(b); err == nil {
		t.Fatal("expected injected failure at 70000")
	}
	im2 := New(st, dir, 1000)
	if err := im2.Run(b); err != nil {
		t.Fatal(err)
	}
	if im2.ResumeRewrites() > 1000 {
		t.Fatalf("resume rewrites=%d > N=1000", im2.ResumeRewrites())
	}
	if im2.PeakInflight() > 1000 {
		t.Fatalf("peak inflight=%d > limit 1000", im2.PeakInflight())
	}
	if st.Len() != 100000 {
		t.Fatalf("store len=%d", st.Len())
	}
}

func TestEdgeCases(t *testing.T) {
	t.Run("validation", func(t *testing.T) {
		if _, err := batch.New("", nil); err == nil {
			t.Fatal("empty batch ID accepted")
		}
		_, err := batch.New("b", []string{"a", "x", "a"})
		var de *batch.DupError
		if !errors.As(err, &de) || de.Key != "a" || de.First != 0 || de.Second != 2 {
			t.Fatalf("dup error=%v", err)
		}
		if _, err := batch.New("b", []string{""}); err != nil {
			t.Fatalf("empty key rejected: %v", err)
		}
	})
	cases := []struct {
		name string
		n    int
	}{
		{"zero records", 0}, {"single record", 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			st := store.New()
			dir := t.TempDir()
			b := mkBatch(t, "b", c.n)
			if err := New(st, dir, 10).Run(b); err != nil {
				t.Fatal(err)
			}
			if !progress.Committed(dir, "b") {
				t.Fatal("not committed")
			}
			before := st.Bytes()
			err := New(st, dir, 10).Run(b)
			if !errors.Is(err, ErrAlreadyCommitted) {
				t.Fatalf("re-import err=%v, want ErrAlreadyCommitted", err)
			}
			if !bytes.Equal(before, st.Bytes()) {
				t.Fatal("store changed after repeat import")
			}
		})
	}
}

func TestProgressAheadOfStore(t *testing.T) {
	b := mkBatch(t, "skew", 1000)
	st := store.New()
	st.FailAt(301) // 300 records actually written
	dir := t.TempDir()
	if err := New(st, dir, 100).Run(b); err == nil {
		t.Fatal("expected injected failure")
	}
	// Craft a lying progress file claiming 500 records.
	if err := progress.Save(dir, &progress.Progress{BatchID: "skew", Total: 1000, Intervals: [][2]int{{0, 500}}}); err != nil {
		t.Fatal(err)
	}
	im := New(st, dir, 100)
	if err := im.Run(b); err != nil {
		t.Fatal(err)
	}
	if im.ResumeRewrites() != 0 {
		t.Fatalf("rewrites=%d, want 0 (resume must start at store frontier 300)", im.ResumeRewrites())
	}
	if st.Len() != 1000 {
		t.Fatalf("store len=%d, want 1000", st.Len())
	}
}

func TestConcurrency(t *testing.T) {
	dir := t.TempDir()
	st := store.New()
	b := mkBatch(t, "b1", 100)
	rel, err := progress.Acquire(dir, "b1", "other", time.Now(), time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if err := New(st, dir, 10).Run(b); !errors.Is(err, ErrBusy) {
		t.Fatalf("active holder: err=%v, want ErrBusy", err)
	}
	rel()
	if _, err := progress.Acquire(dir, "b2", "dead", time.Now().Add(-2*time.Hour), time.Hour); err != nil {
		t.Fatal(err)
	}
	if err := New(st, dir, 10).Run(mkBatch(t, "b2", 100)); err != nil {
		t.Fatalf("expired holder not taken over: %v", err)
	}
	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i, id := range []string{"c1", "c2"} {
		wg.Add(1)
		go func(i int, id string) {
			defer wg.Done()
			errs[i] = New(st, dir, 10).Run(mkBatch(t, id, 500))
		}(i, id)
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("parallel batch %d: %v", i, err)
		}
	}
}

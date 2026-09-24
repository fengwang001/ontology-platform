package importer

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"ontology/batch"
	"ontology/progress"
	"ontology/store"
)

func makeBatch(id string, n int) *batch.Batch {
	b := &batch.Batch{ID: id, Records: make([]batch.Record, n)}
	for i := range b.Records {
		b.Records[i] = batch.Record{Key: fmt.Sprintf("k%06d", i), Value: []byte(fmt.Sprintf("v%d", i))}
	}
	return b
}

func reference(t *testing.T, b *batch.Batch, n int) []byte {
	st := store.New()
	if _, err := New(st, t.TempDir(), n).Import(b); err != nil {
		t.Fatal(err)
	}
	return st.Snapshot()
}

func TestImporter(t *testing.T) {
	t.Run("write failure positions", func(t *testing.T) {
		for _, k := range []int{1, 5, 10} { // 首条 / 中间 / 末条，n=10, N=3
			b := makeBatch("f", 10)
			want := reference(t, b, 3)
			st, dir := store.New(), t.TempDir()
			st.FailOnPut(k)
			if _, err := New(st, dir, 3).Import(b); !errors.Is(err, store.ErrInjected) {
				t.Fatalf("k=%d: want ErrInjected, got %v", k, err)
			}
			pf, _, _ := progress.Open(filepath.Join(dir, "f.progress"), "f")
			if stop, ws := pf.Contiguous(), (k-1)/3*3; stop != ws {
				t.Fatalf("k=%d: progress stop=%d, want %d", k, stop, ws)
			}
			im2 := New(st, dir, 3)
			res, err := im2.Import(b)
			if err != nil {
				t.Fatalf("k=%d resume: %v", k, err)
			}
			if !bytes.Equal(st.Snapshot(), want) {
				t.Fatalf("k=%d: final snapshot differs from no-failure run", k)
			}
			if im2.Rewrites() > 3 {
				t.Fatalf("k=%d: rewrites %d > N", k, im2.Rewrites())
			}
			t.Logf("k=%d stop=%d rewrites=%d final=byte-identical", k, pf.Contiguous(), res.Rewritten)
		}
	})

	t.Run("interrupt at 70000 of 100000", func(t *testing.T) {
		b := makeBatch("big", 100000)
		want := reference(t, b, 1000)
		st, dir := store.New(), t.TempDir()
		st.FailOnPut(70000)
		New(st, dir, 1000).Import(b)
		im2 := New(st, dir, 1000)
		if _, err := im2.Import(b); err != nil {
			t.Fatal(err)
		}
		if im2.Rewrites() > 1000 || im2.Peak() > 1000 {
			t.Fatalf("rewrites=%d peak=%d exceed N=1000", im2.Rewrites(), im2.Peak())
		}
		if !bytes.Equal(st.Snapshot(), want) {
			t.Fatal("final snapshot differs")
		}
		t.Logf("rewrites=%d peak=%d", im2.Rewrites(), im2.Peak())
	})

	t.Run("reimport is byte-identical and reports done", func(t *testing.T) {
		b := makeBatch("r", 50)
		st, dir := store.New(), t.TempDir()
		New(st, dir, 4).Import(b)
		snap := st.Snapshot()
		res, err := New(st, dir, 4).Import(b)
		if err != nil || !res.AlreadyDone {
			t.Fatalf("res=%+v err=%v", res, err)
		}
		if !bytes.Equal(st.Snapshot(), snap) {
			t.Fatal("snapshot changed after reimport")
		}
	})

	t.Run("boundaries", func(t *testing.T) {
		st, dir := store.New(), t.TempDir()
		if _, err := New(st, dir, 2).Import(&batch.Batch{ID: "z"}); err != nil {
			t.Fatalf("zero records: %v", err)
		}
		if res, _ := New(st, dir, 2).Import(&batch.Batch{ID: "z"}); !res.AlreadyDone {
			t.Fatal("zero-record batch not marked done")
		}
		if _, err := New(store.New(), t.TempDir(), 2).Import(makeBatch("one", 1)); err != nil {
			t.Fatalf("single record: %v", err)
		}
		dup := &batch.Batch{ID: "d", Records: []batch.Record{{Key: "x"}, {Key: "y"}, {Key: "x"}}}
		var derr *batch.DupKeyError
		if _, err := New(store.New(), t.TempDir(), 2).Import(dup); !errors.As(err, &derr) ||
			derr.Key != "x" || derr.First != 0 || derr.Second != 2 {
			t.Fatalf("dup key: %v", err)
		}
		emptyKey := &batch.Batch{ID: "e", Records: []batch.Record{{Key: "", Value: []byte("v")}}}
		st2 := store.New()
		if _, err := New(st2, t.TempDir(), 2).Import(emptyKey); err != nil || !st2.Has("") {
			t.Fatalf("empty key: %v", err)
		}
		noID := &batch.Batch{Records: []batch.Record{{Key: "a"}}}
		_, err := New(store.New(), t.TempDir(), 2).Import(noID)
		if !errors.Is(err, batch.ErrEmptyBatchID) {
			t.Fatalf("empty id: %v", err)
		}
	})

	t.Run("lock held then expired", func(t *testing.T) {
		dir := t.TempDir()
		now := time.Unix(1000, 0)
		im1 := New(store.New(), dir, 2)
		im1.Now = func() time.Time { return now }
		im1.LockTTL = 10 * time.Second
		if err := im1.acquire("L"); err != nil {
			t.Fatal(err)
		}
		im2 := New(store.New(), dir, 2)
		im2.Now = func() time.Time { return now.Add(5 * time.Second) }
		if _, err := im2.Import(makeBatch("L", 2)); !errors.Is(err, ErrInProgress) {
			t.Fatalf("active holder: %v", err)
		}
		im2.Now = func() time.Time { return now.Add(11 * time.Second) }
		if _, err := im2.Import(makeBatch("L", 2)); err != nil {
			t.Fatalf("expired holder should be taken over: %v", err)
		}
	})

	t.Run("progress ahead of store: store wins", func(t *testing.T) {
		b := makeBatch("skew", 100000)
		want := reference(t, b, 1000)
		dir := t.TempDir()
		st1 := store.New()
		st1.FailOnPut(50001)
		New(st1, dir, 1000).Import(b) // 进度声称 50000
		st2 := store.New()
		for i := 0; i < 30000; i++ { // 存储只有 30000
			st2.Put(b.Records[i].Key, b.Records[i].Value)
		}
		im := New(st2, dir, 1000)
		if _, err := im.Import(b); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(st2.Snapshot(), want) {
			t.Fatal("importer trusted progress over store; final snapshot differs")
		}
	})

	t.Run("truncated progress resume", func(t *testing.T) {
		b := makeBatch("tr", 10)
		want := reference(t, b, 2)
		st := store.New()
		dir0 := t.TempDir()
		st.FailOnPut(7)
		New(st, dir0, 2).Import(b)
		raw, _ := os.ReadFile(filepath.Join(dir0, "tr.progress"))
		for cut := 1; cut < len(raw); cut++ {
			dir := t.TempDir()
			os.WriteFile(filepath.Join(dir, "tr.progress"), raw[:cut], 0o644)
			if _, err := New(st, dir, 2).Import(b); err != nil {
				t.Fatalf("cut=%d: %v", cut, err)
			}
			if !bytes.Equal(st.Snapshot(), want) {
				t.Fatalf("cut=%d: final snapshot differs", cut)
			}
		}
	})

	t.Run("concurrent different batches", func(t *testing.T) {
		st, dir := store.New(), t.TempDir()
		var wg sync.WaitGroup
		for _, id := range []string{"c1", "c2"} {
			wg.Add(1)
			go func() {
				defer wg.Done()
				if _, err := New(st, dir, 7).Import(makeBatch(id, 500)); err != nil {
					t.Error(err)
				}
			}()
		}
		wg.Wait()
	})
}

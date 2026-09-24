package seq_test

import (
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"ontology/api"
	"ontology/seq"
)

func ckpt(t *testing.T, dir string) int64 {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, "checkpoint"))
	if err != nil {
		t.Fatal(err)
	}
	return int64(binary.LittleEndian.Uint64(b))
}

func TestNewInvalidDir(t *testing.T) {
	for _, dir := range []string{"", "  "} {
		if _, err := seq.New(dir); !errors.Is(err, seq.ErrInvalidDir) {
			t.Fatalf("dir=%q: want ErrInvalidDir, got %v", dir, err)
		}
	}
}

// TestSelfCheck exercises the exported built-in invariant verifier.
func TestSelfCheck(t *testing.T) {
	a, _ := api.New(t.TempDir())
	if err := a.SelfCheck(); err != nil {
		t.Fatalf("SelfCheck: %v", err)
	}
}

// TestEightStepSequence pins NOTES section three step by step.
func TestEightStepSequence(t *testing.T) {
	dir := t.TempDir()
	a, err := seq.New(dir)
	if err != nil {
		t.Fatal(err)
	}
	for i, w := range []struct{ ret, ckpt int64 }{{0, 1}, {1, 2}, {2, 3}, {3, 4}} { // steps 1-4
		if n, err := a.Next(); err != nil || n != w.ret || ckpt(t, dir) != w.ckpt {
			t.Fatalf("step %d: n=%d ckpt=%d err=%v", i+1, n, ckpt(t, dir), err)
		}
	}
	a.SimulateCrash() // step 5: memory discarded, file survives
	if ckpt(t, dir) != 4 {
		t.Fatalf("crash changed checkpoint: %d", ckpt(t, dir))
	}
	if err := a.Recover(); err != nil { // step 6
		t.Fatal(err)
	}
	for i, w := range []struct{ ret, ckpt int64 }{{4, 5}, {5, 6}} { // steps 7-8
		if n, err := a.Next(); err != nil || n != w.ret || ckpt(t, dir) != w.ckpt {
			t.Fatalf("step %d: n=%d ckpt=%d want %d/%d err=%v", i+7, n, ckpt(t, dir), w.ret, w.ckpt, err)
		}
	}
}

func TestPersistFailureLeavesNoTrace(t *testing.T) {
	dir := t.TempDir()
	a, _ := seq.New(dir)
	for i := 0; i < 3; i++ {
		if _, err := a.Next(); err != nil {
			t.Fatal(err)
		}
	}
	a.SetPersistFault(true)
	if n, err := a.Next(); !errors.Is(err, seq.ErrPersistFailed) {
		t.Fatalf("want ErrPersistFailed, got n=%d err=%v", n, err)
	}
	a.SetPersistFault(false)
	if ckpt(t, dir) != 3 {
		t.Fatalf("file changed on failed Next: ckpt=%d", ckpt(t, dir))
	}
	if n, err := a.Next(); err != nil || n != 3 || ckpt(t, dir) != 4 {
		t.Fatalf("retry: n=%d ckpt=%d want 3/4 err=%v", n, ckpt(t, dir), err)
	}
}

func TestCorruptRecover(t *testing.T) {
	dir := t.TempDir()
	a, _ := seq.New(dir)
	for i := 0; i < 3; i++ {
		_, _ = a.Next()
	}
	if err := os.WriteFile(filepath.Join(dir, "checkpoint"), []byte{9, 9}, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := a.Recover(); !errors.Is(err, seq.ErrCorrupt) {
		t.Fatalf("want ErrCorrupt, got %v", err)
	}
	var b [8]byte
	binary.LittleEndian.PutUint64(b[:], 3) // repaired checkpoint
	if err := os.WriteFile(filepath.Join(dir, "checkpoint"), b[:], 0o600); err != nil {
		t.Fatal(err)
	}
	if err := a.Recover(); err != nil {
		t.Fatal(err)
	}
	if n, err := a.Next(); err != nil || n != 3 { // still usable
		t.Fatalf("continue after repair: n=%d err=%v", n, err)
	}
}

func TestConcurrentNext(t *testing.T) {
	for _, tc := range []struct{ gor, total int }{{1, 1}, {4, 100}, {16, 1000}} {
		dir := t.TempDir()
		a, _ := seq.New(dir)
		got := make([]int64, tc.total)
		var wg sync.WaitGroup
		for g := 0; g < tc.gor; g++ {
			wg.Add(1)
			go func(g int) {
				defer wg.Done()
				for i := g; i < tc.total; i += tc.gor {
					v, err := a.Next()
					if err != nil {
						t.Errorf("Next: %v", err)
						return
					}
					got[i] = v
				}
			}(g)
		}
		wg.Wait()
		seen := make(map[int64]bool, tc.total)
		for _, v := range got {
			if seen[v] {
				t.Fatalf("gor=%d total=%d: duplicate %d", tc.gor, tc.total, v)
			}
			seen[v] = true
		}
		if len(seen) != tc.total {
			t.Fatalf("gor=%d total=%d: %d unique, want %d (gap)", tc.gor, tc.total, len(seen), tc.total)
		}
		// `total` distinct values all < persisted total == {0..total-1}.
		if ckpt(t, dir) != int64(tc.total) {
			t.Fatalf("ckpt=%d want %d", ckpt(t, dir), tc.total)
		}
	}
}

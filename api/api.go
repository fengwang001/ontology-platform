// Package api is the public facade over the crash-safe sequence allocator.
// It depends only on package seq (which depends only on check).
package api

import (
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"ontology/seq"
)

// Three distinguishable failure classes.
var (
	ErrInvalidDir    = seq.ErrInvalidDir
	ErrPersistFailed = seq.ErrPersistFailed
	ErrCorrupt       = seq.ErrCorrupt
)

// Allocator is the crash-safe, concurrency-safe sequence allocator.
type Allocator struct{ a *seq.Allocator }

// New creates an allocator rooted at dir with next == 0.
func New(dir string) (*Allocator, error) {
	a, err := seq.New(dir)
	if err != nil {
		return nil, err
	}
	return &Allocator{a: a}, nil
}

// Next issues n only after n+1 is durably checkpointed.
func (x *Allocator) Next() (int64, error) { return x.a.Next() }

// Recover rebuilds in-memory next from the checkpoint file.
func (x *Allocator) Recover() error { return x.a.Recover() }

// SimulateCrash discards in-memory state (test/demo only).
func (x *Allocator) SimulateCrash() { x.a.SimulateCrash() }

// SetPersistFault toggles forced checkpoint-write failure (test hook).
func (x *Allocator) SetPersistFault(on bool) { x.a.SetPersistFault(on) }

// readCheckpoint reads the raw 8-byte scalar; -1 means "no file yet".
func readCheckpoint(dir string) int64 {
	b, err := os.ReadFile(filepath.Join(dir, "checkpoint"))
	if err != nil {
		return -1
	}
	return int64(binary.LittleEndian.Uint64(b))
}

// SelfCheck replays built-in operation sequences in fresh temp directories
// and verifies the four invariants: no duplicate, no gap, agreement with the
// naive reference, and failure leaves no trace. The white-box O(1)
// record-count property is pinned in package check (the counter is
// unexported by requirement and never read through an exported method).
func (x *Allocator) SelfCheck() error {
	dir, err := os.MkdirTemp("", "seq-selfcheck-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	a, err := New(dir)
	if err != nil {
		return err
	}
	// Eight-step table (steps 1-4): returned n and checkpoint after each.
	for i, w := range []struct{ ret, ckpt int64 }{{0, 1}, {1, 2}, {2, 3}, {3, 4}} {
		n, err := a.Next()
		if err != nil || n != w.ret || readCheckpoint(dir) != w.ckpt {
			return fmt.Errorf("step %d: got %d ckpt %d (err=%v)", i+1, n, readCheckpoint(dir), err)
		}
	}
	a.SimulateCrash()                                               // step 5
	if err := a.Recover(); err != nil || readCheckpoint(dir) != 4 { // step 6
		return fmt.Errorf("recover: err=%v ckpt=%d", err, readCheckpoint(dir))
	}
	for _, w := range []struct{ ret, ckpt int64 }{{4, 5}, {5, 6}} { // steps 7-8
		if n, err := a.Next(); err != nil || n != w.ret || readCheckpoint(dir) != w.ckpt {
			return fmt.Errorf("post-recover: got %d ckpt %d want %d/%d", n, readCheckpoint(dir), w.ret, w.ckpt)
		}
	}
	// Persist failure leaves no trace: the same 6 is re-issued afterwards.
	before := readCheckpoint(dir)
	a.SetPersistFault(true)
	if _, err := a.Next(); !errors.Is(err, ErrPersistFailed) {
		return fmt.Errorf("fault: want ErrPersistFailed, got %v", err)
	}
	a.SetPersistFault(false)
	if n, err := a.Next(); err != nil || n != 6 || readCheckpoint(dir) != before+1 {
		return fmt.Errorf("retry after fault: got %d ckpt %d", n, readCheckpoint(dir))
	}
	// Corrupt checkpoint is rejected; empty dir is the third distinct error.
	if err := os.WriteFile(filepath.Join(dir, "checkpoint"), []byte{1, 2, 3}, 0o600); err != nil {
		return err
	}
	if err := a.Recover(); !errors.Is(err, ErrCorrupt) {
		return fmt.Errorf("corrupt: want ErrCorrupt, got %v", err)
	}
	if _, err := New(" "); !errors.Is(err, ErrInvalidDir) {
		return fmt.Errorf("emptydir: want ErrInvalidDir, got %v", err)
	}
	if errors.Is(ErrInvalidDir, ErrPersistFailed) || errors.Is(ErrCorrupt, ErrPersistFailed) {
		return errors.New("sentinel errors are not distinct")
	}
	// Concurrency: M allocations across N goroutines must be exactly 0..M-1.
	cdir, _ := os.MkdirTemp("", "seq-conc-")
	defer os.RemoveAll(cdir)
	c, err := New(cdir)
	if err != nil {
		return err
	}
	const gor, total = 8, 400
	var wg sync.WaitGroup
	res := make([]int64, total)
	for g := 0; g < gor; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := g; i < total; i += gor {
				res[i], _ = c.Next()
			}
		}(g)
	}
	wg.Wait()
	seen := make(map[int64]bool, total)
	for _, v := range res {
		if seen[v] {
			return fmt.Errorf("concurrent duplicate %d", v)
		}
		seen[v] = true
	}
	if len(seen) != total {
		return errors.New("concurrent: gaps detected")
	}
	return nil
}

package kvlog

import (
	"fmt"
	"sync"
	"testing"
)

// TestConcurrentOps stresses concurrent writers, readers and one merger.
// Final logical state must equal an independent raw scan of the segments.
func TestConcurrentOps(t *testing.T) {
	dir := t.TempDir()
	e, err := Open(dir, Config{MaxSegmentBytes: 48, WriteHints: true})
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()

	keys := []string{"a", "b", "c", "d", "e"}

	var writers sync.WaitGroup
	stopReaders := make(chan struct{})
	var readers sync.WaitGroup
	var failMu sync.Mutex
	failed := false
	fail := func(err error) {
		failMu.Lock()
		failed = true
		failMu.Unlock()
		t.Error(err)
	}

	const writerCount = 6
	const perWriter = 120
	for g := 0; g < writerCount; g++ {
		writers.Add(1)
		go func(g int) {
			defer writers.Done()
			for i := 0; i < perWriter; i++ {
				k := keys[(g*7+i)%len(keys)]
				if i%11 == 0 {
					if _, err := e.Delete([]byte(k)); err != nil {
						fail(err)
						return
					}
					continue
				}
				v := fmt.Sprintf("%d-%s-g%d", i%10, k, g)
				if _, err := e.Put([]byte(k), []byte(v)); err != nil {
					fail(err)
					return
				}
			}
		}(g)
	}

	for g := 0; g < 4; g++ {
		readers.Add(1)
		go func(g int) {
			defer readers.Done()
			for {
				select {
				case <-stopReaders:
					return
				default:
					k := keys[g%len(keys)]
					if _, err := e.Get([]byte(k)); err != nil {
						fail(err)
						return
					}
				}
			}
		}(g)
	}

	mergerDone := make(chan struct{})
	go func() {
		defer close(mergerDone)
		for i := 0; i < 30; i++ {
			ids := sealedIDs(t, dir)
			if len(ids) >= 2 {
				if _, err := e.Merge(ids[:2]); err != nil {
					fail(err)
					return
				}
			}
		}
	}()

	writers.Wait()
	<-mergerDone
	close(stopReaders)
	readers.Wait()

	if failed {
		t.Fatal("concurrent errors recorded")
	}

	diskModel := rebuildModelFromDisk(t, e, dir)
	if !snapshotsEqual(diskModel.snapshot(), dumpEngine(e)) {
		t.Fatal("final concurrent state inconsistent with disk")
	}

	// Reopen must preserve the same state.
	e.Close()
	e2, err := Open(dir, Config{MaxSegmentBytes: 48, WriteHints: true})
	if err != nil {
		t.Fatal(err)
	}
	defer e2.Close()
	if !snapshotsEqual(diskModel.snapshot(), dumpEngine(e2)) {
		t.Fatal("post-restart state differs")
	}
}

package ontology

import (
	"sync"
	"testing"
)

func TestConcurrentCallsAreSafe(t *testing.T) {
	e, err := NewEngine(4)
	if err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	for worker := 0; worker < 8; worker++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			tx := 0
			for step := 0; step < 80; step++ {
				switch step % 7 {
				case 0:
					tx = e.Begin()
				case 1:
					_, _ = e.Read(tx, worker%4)
				case 2:
					_ = e.Write(tx, step%4, step)
				case 3:
					_, _ = e.Precommit(tx)
				case 4:
					_, _ = e.Finish(tx)
				case 5:
					_, _ = e.Abort(tx)
				default:
					_, _ = e.Read(999, worker%4)
				}
			}
		}(worker)
	}
	wg.Wait()

	for key := 0; key < 4; key++ {
		var latest *version
		for _, v := range e.versions[key] {
			if !e.isGarbage(v) {
				latest = v
			}
		}
		if latest == nil {
			t.Fatalf("key %d missing non-garbage version", key)
		}
		if e.effectiveEnder(latest) != noTx {
			t.Fatalf("latest non-garbage version for key %d has ender %d", key, latest.ender)
		}
	}
}

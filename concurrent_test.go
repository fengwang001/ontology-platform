package ontology

import (
	"sync"
	"testing"
)

func TestConcurrentOperations(t *testing.T) {
	manager := NewManager()
	if err := manager.Register("root", ""); err != nil {
		t.Fatal(err)
	}
	if err := manager.Register("child", "root"); err != nil {
		t.Fatal(err)
	}
	if err := manager.Lock(1, "root", IX); err != nil {
		t.Fatal(err)
	}

	var waitGroup sync.WaitGroup
	for worker := 0; worker < 12; worker++ {
		waitGroup.Add(1)
		go func(worker int) {
			defer waitGroup.Done()
			transaction := 2 + worker
			for step := 0; step < 50; step++ {
				switch step % 5 {
				case 0:
					_ = manager.Lock(transaction, "root", IS)
				case 1:
					_ = manager.Lock(transaction, "child", IS)
				case 2:
					_, _, _ = manager.Held(transaction, "child")
				case 3:
					_, _ = manager.Holders("root")
				case 4:
					_ = manager.ReleaseAll(transaction)
				}
			}
		}(worker)
	}
	waitGroup.Wait()

	assertManagerInvariants(t, manager)
}

package registry

import (
	"sync"
	"testing"
)

func TestConcurrentOperationsAreSerializable(t *testing.T) {
	r, err := NewReclaimer(0)
	mustDo(t, err, "NewReclaimer")
	mustDo(t, r.PutLayer("layer", 8, 0), "PutLayer")
	mustDo(t, r.PutManifest("manifest", []string{"layer"}, 0), "PutManifest")
	mustDo(t, r.Tag("v1", "manifest", 0), "Tag")

	const workers = 100
	var wg sync.WaitGroup
	errs := make(chan error, workers*2)

	for i := 1; i <= workers; i++ {
		wg.Add(2)
		now := int64(1)

		go func() {
			defer wg.Done()
			errs <- r.PutLayer("layer", 8, now)
		}()

		go func() {
			defer wg.Done()
			_, err := r.GC(now)
			errs <- err
		}()
	}

	wg.Wait()
	close(errs)

	accepted := 0
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent operation failed: %v", err)
		}
		accepted++
	}

	t.Logf("input=%d concurrent idempotent PutLayer and GC operations all at now=1; output=%d accepted; basis=one global mutex makes every accepted call follow some serial order", workers*2, accepted)
	if r.maxNow != 1 {
		t.Fatalf("maxNow = %d, want 1", r.maxNow)
	}
	if !r.Exists("layer") {
		t.Fatal("idempotent concurrent uploads must not delete or replace the layer")
	}
}

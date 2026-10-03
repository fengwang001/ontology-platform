package ontology

import (
	"sync"
	"testing"
)

func TestConcurrentCallsRemainSafe(t *testing.T) {
	scheduler, err := NewBlockScheduler(Config{
		Blocks:              12,
		BaseConcurrency:     4,
		GlobalInflightLimit: 8,
		MaxBlockRequests:    3,
		Timeout:             3,
		FailureBanThreshold: 3,
	})
	if err != nil {
		t.Fatal(err)
	}

	for id := 0; id < 6; id++ {
		have := make([]bool, 12)
		for block := range have {
			have[block] = (block+id)%2 == 0
		}
		if err := scheduler.AddPeer(string(rune('a'+id)), have); err != nil {
			t.Fatal(err)
		}
	}

	var wg sync.WaitGroup
	for worker := 0; worker < 8; worker++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			id := string(rune('a' + worker%6))
			for step := 0; step < 40; step++ {
				now := int64(step)
				if block, ok, err := scheduler.Next(now, id); err == nil && ok {
					_, _ = scheduler.Done(now, id, block, step%3 != 0)
				}
				if step%4 == 0 {
					_, _ = scheduler.Tick(now)
				}
				_ = scheduler.Complete()
			}
		}(worker)
	}
	wg.Wait()
}

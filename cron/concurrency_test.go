package cron

import (
	"sync"
	"testing"
)

func TestConcurrentOperations(t *testing.T) {
	j, err := NewJob("0 * * * *", 0, -1, Allow)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for k := 0; k < 200; k++ {
				now := (k*8 + g + 1) * 60
				r, err := j.Sync(now)
				if err == nil && r.Fired {
					_ = j.Finish(r.TaskID, now+1)
				}
				_ = j.Active()
				_ = j.Skipped()
				_ = j.Replaced()
				_ = j.LastScheduled()
				j.SetSuspend(false)
			}
		}(g)
	}
	wg.Wait()
	// Final serial state: every hour 60..1600? was offered, but Sync
	// calls arrive in nondeterministic order; backwards calls are simply
	// rejected. We only assert internal consistency here.
	if j.LastScheduled() < 0 {
		t.Fatal("inconsistent state")
	}
}

package dynamicforest

import (
	"sync"
	"testing"
)

func TestConcurrentUpdatesAndQueries(t *testing.T) {
	service, err := NewService(16, 400)
	if err != nil {
		t.Fatal(err)
	}

	var wait sync.WaitGroup
	for worker := 0; worker < 16; worker++ {
		wait.Add(1)
		go func(worker int) {
			defer wait.Done()
			for step := 0; step < 100; step++ {
				u := (worker + step) % 16
				v := (worker + step + 1) % 16
				weight := int64((worker*7+step)%21 - 10)
				if id, _, addErr := service.AddEdge(u, v, weight); addErr == nil {
					_, _ = service.SetWeight(id, weight+1)
				}
				_, _ = service.Connected(u, v)
				_ = service.Weight()
				_ = service.Components()
			}
		}(worker)
	}
	wait.Wait()

	edges := service.Components()
	if edges < 1 || edges > 16 || service.Version() < 16 {
		t.Fatalf("unexpected concurrent final state components=%d version=%d", edges, service.Version())
	}
}

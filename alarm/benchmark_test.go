package alarm

import (
	"fmt"
	"io"
	"sync"
	"sync/atomic"
	"testing"
)

func TestConcurrentOperationsAreSerializable(t *testing.T) {
	points := []PointConfig{
		{ID: "E", Priority: Emergency},
		{ID: "H", Priority: High, Conditions: []string{"startup"}},
		{ID: "L", Priority: Low},
	}
	service, err := NewService(testConfig(), points)
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	var wait sync.WaitGroup
	var clock int64
	for worker := 0; worker < 16; worker++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			for step := int64(0); step < 100; step++ {
				at := atomic.AddInt64(&clock, 1)
				pointID := []string{"E", "H", "L"}[step%3]
				_, _ = service.Trigger(Operation{At: at, PointID: pointID, Role: Operator})
				_, _ = service.ActiveAlarms(at + 1)
			}
		}()
	}
	wait.Wait()
}

func benchmarkService(totalPoints int, b *testing.B) {
	configs := make([]PointConfig, totalPoints)
	for index := range configs {
		configs[index] = PointConfig{
			ID:       fmt.Sprintf("P%06d", index),
			Priority: Priority(index % 3),
		}
	}
	b.ResetTimer()
	b.Run("trigger", func(b *testing.B) {
		service := benchService(b, configs)
		if _, err := service.Trigger(Operation{At: 1, PointID: configs[0].ID, Role: Operator}); err != nil {
			b.Fatalf("Trigger() error = %v", err)
		}
		for iteration := 0; iteration < b.N; iteration++ {
			if _, err := service.Trigger(Operation{At: int64(iteration + 2), PointID: configs[0].ID, Role: Operator}); err != nil {
				b.Fatalf("Trigger() error = %v", err)
			}
		}
	})
	b.Run("active list fixed visible count", func(b *testing.B) {
		service := benchService(b, configs)
		for index := 0; index < 10; index++ {
			if _, err := service.Trigger(Operation{At: int64(index + 1), PointID: configs[index].ID, Role: Operator}); err != nil {
				b.Fatalf("Trigger() error = %v", err)
			}
		}
		for iteration := 0; iteration < b.N; iteration++ {
			list, err := service.ActiveAlarms(11)
			if err != nil {
				b.Fatalf("ActiveAlarms() error = %v", err)
			}
			if len(list) != 10 {
				b.Fatalf("active count = %d, want 10", len(list))
			}
		}
	})
}

func benchService(b *testing.B, configs []PointConfig) *Service {
	b.Helper()
	service, err := NewService(Config{
		HighManualDuration: 1000,
		LowManualDuration:  1000,
		ChatterWindow:      1000,
		ChatterCount:       1_000_000,
		ChatterDuration:    1000,
	}, configs)
	if err != nil {
		b.Fatalf("NewService() error = %v", err)
	}
	return service.WithLogger(io.Discard)
}

func BenchmarkService100Points(b *testing.B) {
	benchmarkService(100, b)
}

func BenchmarkService5000Points(b *testing.B) {
	benchmarkService(5000, b)
}

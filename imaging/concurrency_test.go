package imaging

import (
	"sync"
	"testing"
)

func TestConcurrentBookingsSerialize(t *testing.T) {
	system := setupFixture(t, 32)
	const workers = 24
	var wg sync.WaitGroup
	errors := make(chan error, workers)
	for index := 0; index < workers; index++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			id := "concurrent-" + itoaBench(index)
			errors <- system.Book(1, id, "ct", "n", "ct-plain", 95000+index*100)
		}(index)
	}
	wg.Wait()
	close(errors)
	for err := range errors {
		must(t, err)
	}
	for index := 0; index < workers; index++ {
		appt, exists := system.Appointment("concurrent-" + itoaBench(index))
		if !exists || appt.Status != StatusAccepted {
			t.Fatalf("appointment %d missing or not accepted: %+v", index, appt)
		}
	}
}

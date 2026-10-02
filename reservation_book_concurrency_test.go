package ontology

import (
	"sync"
	"testing"
)

func TestConcurrentOperations(t *testing.T) {
	b, err := NewCapacityBook(1000, 4, 5000, 25)
	if err != nil {
		t.Fatal(err)
	}
	for slot := int64(0); slot < 4; slot++ {
		for i := int64(0); i < 100; i++ {
			id := slot*1000 + i
			if err := b.Book(id, i%7, slot, 1+int64(i%3), int(i%3)); err != nil {
				t.Fatal(err)
			}
		}
	}

	var wg sync.WaitGroup
	for slot := int64(0); slot < 4; slot++ {
		wg.Add(1)
		go func(slot int64) {
			defer wg.Done()
			for i := int64(0); i < 100; i++ {
				id := 4000 + slot*1000 + i
				_ = b.Book(id, i%7, slot, 1+int64(i%5), int(i%3))
				_ = b.CurrentOversell()
				_ = b.CurrentLimit()
				_ = b.Window()
			}
		}(slot)
	}

	for slot := int64(0); slot < 4; slot++ {
		wg.Add(1)
		go func(slot int64) {
			defer wg.Done()
			var arrivals []Arrival
			for i := int64(0); i < 100; i++ {
				id := slot*1000 + i
				size := 1 + int64(i%3)
				arrivals = append(arrivals, Arrival{ID: id, Amount: i % (size + 1)})
			}
			_, _ = b.Settle(slot, arrivals)
			_ = b.BumpCount(int64(slot))
		}(slot)
	}

	wg.Wait()

	if b.LastSettled() < 0 {
		t.Fatal("expected at least one successful settlement")
	}
	for _, record := range b.Window() {
		if record.Arrivals > record.Reservations || int64(len(b.Window())) > 4 {
			t.Fatalf("invalid window record: %+v", b.Window())
		}
	}
}

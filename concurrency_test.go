package store

import "sync"
import "testing"

func TestConcurrentWritesAndCatchUp(t *testing.T) {
	records := recoveryTestRecords()
	s := recoveredStoreFromRecords(t, records, 1, 1)

	var wait sync.WaitGroup
	wait.Add(3)
	go func() {
		defer wait.Done()
		for i := 0; i < 200; i++ {
			primary := string(rune('a' + i%6))
			secondary := string(rune('a' + i%4))
			_, _ = s.Put(Row{Primary: primary, Secondary: strPtr(secondary)})
		}
	}()
	go func() {
		defer wait.Done()
		for i := 0; i < 200; i++ {
			primary := string(rune('g' + i%6))
			if i%3 == 0 {
				_, _ = s.Delete(primary)
			} else {
				secondary := string(rune('e' + i%4))
				_, _ = s.Put(Row{Primary: primary, Secondary: strPtr(secondary)})
			}
		}
	}()
	go func() {
		defer wait.Done()
		for i := 0; i < 400; i++ {
			_, _ = s.CatchUp(1 + i%5)
		}
	}()
	wait.Wait()

	for s.Watermark() < s.LastLSN() {
		advanced, err := s.CatchUp(16)
		if err != nil {
			t.Fatalf("final catch-up: %v", err)
		}
		if advanced == s.Watermark() && advanced < s.LastLSN() {
			t.Fatalf("catch-up stopped at %d/%d", advanced, s.LastLSN())
		}
	}
	report, err := s.Check()
	requireNoError(t, err)
	if len(report.Mismatches) != 0 {
		t.Fatalf("concurrent check mismatches: %#v", report.Mismatches)
	}
}

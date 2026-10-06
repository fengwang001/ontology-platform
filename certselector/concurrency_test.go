package certselector

import (
	"errors"
	"sync"
	"testing"
)

func TestConcurrentUpdatesAndSelections(t *testing.T) {
	selector := New()
	var waitGroup sync.WaitGroup

	for i := 0; i < 16; i++ {
		waitGroup.Add(1)
		go func(worker int) {
			defer waitGroup.Done()
			id := "concurrent-" + string(rune('a'+worker))
			err := selector.Add(cert(id, []string{"api.example.com", "*.example.org"}, KeyTypeEC, 0, 100))
			if err != nil {
				t.Errorf("Add(): %v", err)
				return
			}
			if err := selector.SetDefault(id); err != nil {
				t.Errorf("SetDefault(): %v", err)
			}
			selector.RemoveDefault()
			if err := selector.SetDefault(id); err != nil {
				t.Errorf("SetDefault() after clear: %v", err)
			}
		}(i)
	}

	for i := 0; i < 16; i++ {
		waitGroup.Add(1)
		go func() {
			defer waitGroup.Done()
			input := SelectInput{
				Name:     "api.example.com",
				KeyTypes: keyTypes(KeyTypeEC, KeyTypeRSA),
				Now:      10,
			}
			for j := 0; j < 200; j++ {
				if _, err := selector.Select(input); err != nil && !errors.Is(err, ErrNoMatchingCertificate) {
					t.Errorf("Select(): %v", err)
				}
			}
		}()
	}

	waitGroup.Wait()
	selection, err := selector.Select(SelectInput{Name: "api.example.com", KeyTypes: keyTypes(KeyTypeEC), Now: 10})
	if err != nil {
		t.Fatalf("final Select(): %v", err)
	}
	if selection.Source != MatchExact || selection.Certificate.ID == "" {
		t.Fatalf("final selection = %+v, want exact non-empty certificate", selection)
	}
}

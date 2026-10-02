package ontology

import (
	"errors"
	"sync"
	"testing"
)

func TestConcurrentCalls(t *testing.T) {
	w, err := New(64, 32)
	if err != nil {
		t.Fatal(err)
	}

	// Seed a dictionary so Decode has committed indices available.
	mustAppend(t, w, 1, 1, 1, 1, 1, 1, 1, 1, 2, 3)
	seed, err := w.Flush()
	if err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for k := 0; k < 200; k++ {
				switch k % 4 {
				case 0:
					_ = w.Append(uint32(id*7 + k%5))
				case 1:
					if p, err := w.Flush(); err == nil {
						if _, derr := w.Decode(p); derr != nil {
							t.Errorf("decode flushed page: %v", derr)
						}
					} else if !errors.Is(err, ErrEmpty) {
						t.Errorf("unexpected flush err: %v", err)
					}
				case 2:
					if _, err := w.Decode(seed); err != nil {
						t.Errorf("decode seed: %v", err)
					}
				case 3:
					_ = w.Append(uint32(k % 3))
				}
			}
		}(g)
	}
	wg.Wait()

	// Drain whatever a serial-equivalent schedule left buffered.
	for {
		p, err := w.Flush()
		if errors.Is(err, ErrEmpty) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Decode(p); err != nil {
			t.Fatalf("final decode: %v", err)
		}
	}
}

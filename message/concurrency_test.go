package message

import (
	"bytes"
	"fmt"
	"sync"
	"testing"
)

// Distinct goroutines parse and marshal their own messages concurrently;
// run with -race this must stay clean.
func TestConcurrentParseMarshal(t *testing.T) {
	p := newParser(t, Options{})
	var wg sync.WaitGroup
	for g := 0; g < 32; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			input := cat(
				vfield(1, uint64(g)),
				vfield(7, uint64(g*2)),
				bfield(2, []byte(fmt.Sprintf("msg-%d", g))),
				mfield(3, vfield(8, uint64(g))),
			)
			for i := 0; i < 50; i++ {
				m, err := p.Parse(input)
				if err != nil {
					t.Errorf("Parse: %v", err)
					return
				}
				if got := m.Marshal(); !bytes.Equal(got, input) {
					t.Errorf("round trip mismatch in goroutine %d", g)
					return
				}
			}
		}(g)
	}
	wg.Wait()
}

// One parsed message marshaled from many goroutines at once: write-back
// must not mutate the structure.
func TestConcurrentMarshalSameMessage(t *testing.T) {
	p := newParser(t, Options{})
	input := cat(vfield(1, 1), vfield(7, 2), mfield(3, vfield(9, 3)))
	m := mustParse(t, p, input)
	var wg sync.WaitGroup
	for g := 0; g < 16; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 100; i++ {
				if got := m.Marshal(); !bytes.Equal(got, input) {
					t.Errorf("concurrent marshal changed bytes")
					return
				}
			}
		}()
	}
	wg.Wait()
}

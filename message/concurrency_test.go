package message_test

import (
	"bytes"
	"sync"
	"testing"

	"ontology/message"
)

// TestConcurrentParseMarshal runs many goroutines, each parsing and
// marshaling its own distinct message. Run with -race to validate.
func TestConcurrentParseMarshal(t *testing.T) {
	const workers = 32
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			v := uint64(w + 1)
			input := cat(
				vid(1, v),
				vbytes(9, []byte{byte(w)}),
				vmsg(3, cat(vid(1, v), vid(7, v))),
				vbytes(2, []byte("worker")),
				vid(9, v),
			)
			for i := 0; i < 50; i++ {
				m, err := message.Parse(input, message.DefaultLimits())
				if err != nil {
					t.Errorf("worker %d: %v", w, err)
					return
				}
				m.SetID(v + 1000)
				first := m.Marshal()
				second := m.Marshal()
				if !bytes.Equal(first, second) {
					t.Errorf("worker %d: marshal not idempotent", w)
					return
				}
				// Re-parse the edited output: it must round-trip too.
				m2, err := message.Parse(first, message.DefaultLimits())
				if err != nil {
					t.Errorf("worker %d: re-parse: %v", w, err)
					return
				}
				if got := m2.Marshal(); !bytes.Equal(got, first) {
					t.Errorf("worker %d: edited round trip mismatch", w)
					return
				}
			}
		}(w)
	}
	wg.Wait()
}

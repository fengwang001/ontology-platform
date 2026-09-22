package serve

import (
	"bytes"
	"fmt"
	"ontology/source"
	"sync"
	"testing"
)

// TestConcurrentAssemblers runs many goroutines, each assembling and
// writing a different response from its own source. With -race this must
// be clean, and every result must match its own one-shot reference.
func TestConcurrentAssemblers(t *testing.T) {
	const workers = 32
	var wg sync.WaitGroup
	errs := make(chan error, workers)
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			data := make([]byte, 200)
			for i := range data {
				data[i] = byte(w + i)
			}
			src := &source.Flaky{Src: source.Bytes(data), MaxChunk: 3}
			header := fmt.Sprintf("bytes=0-49, 60-99, 150-%d", 100+w)
			a := NewAssembler(Config{Rand: fixedRand()}, src)
			if err := a.Assemble(header); err != nil {
				errs <- fmt.Errorf("worker %d assemble: %w", w, err)
				return
			}
			cw := &chunkWriter{max: 7}
			for a.Written() < a.Total() {
				if _, err := a.WriteTo(cw); err != nil {
					errs <- fmt.Errorf("worker %d write: %w", w, err)
					return
				}
			}
			// Independent reference: same inputs, fresh assembler.
			ref := NewAssembler(Config{Rand: fixedRand()}, &source.Flaky{
				Src: source.Bytes(data), MaxChunk: 3,
			})
			if err := ref.Assemble(header); err != nil {
				errs <- fmt.Errorf("worker %d ref: %w", w, err)
				return
			}
			var want bytes.Buffer
			if _, err := ref.WriteTo(&want); err != nil {
				errs <- fmt.Errorf("worker %d ref write: %w", w, err)
				return
			}
			if !bytes.Equal(cw.buf.Bytes(), want.Bytes()) {
				errs <- fmt.Errorf("worker %d: result mismatch", w)
			}
		}(w)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
}

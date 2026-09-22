package chunked

import (
	"fmt"
	"strings"
	"sync"
	"testing"
)

// buildMessage encodes body as two chunks with an extension and a
// trailer, so concurrent instances exercise the full state machine.
func buildMessage(body string) []byte {
	half := len(body) / 2
	return []byte(fmt.Sprintf("%x;v=1\r\n%s\r\n%x\r\n%s\r\n0\r\nX-T: t\r\n\r\n",
		half, body[:half], len(body)-half, body[half:]))
}

// TestConcurrentDecoders runs many independent decoders in parallel;
// each owns its instance, so no synchronization is required and the
// race detector must stay silent. Bodies must not cross-contaminate.
func TestConcurrentDecoders(t *testing.T) {
	const workers = 64
	var wg sync.WaitGroup
	for g := 0; g < workers; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			body := fmt.Sprintf("worker-%03d:%s", g, strings.Repeat("x", g))
			msg := buildMessage(body)
			d := New(Config{})
			step := g%7 + 1
			for i := 0; i < len(msg); i += step {
				j := i + step
				if j > len(msg) {
					j = len(msg)
				}
				if _, err := d.Write(msg[i:j]); err != nil {
					t.Errorf("worker %d: %v", g, err)
					return
				}
			}
			if !d.Done() {
				t.Errorf("worker %d: not done", g)
				return
			}
			if string(d.Body()) != body {
				t.Errorf("worker %d: body %q", g, d.Body())
			}
			if err := d.Close(); err != nil {
				t.Errorf("worker %d: close: %v", g, err)
			}
		}(g)
	}
	wg.Wait()
}

// TestConcurrentLimitAndSuccess mixes failing and succeeding decoders
// across goroutines to check terminal states stay per-instance.
func TestConcurrentLimitAndSuccess(t *testing.T) {
	var wg sync.WaitGroup
	for g := 0; g < 32; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			d := New(Config{MaxChunk: 8})
			msg := buildMessage(strings.Repeat("y", 4+g%4))
			if _, err := d.Write(msg); err != nil {
				t.Errorf("worker %d: %v", g, err)
			}
			bad := New(Config{MaxChunk: 1})
			if _, err := bad.Write(msg); err == nil {
				t.Errorf("worker %d: oversized decoder accepted message", g)
			}
			if !d.Done() {
				t.Errorf("worker %d: good decoder disturbed", g)
			}
		}(g)
	}
	wg.Wait()
}

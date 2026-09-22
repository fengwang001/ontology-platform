package serve_test

import (
	"io"
	"testing"

	"ontology/serve"
)

// chunkWriter 每次 Write 至多接受 limit 字节，模拟短写。
type chunkWriter struct {
	limit int
	buf   []byte
}

func (w *chunkWriter) Write(p []byte) (int, error) {
	n := len(p)
	if n > w.limit {
		n = w.limit
	}
	w.buf = append(w.buf, p[:n]...)
	return n, nil
}

func drainAll(t *testing.T, a *serve.Assembler) []byte {
	t.Helper()
	w := &chunkWriter{limit: 7}
	for !a.Done() {
		if _, err := a.WriteTo(w); err != nil {
			t.Fatalf("write: %v", err)
		}
	}
	return w.buf
}

var _ io.Writer = (*chunkWriter)(nil)

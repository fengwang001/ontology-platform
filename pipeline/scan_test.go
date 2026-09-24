package pipeline

import (
	"testing"

	"ontology/sink"
)

// With N bytes buffered and a sink that only trickles, the bytes scanned per
// advance must not grow with N: advance touches only the head piece.
func TestScanBytesIndependentOfBufferSize(t *testing.T) {
	cfg := testCfg()
	cfg.MaxChunk = 64
	scan := func(n int) int64 {
		p := mustNew(t, cfg, &sink.ShortWriter{Max: 3})
		for written := 0; written < n; {
			m := 4096
			if n-written < m {
				m = n - written
			}
			if _, err := p.Write(make([]byte, m)); err != nil {
				t.Fatal(err)
			}
			written += m
		}
		for i := 0; i < 5; i++ {
			if _, err := p.Advance(); err != nil {
				t.Fatal(err)
			}
		}
		return p.scanBytes
	}
	small, large := scan(1<<10), scan(1<<20)
	if small != large {
		t.Fatalf("scan grows with N: 1KB=%d 1MB=%d", small, large)
	}
	// One chunk piece is "40;k=v\r\n" + 64 payload + "\r\n" = 75 bytes.
	if bound := int64(5 * 75); small > bound {
		t.Fatalf("scan %d exceeds per-advance piece bound %d", small, bound)
	}
}

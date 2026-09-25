package pipeline

import (
	"errors"
	"testing"

	"ontology/sink"
)

// One pump that advances only k bytes must scan the same number of bytes
// regardless of whether the pending buffer holds 1KB or 1MB.
func TestScanBytesNotLinearInBuffer(t *testing.T) {
	measure := func(total int) int64 {
		cfg := testCfg()
		cfg.MaxBuffer = total + 64
		s := &sink.Scripted{CutAfter: -1, FailAt: -1}
		p, err := New(s, &testClock{}, cfg)
		if err != nil {
			t.Fatal(err)
		}
		for written := 0; written < total; written += 64 {
			if _, werr := p.Write(make([]byte, 64)); werr != nil && !errors.Is(werr, sink.ErrBackpressure) {
				t.Fatal(werr)
			}
		}
		p.mu.Lock()
		p.scanBytes = 0
		p.mu.Unlock()
		s.AddQuota(8)
		_ = p.Pump()
		return p.ScanBytes()
	}

	d1k := measure(1024)
	d1m := measure(1 << 20)
	if d1k <= 0 {
		t.Fatalf("1KB scan delta = %d", d1k)
	}
	if d1k != d1m {
		t.Fatalf("scan per pump grows with buffer: 1KB=%d 1MB=%d", d1k, d1m)
	}
	if d1m*10 >= 1<<20 {
		t.Fatalf("scan %d is linear in the 1MB buffer", d1m)
	}
}

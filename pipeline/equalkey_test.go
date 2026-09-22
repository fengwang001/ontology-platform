package pipeline

import "testing"

// The same key is spread across at least three runs because the budget
// spills frequently. Output must be in global arrival order.
func TestEqualKeyAcrossRuns(t *testing.T) {
	dir := t.TempDir()
	// Value encodes arrival index so we can assert FIFO ordering.
	p, err := Open(Config{Dir: dir, MemoryByte: 1024})
	if err != nil {
		t.Fatal(err)
	}
	const n = 150
	for i := 0; i < n; i++ {
		if _, err := p.Ingest("dup", encUint32(uint32(i))); err != nil {
			t.Fatal(err)
		}
	}
	if err := p.Close(); err != nil {
		t.Fatal(err)
	}
	if p.Stats().Runs < 3 {
		t.Fatalf("runs = %d, need >= 3", p.Stats().Runs)
	}
	out, err := p.ReadOutput()
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != n {
		t.Fatalf("count %d", len(out))
	}
	for i := 0; i < n; i++ {
		if decUint32(out[i].Value) != uint32(i) {
			t.Fatalf("equal-key FIFO broken at %d: got %d", i, decUint32(out[i].Value))
		}
		if out[i].Seq != uint64(i) {
			t.Fatalf("seq gap at %d: %d", i, out[i].Seq)
		}
	}
}

func encUint32(v uint32) []byte {
	return []byte{byte(v >> 24), byte(v >> 16), byte(v >> 8), byte(v)}
}

func decUint32(b []byte) uint32 {
	return uint32(b[0])<<24 | uint32(b[1])<<16 | uint32(b[2])<<8 | uint32(b[3])
}

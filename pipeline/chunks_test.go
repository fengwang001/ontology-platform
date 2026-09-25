package pipeline

import (
	"fmt"
	"testing"

	"ontology/sink"
)

// Same byte stream, arbitrary upstream call splits, fixed clock: chunk
// sequence and final bytes must be identical.
func TestChunkSequenceIndependentOfCallBoundary(t *testing.T) {
	payload := make([]byte, 52)
	for i := range payload {
		payload[i] = byte('A' + i%26)
	}
	cfg := testCfg()
	patterns := [][][]byte{
		{payload},
		{payload[:1], payload[1:51], payload[51:]},
		{payload[:10], payload[10:20], payload[20:30], payload[30:52]},
		{payload[:1], payload[1:2], payload[2:3], payload[3:]},
	}
	var wantSizes string
	var wantBytes []byte
	for idx, parts := range patterns {
		s := &sink.Scripted{Quota: 1 << 30, CutAfter: -1, FailAt: -1}
		p, _ := New(s, &testClock{}, cfg)
		for _, b := range parts {
			if _, err := p.Write(b); err != nil {
				t.Fatal(err)
			}
		}
		if err := p.Close(); err != nil {
			t.Fatal(err)
		}
		sizes := fmt.Sprint(p.ChunkSizes())
		if idx == 0 {
			wantSizes = sizes
			wantBytes = s.Bytes()
		} else if sizes != wantSizes {
			t.Fatalf("pattern %d sizes %s, want %s", idx, sizes, wantSizes)
		}
		mustEqual(t, s.Bytes(), wantBytes, "call-boundary pattern")
	}
}

// Time-window aggregation of sub-min writes.
func TestTimeWindowAggregation(t *testing.T) {
	cfg := testCfg() // Min 8, Max 16, Window 1s
	clk := &testClock{}
	s := &sink.Scripted{Quota: 1 << 30, CutAfter: -1, FailAt: -1}
	p, _ := New(s, clk, cfg)

	if _, err := p.Write([]byte("abc")); err != nil {
		t.Fatal(err)
	}
	if got := p.ChunkSizes(); len(got) != 0 {
		t.Fatalf("sub-min write produced chunk: %v", got)
	}
	// Within the window, more tiny writes aggregate but still emit nothing
	// (below Min, window not elapsed).
	clk.advance(500_000_000)
	if _, err := p.Write([]byte("de")); err != nil {
		t.Fatal(err)
	}
	if err := p.Pump(); err != nil {
		t.Fatal(err)
	}
	if got := p.ChunkSizes(); len(got) != 0 {
		t.Fatalf("within-window writes produced chunk: %v", got)
	}
	// Window elapsed and >= Min: one aggregated chunk is emitted.
	clk.advance(500_000_001)
	if _, err := p.Write([]byte("fgh")); err != nil {
		t.Fatal(err)
	}
	if err := p.Pump(); err != nil {
		t.Fatal(err)
	}
	if got := p.ChunkSizes(); fmt.Sprint(got) != "[8]" {
		t.Fatalf("aggregated sizes = %v, want [8]", got)
	}
	if err := p.Close(); err != nil {
		t.Fatal(err)
	}
	if st := p.Stats(); st.Buffered != 0 || st.Confirmed != 8 {
		t.Fatalf("stats = %+v", st)
	}
}

// A single write larger than MaxChunk is split, including the final
// sub-min remainder on close.
func TestMaxChunkSplit(t *testing.T) {
	cfg := testCfg()
	s := &sink.Scripted{Quota: 1 << 30, CutAfter: -1, FailAt: -1}
	p, _ := New(s, &testClock{}, cfg)
	if _, err := p.Write(make([]byte, 40)); err != nil {
		t.Fatal(err)
	}
	if err := p.Close(); err != nil {
		t.Fatal(err)
	}
	if got := fmt.Sprint(p.ChunkSizes()); got != "[16 16 8]" {
		t.Fatalf("sizes = %s, want [16 16 8]", got)
	}
}

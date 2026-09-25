package pipeline

import (
	"testing"

	"ontology/sink"
)

// Exhaustive short-write sweep: for every accept limit k = 1..len(ref) the
// final byte stream must equal the uninterrupted reference byte for byte.
func TestAllShortWritePoints(t *testing.T) {
	payload := []byte("0123456789abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOP") // 48 bytes
	cfg := testCfg()
	ref := reference(t, cfg, payload, nil)
	for k := 1; k <= len(ref); k++ {
		s := &sink.Scripted{MaxAccept: k, Quota: int64(len(ref) + 8), CutAfter: -1, FailAt: -1}
		p, err := New(s, &testClock{}, cfg)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := p.Write(payload); err != nil {
			t.Fatalf("k=%d write: %v", k, err)
		}
		if err := p.Close(); err != nil {
			t.Fatalf("k=%d close: %v", k, err)
		}
		mustEqual(t, s.Bytes(), ref, "short-write")
		st := p.Stats()
		if st.Accepted != int64(len(payload)) || st.Confirmed != int64(len(payload)) || st.Buffered != 0 {
			t.Fatalf("k=%d stats %+v", k, st)
		}
	}
}

// Backpressure during writes must preserve bytes and order.
func TestBackpressureNoLossOrReorder(t *testing.T) {
	cfg := testCfg()
	s := &sink.Scripted{CutAfter: -1, FailAt: -1}
	clk := &testClock{}
	p, _ := New(s, clk, cfg)
	writes := []string{"aa", "bbbb", "cccccccc", "z", "1234567890"}
	total := 0
	for _, w := range writes {
		n, err := p.Write([]byte(w))
		if n != len(w) {
			t.Fatalf("accepted %d of %d", n, len(w))
		}
		if err != nil && err != sink.ErrBackpressure {
			t.Fatal(err)
		}
		total += len(w)
		assertIdentity(t, p, "backpressure write")
	}
	if got := p.Stats().Buffered; got != int64(total) {
		t.Fatalf("buffered = %d, want %d", got, total)
	}
	s.AddQuota(int64(total + 64))
	if err := drainPump(p); err != nil {
		t.Fatal(err)
	}
	if err := p.Close(); err != nil {
		t.Fatal(err)
	}
	want := reference(t, cfg, concat(writes), nil)
	mustEqual(t, s.Bytes(), want, "backpressure")
}

func concat(parts []string) []byte {
	n := 0
	for _, p := range parts {
		n += len(p)
	}
	out := make([]byte, 0, n)
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}

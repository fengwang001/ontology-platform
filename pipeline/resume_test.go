package pipeline

import (
	"errors"
	"testing"

	"ontology/sink"
)

// runOne writes the parts, closes, and survives one disconnect at cut.
func runOne(t *testing.T, cfg Config, parts [][]byte, cut int, clk *testClock) []byte {
	t.Helper()
	old := &sink.Scripted{Quota: 1 << 30, CutAfter: int64(cut), FailAt: -1}
	p, err := New(old, clk, cfg)
	if err != nil {
		t.Fatal(err)
	}
	for _, b := range parts {
		if _, werr := p.Write(b); werr != nil && !errors.Is(werr, sink.ErrDisconnect) {
			t.Fatalf("write: %v", werr)
		}
	}
	err = p.Close()
	out := append([]byte(nil), old.Bytes()...)
	if errors.Is(err, sink.ErrDisconnect) {
		st := p.Checkpoint()
		if err := st.Validate(); err != nil {
			t.Fatalf("checkpoint invalid at cut=%d: %v", cut, err)
		}
		mem := &sink.Memory{}
		p2, rerr := Restore(st, mem, clk, cfg)
		if rerr != nil {
			t.Fatalf("restore at cut=%d: %v", cut, rerr)
		}
		assertIdentity(t, p2, "restored")
		if cerr := p2.Close(); cerr != nil {
			t.Fatalf("close after restore at cut=%d: %v", cut, cerr)
		}
		out = append(out, mem.Bytes()...)
		assertIdentity(t, p2, "resumed final")
	} else if err != nil {
		t.Fatalf("close at cut=%d: %v", cut, err)
	}
	return out
}

// Exhaustive disconnect sweep over every byte position.
func TestAllDisconnectPositions(t *testing.T) {
	payload := []byte("0123456789abcdefghijklmnopqrstuvwxyz!@#$%^&*()") // 40 bytes
	cfg := testCfg()
	clk := &testClock{}
	ref := reference(t, cfg, payload, nil)
	for cut := 0; cut <= len(ref); cut++ {
		clk := &testClock{t: clk.t}
		got := runOne(t, cfg, [][]byte{payload}, cut, clk)
		mustEqual(t, got, ref, "disconnect resume")
	}
}

// Disconnect mid-stream, then more upstream writes happen on the rebuilt
// pipeline: the result must still match an uninterrupted run with the same
// byte stream and clock.
func TestResumeThenMoreWrites(t *testing.T) {
	cfg := testCfg()
	clk := &testClock{}
	first := []byte("0123456789abcdefghijklmnopqrstuvwxyz0123") // 36
	more := []byte("ABCDEFGHIJKLMNOP")
	ref := reference(t, cfg, append(append([]byte(nil), first...), more...), nil)

	for _, cut := range []int{0, 1, 5, 17, 40} {
		old := &sink.Scripted{Quota: 1 << 30, CutAfter: int64(cut), FailAt: -1}
		p, _ := New(old, clk, cfg)
		_, err := p.Write(first)
		if err == nil {
			continue // cut beyond bytes written by the first write
		}
		if !errors.Is(err, sink.ErrDisconnect) {
			t.Fatal(err)
		}
		st := p.Checkpoint()
		mem := &sink.Memory{}
		p2, rerr := Restore(st, mem, clk, cfg)
		if rerr != nil {
			t.Fatal(rerr)
		}
		if n, werr := p2.Write(more); n != len(more) || werr != nil {
			t.Fatalf("write after resume: n=%d err=%v", n, werr)
		}
		if cerr := p2.Close(); cerr != nil {
			t.Fatal(cerr)
		}
		got := append(append([]byte(nil), old.Bytes()...), mem.Bytes()...)
		mustEqual(t, got, ref, "resume+more")
	}
}

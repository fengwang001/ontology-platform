package collab

import (
	"bytes"
	"sync"
	"testing"
)

// TestConcurrentSerializable drives many clients concurrently and verifies
// the final accumulator value equals the single-threaded sum (Adds commute).
func TestConcurrentSerializable(t *testing.T) {
	s := NewServer()
	if err := s.Create("d", map[string]Kind{"counter": KindAdd}); err != nil {
		t.Fatal(err)
	}
	const clients = 16
	const perClient = 200
	var wg sync.WaitGroup
	errs := make(chan error, clients)
	for c := 0; c < clients; c++ {
		wg.Add(1)
		go func(c int) {
			defer wg.Done()
			client := "c" + itoa(c)
			ops := make([]Op, 0, perClient)
			for i := 0; i < perClient; i++ {
				ops = append(ops, addOp(int64(i+1), "counter", 1, "g"))
			}
			// submit in chunks to respect MaxBatchOps=500
			for start := 0; start < len(ops); start += 400 {
				end := start + 400
				if end > len(ops) {
					end = len(ops)
				}
				if _, err := s.Sync(client, "d", ops[start:end], int64(c+1)); err != nil {
					errs <- err
					return
				}
			}
		}(c)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	snap, err := s.Get("d")
	if err != nil {
		t.Fatal(err)
	}
	if want := int64(clients * perClient); snap.Fields["counter"].Value != want {
		t.Fatalf("counter=%d want %d", snap.Fields["counter"].Value, want)
	}
	for c := 0; c < clients; c++ {
		if p := s.Pending("c"+itoa(c), "d"); p != perClient {
			t.Fatalf("client %d pending=%d", c, p)
		}
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

func TestTextLogger(t *testing.T) {
	var buf bytes.Buffer
	s := NewServer()
	s.SetLogger(NewTextLogger(&buf))
	if err := s.Create("d", basicSchema); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Sync("c", "d", []Op{setOp(1, "title", 9, 0, "g1")}, 1); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{"SYNC", "SET title=9", "OUT seq=1 applied"} {
		if !bytes.Contains(buf.Bytes(), []byte(want)) {
			t.Fatalf("log missing %q:\n%s", want, out)
		}
	}
}

// TestReplayDeterminism runs the identical accepted batch sequence twice on
// two fresh servers and asserts byte-identical verdicts and state.
func TestReplayDeterminism(t *testing.T) {
	run := func() (string, int64) {
		s := NewServer()
		var log bytes.Buffer
		s.SetLogger(NewTextLogger(&log))
		if err := s.Create("d", basicSchema); err != nil {
			t.Fatal(err)
		}
		seq := []Op{setOp(1, "title", 5, 0, "g1"), addOp(2, "count", 3, "g1")}
		s.Sync("c", "d", seq, 1)
		s.Sync("c", "d", seq, 2) // replay
		s.Sync("c", "d", []Op{setOp(3, "title", 6, 1, "g2")}, 3)
		snap, _ := s.Get("d")
		return log.String(), snap.Revision
	}
	l1, r1 := run()
	l2, r2 := run()
	if l1 != l2 || r1 != r2 {
		t.Fatal("replay sequence not deterministic")
	}
}

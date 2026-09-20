package partscan

import (
	"errors"
	"testing"
)

// 语义 4：开头校验。
func TestNoPreamble(t *testing.T) {
	for _, data := range [][]byte{
		[]byte("-b\r\n"),
		[]byte("--nope\r\n"),
		[]byte("--b\r "),
	} {
		s := New("b")
		_, err := s.Feed(append([]byte(nil), data...))
		if !errors.Is(err, ErrNoPreamble) {
			t.Errorf("input %q: err = %v, want ErrNoPreamble", data, err)
		}
		_, err = s.Feed([]byte("--b\r\n"))
		if !errors.Is(err, ErrNoPreamble) {
			t.Errorf("input %q: sticky err = %v", data, err)
		}
		if err := s.Close(); !errors.Is(err, ErrNoPreamble) {
			t.Errorf("input %q: close err = %v", data, err)
		}
	}

	// preamble 跨块后再出现分歧也要报错。
	s := New("boundary")
	if _, err := s.Feed([]byte("--bounda")); err != nil {
		t.Fatalf("prefix-only feed: %v", err)
	}
	if _, err := s.Feed([]byte("X")); !errors.Is(err, ErrNoPreamble) {
		t.Fatalf("divergence across chunks: %v", err)
	}
}

// 语义 5：结束分隔符。
func TestAfterClose(t *testing.T) {
	s := New("b")
	ps, err := s.Feed([]byte("--b\r\nonly\r\n--b--"))
	if err != nil || len(ps) != 1 || string(ps[0]) != "only" {
		t.Fatalf("first feed: %v %q", err, ps)
	}
	if !s.Done() {
		t.Fatal("Done = false")
	}
	if _, err := s.Feed(nil); err != nil {
		t.Fatalf("empty feed after close: %v", err)
	}
	if _, err := s.Feed([]byte("junk")); !errors.Is(err, ErrAfterClose) {
		t.Fatalf("junk feed: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close after done: %v", err)
	}
}

// 语义 6：未结束就 Close。
func TestIncompleteClose(t *testing.T) {
	for _, data := range [][]byte{
		nil,
		[]byte("--b\r\nabc"),
		[]byte("--b\r\nabc\r\n--b"),
		[]byte("--b\r\nabc\r\n--b-"),
	} {
		s := New("b")
		if data != nil {
			if _, err := s.Feed(append([]byte(nil), data...)); err != nil {
				t.Fatalf("feed %q: %v", data, err)
			}
		}
		if err := s.Close(); !errors.Is(err, ErrIncomplete) {
			t.Fatalf("data %q: close err = %v", data, err)
		}
		if err := s.Close(); !errors.Is(err, ErrIncomplete) {
			t.Fatalf("repeat close: %v", err)
		}
		if _, err := s.Feed([]byte("x")); !errors.Is(err, ErrIncomplete) {
			t.Fatalf("feed after close: %v", err)
		}
	}
}

// 语义 7：拷贝隔离。
func TestCopyIsolation(t *testing.T) {
	s := New("b")
	in := []byte("--b\r\nAAAA\r\n--b\r\nBBBB\r\n--b--")
	ps, err := s.Feed(in)
	if err != nil {
		t.Fatal(err)
	}
	for i := range in {
		in[i] = 'Z'
	}
	ps[0][0] = 'Z'
	if string(ps[1]) != "BBBB" {
		t.Fatalf("later part mutated: %q", ps[1])
	}
	fresh, _ := New("b").Feed([]byte("--b\r\nAAAA\r\n--b--"))
	if string(fresh[0]) != "AAAA" {
		t.Fatalf("fresh scan affected by mutation: %q", fresh[0])
	}
}

// 语义 8：暂存有界。
func TestBufferBounded(t *testing.T) {
	s := New("b")
	if _, err := s.Feed([]byte("--b\r\n")); err != nil {
		t.Fatal(err)
	}
	const n = 10000
	for i := 0; i < n; i++ {
		ps, err := s.Feed([]byte("seg\r\n--b\r\n"))
		if err != nil {
			t.Fatalf("iter %d: %v", i, err)
		}
		if s.Pending() != 0 {
			t.Fatalf("iter %d: Pending = %d", i, s.Pending())
		}
		if len(ps) != 1 || string(ps[0]) != "seg" {
			t.Fatalf("iter %d: %q", i, ps)
		}
	}
	if cap(s.buf) > 2*len("seg\r\n--b\r\n") {
		t.Fatalf("buffer cap grew to %d", cap(s.buf))
	}
	_, _ = s.Feed([]byte("xy"))
	if s.Pending() != 2 {
		t.Fatalf("Pending = %d, want 2", s.Pending())
	}
}

package pipeline

import (
	"bytes"
	"errors"
	"testing"

	"ontology/sink"
)

func TestZeroWritesNeverProduceChunks(t *testing.T) {
	mem := &sink.Memory{}
	p, err := New(mem, &testClock{}, testCfg())
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 100; i++ {
		n, err := p.Write(nil)
		if n != 0 || err != nil {
			t.Fatalf("zero write %d: n=%d err=%v", i, n, err)
		}
		if err := p.Pump(); err != nil {
			t.Fatal(err)
		}
	}
	s := p.Stats()
	if s.Produced != 0 || s.Accepted != 0 || s.Buffered != 0 || s.Closed {
		t.Fatalf("state after 100 zero writes: %+v", s)
	}
	if len(mem.Bytes()) != 0 {
		t.Fatalf("downstream saw bytes: %q", mem.Bytes())
	}
	// Real data still works afterwards, and the terminating block appears once.
	if _, err := p.Write([]byte("hello!!")); err != nil {
		t.Fatal(err)
	}
	if err := p.Close(); err != nil {
		t.Fatal(err)
	}
	out := mem.Bytes()
	if bytes.Count(out, []byte("0\r\n\r\n")) != 1 {
		t.Fatalf("terminator count = %d in %q", bytes.Count(out, []byte("0\r\n\r\n")), out)
	}
}

func TestCloseIdempotent(t *testing.T) {
	cases := []struct {
		name    string
		payload []byte
	}{
		{"empty", nil},
		{"data", []byte("0123456789abcdef")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mem := &sink.Memory{}
			p, _ := New(mem, &testClock{}, testCfg())
			if _, err := p.Write(tc.payload); err != nil {
				t.Fatal(err)
			}
			for i := 0; i < 5; i++ {
				if err := p.Close(); err != nil {
					t.Fatalf("close %d: %v", i, err)
				}
			}
			out := mem.Bytes()
			if bytes.Count(out, []byte("0\r\n\r\n")) != 1 ||
				!bytes.HasSuffix(out, []byte("0\r\n\r\n")) {
				t.Fatalf("bad terminator in %q", out)
			}
			if _, err := p.Write([]byte("x")); !errors.Is(err, ErrClosed) {
				t.Fatalf("write after close: %v", err)
			}
		})
	}
}

func TestBasicIdentity(t *testing.T) {
	mem := &sink.Memory{}
	clk := &testClock{}
	p, _ := New(mem, clk, testCfg())
	chunks := [][]byte{[]byte("aaa"), []byte("bbbb"), []byte("cd"), []byte("efgh"), []byte("xy")}
	total := 0
	for _, b := range chunks {
		if _, err := p.Write(b); err != nil {
			t.Fatal(err)
		}
		total += len(b)
		assertIdentity(t, p, "after write")
	}
	s := p.Stats()
	if s.Accepted != int64(total) || s.Buffered != int64(total) {
		t.Fatalf("pre-close stats: %+v", s)
	}
	if err := p.Close(); err != nil {
		t.Fatal(err)
	}
	s = p.Stats()
	if s.Accepted != int64(total) || s.Confirmed != int64(total) || s.Buffered != 0 {
		t.Fatalf("post-close stats: %+v", s)
	}
	assertIdentity(t, p, "final")
}

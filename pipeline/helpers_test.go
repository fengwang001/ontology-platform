package pipeline

import (
	"bytes"
	"errors"
	"testing"
	"time"

	"ontology/sink"
	"ontology/sizeline"
)

type testClock struct{ t time.Time }

func (c *testClock) Now() time.Time          { return c.t }
func (c *testClock) advance(d time.Duration) { c.t = c.t.Add(d) }

func testCfg() Config {
	return Config{
		MinChunk:  8,
		MaxChunk:  16,
		Window:    time.Second,
		MaxBuffer: 1 << 20,
		MaxExtLen: 1 << 10,
	}
}

// drain pumps until no progress can be made. Returns the last error or nil.
func drainPump(p *Pipeline) error {
	for i := 0; i < 1_000_000; i++ {
		err := p.Pump()
		if err == nil {
			return nil
		}
		if !errors.Is(err, sink.ErrBackpressure) {
			return err
		}
	}
	return errors.New("drain did not finish")
}

// reference runs the whole input through a perfect sink and returns bytes.
func reference(t *testing.T, cfg Config, payload []byte, exts []sizeline.Ext) []byte {
	t.Helper()
	cfg.Exts = exts
	mem := &sink.Memory{}
	p, err := New(mem, &testClock{t: time.Unix(1, 0)}, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.Write(payload); err != nil {
		t.Fatal(err)
	}
	if err := p.Close(); err != nil {
		t.Fatal(err)
	}
	return mem.Bytes()
}

func assertIdentity(t *testing.T, p *Pipeline, where string) {
	t.Helper()
	s := p.Stats()
	if s.Accepted != s.Confirmed+s.Buffered {
		t.Fatalf("identity broken at %s: %d != %d + %d",
			where, s.Accepted, s.Confirmed, s.Buffered)
	}
	s2 := p.Stats()
	if s != s2 {
		t.Fatalf("non-repeatable stats: %+v != %+v", s, s2)
	}
}

func mustEqual(t *testing.T, got, want []byte, label string) {
	t.Helper()
	if !bytes.Equal(got, want) {
		t.Fatalf("%s mismatch: got %d bytes, want %d bytes\n got=%q\nwant=%q",
			label, len(got), len(want), got, want)
	}
}

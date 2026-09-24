package pipeline

import (
	"bytes"
	"errors"
	"testing"
	"time"

	"ontology/sink"
	"ontology/sizeline"
)

type fakeClock struct{ t time.Time }

func (c *fakeClock) Now() time.Time { return c.t }

func testCfg() Config {
	return Config{
		MinChunk: 4, MaxChunk: 16, Window: time.Second,
		MaxWriteBytes: 1 << 20, MaxBufferBytes: 4 << 20, MaxExtBytes: 256,
		Exts:  []sizeline.Ext{{Key: "k", Val: "v"}},
		Clock: &fakeClock{},
	}
}

func mustNew(t *testing.T, cfg Config, snk sink.Sink) *Pipeline {
	t.Helper()
	p, err := New(cfg, snk)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func drain(t *testing.T, p *Pipeline) {
	t.Helper()
	for {
		n, err := p.Advance()
		if err != nil {
			t.Fatalf("advance: %v", err)
		}
		if n == 0 {
			return
		}
	}
}

func produce(t *testing.T, cfg Config, input []byte, snk sink.Sink) *Pipeline {
	t.Helper()
	p := mustNew(t, cfg, snk)
	if _, err := p.Write(input); err != nil {
		t.Fatal(err)
	}
	if err := p.Close(); err != nil {
		t.Fatal(err)
	}
	drain(t, p)
	return p
}

func golden(t *testing.T, cfg Config, input []byte) []byte {
	t.Helper()
	rec := &sink.Recorder{}
	produce(t, cfg, input, rec)
	return rec.Buf
}

var testInput = bytes.Repeat([]byte("the quick brown fox jumps over the lazy dog. "), 4)

func TestZeroWritesNeverProduceChunks(t *testing.T) {
	rec := &sink.Recorder{}
	p := mustNew(t, testCfg(), rec)
	for i := 0; i < 100; i++ {
		if n, err := p.Write(nil); n != 0 || err != nil {
			t.Fatalf("zero write %d: %d, %v", i, n, err)
		}
		if n, err := p.Write([]byte{}); n != 0 || err != nil {
			t.Fatalf("empty write %d: %d, %v", i, n, err)
		}
	}
	st := p.Stats()
	if st.Chunks != 0 || st.Accepted != 0 || st.Pending != 0 || st.Closed {
		t.Fatalf("zero writes changed state: %+v", st)
	}
	if _, err := p.Write(testInput); err != nil {
		t.Fatal(err)
	}
	_ = p.Close()
	drain(t, p)
	if !bytes.Equal(rec.Buf, golden(t, testCfg(), testInput)) {
		t.Fatal("stream after 100 zero writes differs from golden")
	}
}

func TestCloseIdempotent(t *testing.T) {
	rec := &sink.Recorder{}
	p := mustNew(t, testCfg(), rec)
	if _, err := p.Write([]byte("hello")); err != nil {
		t.Fatal(err)
	}
	if err := p.Close(); err != nil {
		t.Fatal(err)
	}
	before := p.Stats()
	if err := p.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
	if after := p.Stats(); after != before {
		t.Fatalf("second Close changed state: %+v -> %+v", before, after)
	}
	if _, err := p.Write([]byte("x")); !errors.Is(err, ErrClosed) {
		t.Fatalf("write after close: %v", err)
	}
	drain(t, p)
	if n := bytes.Count(rec.Buf, []byte("0\r\n\r\n")); n != 1 {
		t.Fatalf("end marker appears %d times, want 1", n)
	}
	fresh := &sink.Recorder{}
	q := mustNew(t, testCfg(), fresh)
	_ = q.Close()
	drain(t, q)
	if !bytes.Equal(fresh.Buf, []byte("0\r\n\r\n")) {
		t.Fatalf("close-only stream = %q", fresh.Buf)
	}
}

func TestLimitsRejectWithoutStateChange(t *testing.T) {
	t.Run("write-too-large", func(t *testing.T) {
		cfg := testCfg()
		cfg.MaxWriteBytes = 8
		p := mustNew(t, cfg, &sink.Recorder{})
		before := p.Stats()
		_, err := p.Write(make([]byte, 9))
		if !errors.Is(err, ErrWriteTooLarge) || errors.Is(err, ErrWouldBlock) || errors.Is(err, ErrExtsTooLarge) {
			t.Fatalf("error not distinguishable: %v", err)
		}
		if p.Stats() != before {
			t.Fatal("rejection changed state")
		}
	})
	t.Run("exts-too-large", func(t *testing.T) {
		cfg := testCfg()
		cfg.MaxExtBytes = 2
		_, err := New(cfg, &sink.Recorder{})
		if !errors.Is(err, ErrExtsTooLarge) || errors.Is(err, ErrWouldBlock) || errors.Is(err, ErrWriteTooLarge) {
			t.Fatalf("error not distinguishable: %v", err)
		}
	})
	t.Run("buffer-full-is-backpressure", func(t *testing.T) {
		cfg := testCfg()
		cfg.MaxBufferBytes = 40
		p := mustNew(t, cfg, &sink.Recorder{})
		var err error
		for err == nil {
			_, err = p.Write([]byte("0123456789abcdef"))
		}
		if !errors.Is(err, ErrWouldBlock) || errors.Is(err, ErrWriteTooLarge) || errors.Is(err, ErrExtsTooLarge) {
			t.Fatalf("buffer limit must report backpressure, got %v", err)
		}
		before := p.Stats()
		if _, err2 := p.Write([]byte("0123456789abcdef")); !errors.Is(err2, ErrWouldBlock) {
			t.Fatalf("second write: %v", err2)
		}
		if p.Stats() != before {
			t.Fatal("rejected write changed buffered state")
		}
	})
}

func TestStatsAreReadOnlyAndConsistent(t *testing.T) {
	p := mustNew(t, testCfg(), &sink.ShortWriter{Max: 3})
	if _, err := p.Write(testInput); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Advance(); err != nil {
		t.Fatal(err)
	}
	s1, s2 := p.Stats(), p.Stats()
	if s1 != s2 {
		t.Fatalf("two reads differ: %+v vs %+v", s1, s2)
	}
	if s1.Accepted != s1.Confirmed+s1.Pending {
		t.Fatalf("identity broken: %+v", s1)
	}
	_ = p.Close()
	drain(t, p)
	st := p.Stats()
	if st.Accepted != st.Confirmed+st.Pending || st.Pending != 0 || !st.Closed {
		t.Fatalf("final stats: %+v", st)
	}
}

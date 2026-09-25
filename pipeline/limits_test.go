package pipeline

import (
	"bytes"
	"errors"
	"testing"

	"ontology/sink"
	"ontology/sizeline"
)

func TestLimitErrors(t *testing.T) {
	cases := []struct {
		name string
		cfg  func(Config) Config
		want error
	}{
		{
			"chunk-size",
			func(c Config) Config { c.MinChunk = c.MaxChunk + 1; return c },
			ErrChunkTooLarge,
		},
		{
			"zero-max-chunk",
			func(c Config) Config { c.MaxChunk = 0; return c },
			ErrChunkTooLarge,
		},
		{
			"extension",
			func(c Config) Config {
				c.MaxExtLen = 2
				c.Exts = []sizeline.Ext{{Key: "k", Value: "value-too-long"}}
				return c
			},
			ErrExtTooLong,
		},
		{
			"buffer",
			func(c Config) Config { c.MaxBuffer = 4; return c },
			ErrBackpressure,
		},
		{
			"bad-buffer-config",
			func(c Config) Config { c.MaxBuffer = 0; return c },
			ErrConfig,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := tc.cfg(testCfg())
			var err error
			s := &sink.Scripted{CutAfter: -1, FailAt: -1}
			p, nerr := New(s, &testClock{}, cfg)
			if nerr != nil {
				err = nerr
			} else if tc.want == ErrBackpressure {
				_, err = p.Write([]byte("abcdefgh"))
			}
			if !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
			// Error classes must be mutually distinguishable.
			for _, other := range []error{ErrChunkTooLarge, ErrExtTooLong, ErrBackpressure} {
				if other == tc.want {
					continue
				}
				if errors.Is(err, other) {
					t.Fatalf("err %v also matches %v", err, other)
				}
			}
		})
	}
}

// Buffer overflow rejects without mutating any buffered state, then a retry
// after draining succeeds.
func TestBufferOverflowZeroStateChange(t *testing.T) {
	cfg := testCfg()
	cfg.MaxBuffer = 16
	s := &sink.Scripted{CutAfter: -1, FailAt: -1}
	clk := &testClock{}
	p, _ := New(s, clk, cfg)
	if n, err := p.Write([]byte("12345678")); n != 8 {
		t.Fatalf("seed write n=%d err=%v", n, err)
	}
	before := p.Stats()
	n, err := p.Write([]byte("xxxxxxxxxxxxxxxx"))
	if n != 0 || !errors.Is(err, ErrBackpressure) {
		t.Fatalf("overflow: n=%d err=%v", n, err)
	}
	if got := p.Stats(); got != before {
		t.Fatalf("state changed on reject: %+v != %+v", got, before)
	}
	// Drain and retry: nothing was dropped.
	s.AddQuota(1 << 20)
	if err := p.Close(); err != nil {
		t.Fatal(err)
	}
	out := s.Bytes()
	if !bytes.Contains(out, []byte("12345678")) || bytes.Contains(out, []byte("xxxx")) {
		t.Fatalf("unexpected stream after overflow: %q", out)
	}
}

package main

import (
	"bytes"
	"time"

	"ontology/chunker"
	"ontology/pipeline"
	"ontology/sink"
	"ontology/sizeline"
)

type fakeClock struct{ t time.Time }

func (c *fakeClock) Now() time.Time { return c.t }

func cfg(clock chunker.Clock) pipeline.Config {
	return pipeline.Config{
		MinChunk: 4, MaxChunk: 16, Window: time.Second,
		MaxWriteBytes: 1 << 20, MaxBufferBytes: 4 << 20, MaxExtBytes: 256,
		Exts:  []sizeline.Ext{{Key: "k", Val: "v"}},
		Clock: clock,
	}
}

func drain(p *pipeline.Pipeline) error {
	for {
		n, err := p.Advance()
		if err != nil {
			return err
		}
		if n == 0 {
			return nil
		}
	}
}

var input = bytes.Repeat([]byte("the quick brown fox jumps over the lazy dog. "), 4)

func golden(in []byte) []byte {
	rec := &sink.Recorder{}
	p, _ := pipeline.New(cfg(&fakeClock{}), rec)
	_, _ = p.Write(in)
	_ = p.Close()
	_ = drain(p)
	return rec.Buf
}

func produce(in []byte, split int, snk sink.Sink) ([]int, error) {
	p, err := pipeline.New(cfg(&fakeClock{}), snk)
	if err != nil {
		return nil, err
	}
	for i := 0; i < len(in); i += split {
		end := i + split
		if end > len(in) {
			end = len(in)
		}
		if _, err := p.Write(in[i:end]); err != nil {
			return nil, err
		}
	}
	if err := p.Close(); err != nil {
		return nil, err
	}
	return p.ChunkSizes(), drain(p)
}

func equalInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

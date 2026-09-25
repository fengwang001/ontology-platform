package main

import (
	"bytes"
	"errors"
	"time"

	"ontology/pipeline"
	"ontology/sink"
)

type mclock struct{ t time.Time }

func (m *mclock) Now() time.Time { return m.t }

func demoCfg() pipeline.Config {
	return pipeline.Config{
		MinChunk:  8,
		MaxChunk:  16,
		Window:    time.Second,
		MaxBuffer: 1 << 20,
		MaxExtLen: 1 << 10,
	}
}

var payload = bytes.Repeat([]byte("abcdefghijklmnopqrstuvwxyz0123456789"), 1) // 36 bytes

func reference(cfg pipeline.Config, parts [][]byte) []byte {
	mem := &sink.Memory{}
	p, err := pipeline.New(mem, &mclock{}, cfg)
	if err != nil {
		panic(err)
	}
	for _, b := range parts {
		if _, err := p.Write(b); err != nil {
			panic(err)
		}
	}
	if err := p.Close(); err != nil {
		panic(err)
	}
	return mem.Bytes()
}

func checkZero() bool {
	s := &sink.Scripted{CutAfter: -1, FailAt: -1}
	p, _ := pipeline.New(s, &mclock{}, demoCfg())
	for i := 0; i < 100; i++ {
		if n, err := p.Write(nil); n != 0 || err != nil {
			return false
		}
	}
	st := p.Stats()
	return st.Accepted == 0 && st.Produced == 0 && !st.Closed && len(s.Bytes()) == 0
}

func checkClose() bool {
	mem := &sink.Memory{}
	p, _ := pipeline.New(mem, &mclock{}, demoCfg())
	if _, err := p.Write(payload); err != nil {
		return false
	}
	for i := 0; i < 3; i++ {
		if err := p.Close(); err != nil {
			return false
		}
	}
	b := mem.Bytes()
	return bytes.Count(b, []byte("0\r\n\r\n")) == 1 && bytes.HasSuffix(b, []byte("0\r\n\r\n"))
}

func checkShortWrites() bool {
	ref := reference(demoCfg(), [][]byte{payload})
	for k := 1; k <= len(ref); k++ {
		s := &sink.Scripted{MaxAccept: k, Quota: int64(len(ref) + 8), CutAfter: -1, FailAt: -1}
		p, _ := pipeline.New(s, &mclock{}, demoCfg())
		if _, err := p.Write(payload); err != nil {
			return false
		}
		if err := p.Close(); err != nil {
			return false
		}
		if !bytes.Equal(s.Bytes(), ref) {
			return false
		}
	}
	return true
}

func checkBackpressure() bool {
	s := &sink.Scripted{CutAfter: -1, FailAt: -1}
	clk := &mclock{}
	p, _ := pipeline.New(s, clk, demoCfg())
	for _, b := range []string{"ABC", "DEF"} {
		if n, err := p.Write([]byte(b)); n != 3 {
			return false
		} else if err != nil && !errors.Is(err, sink.ErrBackpressure) {
			return false
		}
	}
	if p.Stats().Buffered != 6 {
		return false
	}
	s.AddQuota(1 << 20)
	if err := p.Close(); err != nil {
		return false
	}
	want := reference(demoCfg(), [][]byte{[]byte("ABC"), []byte("DEF")})
	return bytes.Equal(s.Bytes(), want)
}

func checkResume() bool {
	cfg := demoCfg()
	ref := reference(cfg, [][]byte{payload})
	for d := 0; d <= len(ref); d++ {
		clk := &mclock{}
		old := &sink.Scripted{Quota: int64(len(ref) + 8), CutAfter: int64(d)}
		p, _ := pipeline.New(old, clk, cfg)
		if _, err := p.Write(payload); err != nil && !errors.Is(err, sink.ErrDisconnect) {
			return false
		}
		err := p.Close()
		if err != nil && !errors.Is(err, sink.ErrDisconnect) {
			return false
		}
		got := append([]byte(nil), old.Bytes()...)
		if errors.Is(err, sink.ErrDisconnect) {
			st := p.Checkpoint()
			mem := &sink.Memory{}
			p2, rerr := pipeline.Restore(st, mem, clk, cfg)
			if rerr != nil {
				return false
			}
			if cerr := p2.Close(); cerr != nil {
				return false
			}
			got = append(got, mem.Bytes()...)
		}
		if !bytes.Equal(got, ref) {
			return false
		}
	}
	return true
}

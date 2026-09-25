package main

import (
	"bytes"
	"errors"
	"fmt"
	"strconv"
	"sync"

	"ontology/pipeline"
	"ontology/sink"
	"ontology/sizeline"
)

func checkChunkSeq() bool {
	cfg := demoCfg()
	splits := [][][]byte{
		{payload},
		{payload[:10], payload[10:20], payload[20:]},
		{payload[:1], payload[1:2], payload[2:8], payload[8:36]},
		{payload[:5], payload[5:36]},
	}
	ref := reference(cfg, splits[0])
	for _, parts := range splits {
		if !bytes.Equal(reference(cfg, parts), ref) {
			return false
		}
	}
	s := &sink.Scripted{CutAfter: -1, FailAt: -1, Quota: 1 << 20}
	p, _ := pipeline.New(s, &mclock{}, cfg)
	if _, err := p.Write(payload); err != nil {
		return false
	}
	if err := p.Close(); err != nil {
		return false
	}
	return fmt.Sprint(p.ChunkSizes()) == "[16 16 4]"
}

func checkExts() bool {
	exts := []sizeline.Ext{{Key: "note", Value: `a;b=c"d\e f`}, {Key: "plain", Value: "ok"}}
	line := sizeline.Line(7, exts)
	size, got, err := parseExts(line)
	return err == nil && size == 7 && got["note"] == exts[0].Value && got["plain"] == "ok"
}

func parseExts(line []byte) (int64, map[string]string, error) {
	out := map[string]string{}
	i := 0
	for i < len(line) && line[i] != ';' && line[i] != '\r' {
		i++
	}
	size, err := strconv.ParseInt(string(line[:i]), 16, 64)
	if err != nil {
		return 0, nil, err
	}
	for i < len(line) && line[i] == ';' {
		i++
		j := i
		for line[j] != '=' {
			j++
		}
		key := string(line[i:j])
		j++
		var val string
		if line[j] == '"' {
			j++
			var b []byte
			for line[j] != '"' {
				if line[j] == '\\' {
					j++
				}
				b = append(b, line[j])
				j++
			}
			j++
			val = string(b)
		} else {
			k := j
			for k < len(line) && line[k] != ';' && line[k] != '\r' {
				k++
			}
			val = string(line[j:k])
			j = k
		}
		out[key] = val
		i = j
	}
	return size, out, nil
}

func checkLimits() bool {
	cfg := demoCfg()
	bad := cfg
	bad.MinChunk = bad.MaxChunk + 1
	if _, err := pipeline.New(&sink.Memory{}, &mclock{}, bad); !errors.Is(err, pipeline.ErrChunkTooLarge) {
		return false
	}
	bad = cfg
	bad.MaxExtLen = 1
	bad.Exts = []sizeline.Ext{{Key: "k", Value: "vvvv"}}
	if _, err := pipeline.New(&sink.Memory{}, &mclock{}, bad); !errors.Is(err, pipeline.ErrExtTooLong) {
		return false
	}
	small := cfg
	small.MaxBuffer = 10
	s := &sink.Scripted{CutAfter: -1, FailAt: -1}
	p, _ := pipeline.New(s, &mclock{}, small)
	if _, err := p.Write([]byte("12345678")); err != nil && !errors.Is(err, sink.ErrBackpressure) {
		return false
	}
	before := p.Stats()
	if _, err := p.Write([]byte("xyz")); !errors.Is(err, pipeline.ErrBackpressure) {
		return false
	}
	if p.Stats() != before {
		return false
	}
	s.AddQuota(1 << 20)
	if err := p.Close(); err != nil {
		return false
	}
	return p.Stats().Confirmed == 8
}

func checkIdentity() bool {
	s := &sink.Scripted{Quota: 1 << 20, CutAfter: -1, FailAt: -1}
	p, _ := pipeline.New(s, &mclock{}, demoCfg())
	var wg sync.WaitGroup
	bad := false
	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 50; i++ {
				if _, err := p.Write([]byte("0123456789")); err != nil {
					bad = true
				}
			}
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 200; i++ {
			_ = p.Pump()
		}
	}()
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 2000; i++ {
			st := p.Stats()
			if st.Accepted != st.Confirmed+st.Buffered {
				bad = true
			}
		}
	}()
	wg.Wait()
	if err := p.Close(); err != nil {
		return false
	}
	st := p.Stats()
	return !bad && st.Accepted == 2000 && st.Confirmed == 2000 && st.Buffered == 0
}

func checkScan() bool {
	measure := func(n int) int64 {
		cfg := demoCfg()
		cfg.MaxBuffer = n + 64
		s := &sink.Scripted{CutAfter: -1, FailAt: -1}
		p, _ := pipeline.New(s, &mclock{}, cfg)
		for done := 0; done < n; {
			b := make([]byte, 64)
			if _, err := p.Write(b); err != nil && !errors.Is(err, sink.ErrBackpressure) {
				return -1
			}
			done += 64
		}
		s0 := p.ScanBytes()
		s.AddQuota(8)
		_ = p.Pump()
		return p.ScanBytes() - s0
	}
	d1k, d1m := measure(1024), measure(1<<20)
	return d1k > 0 && d1k == d1m
}

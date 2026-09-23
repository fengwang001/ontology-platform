package main

import (
	"bytes"
	"errors"
	"time"

	"ontology/chunker"
	"ontology/pipeline"
	"ontology/sink"
	"ontology/sizeline"
)

const (
	minChunk = 4
	maxChunk = 8
	window   = time.Second
)

var (
	t0       = time.Unix(1, 0)
	payload  = []byte("hello world, chunked!")
	endFrame = []byte("0\r\n\r\n")
)

func newPipe(snk sink.Sink) (*pipeline.Pipeline, *chunker.ManualClock) {
	clk := chunker.NewManualClock(t0)
	p, err := pipeline.New(pipeline.Config{
		MinChunk: minChunk, MaxChunk: maxChunk, Window: window, Clock: clk,
	}, snk)
	if err != nil {
		panic(err)
	}
	return p, clk
}

func baseCfg() pipeline.Config {
	return pipeline.Config{
		MinChunk: minChunk, MaxChunk: maxChunk, Window: window,
		Clock: chunker.NewManualClock(t0),
	}
}

func feed(p *pipeline.Pipeline, parts ...[]byte) {
	for _, b := range parts {
		if _, err := p.Write(b); err != nil {
			panic(err)
		}
	}
}

func drain(p *pipeline.Pipeline) {
	for i := 0; i < 1_000_000; i++ {
		err := p.Advance()
		if err != nil && !errors.Is(err, sink.ErrBackpressure) && !errors.Is(err, sink.ErrDisconnected) {
			panic(err)
		}
		if p.Stats().Pending == 0 {
			return
		}
	}
	panic("drain did not finish")
}

// reference 给出从不中断时的完整编码字节序列。
func reference() []byte {
	snk := sink.Controlled{}
	p, _ := newPipe(&snk)
	feed(p, payload)
	if err := p.Close(); err != nil {
		panic(err)
	}
	return snk.Bytes()
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

// firstLine 返回首行（不含 CRLF）及其占用的字节数。
func firstLine(wire []byte) (line []byte, n int, err error) {
	i := bytes.Index(wire, []byte{'\r', '\n'})
	if i < 0 {
		return nil, 0, errors.New("no CRLF")
	}
	return wire[:i], i + 2, nil
}

// parseDataSizes 用最小解析器读出每个数据块大小，直到结束块。
func parseDataSizes(wire []byte) ([]int, error) {
	var sizes []int
	off := 0
	for {
		line, n, err := firstLine(wire[off:])
		if err != nil {
			return nil, err
		}
		size, _, err := sizeline.ParseLine(line)
		if err != nil {
			return nil, err
		}
		off += n + size + 2 // 大小行(含CRLF) + 载荷 + CRLF
		if size == 0 {
			return sizes, nil
		}
		sizes = append(sizes, size)
	}
}

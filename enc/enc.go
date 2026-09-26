// Package enc 提供流式 LZ77 压缩器与按块并行压缩。单个 Encoder 非并发安全。
package enc

import (
	"bytes"
	"errors"
	"hash/crc64"
	"io"
	"sync"

	"ontology/match"
	"ontology/wire"
)

var (
	ErrClosed      = errors.New("enc: encoder closed") // 已关闭后再写
	ErrBadParallel = errors.New("enc: blockSize and workers must be positive")
)

// maxMatch 是匹配长度上限：保证每字节考察数与输入规模无关。
const minMatch, maxMatch = 4, 4096

var table = crc64.MakeTable(crc64.ECMA)

type Config struct{ WindowCap, ChainLen int }

// Encoder 是流式压缩器。Write 只缓冲不编码，编码决策推迟到 Flush/Close，
// 因此压缩输出只取决于输入字节与 Flush 位置，与 Write 切法无关。
type Encoder struct {
	sw             *wire.Writer
	m              *match.Matcher
	pending        []byte
	total          int64
	sum            uint64
	header, closed bool
}

func New(w io.Writer, cfg Config) (*Encoder, error) {
	m, err := match.New(cfg.WindowCap, cfg.ChainLen, maxMatch)
	if err != nil {
		return nil, err
	}
	return &Encoder{sw: &wire.Writer{W: w}, m: m}, nil
}

func (e *Encoder) Write(p []byte) (int, error) {
	if e.closed {
		return 0, ErrClosed
	}
	e.pending = append(e.pending, p...)
	e.sum = crc64.Update(e.sum, table, p)
	e.total += int64(len(p))
	return len(p), nil
}

func (e *Encoder) Flush() error {
	if e.closed {
		return ErrClosed
	}
	if len(e.pending) == 0 {
		return nil
	}
	e.encodeAll()
	e.sw.Flush()
	return e.sw.Err
}

func (e *Encoder) Close() error {
	if e.closed {
		return ErrClosed
	}
	e.encodeAll()
	e.sw.Trailer(e.total, e.sum)
	e.closed = true
	return e.sw.Err
}

func (e *Encoder) Candidates() int64 { return e.m.Candidates() }

func (e *Encoder) encodeAll() {
	if !e.header {
		e.sw.Header()
		e.header = true
	}
	encodeTokens(e.sw, e.m, e.pending)
	e.pending = e.pending[:0]
}

func encodeTokens(sw *wire.Writer, m *match.Matcher, data []byte) {
	for i := 0; i < len(data); {
		if dist, length := m.Best(data, i); length >= minMatch {
			sw.Match(dist, length)
			for j := i; j < i+length; j++ {
				m.Advance(data, j)
			}
			i += length
		} else {
			sw.Lit(data[i])
			m.Advance(data, i)
			i++
		}
	}
	sw.EndLiterals()
}

// CompressParallel 按 blockSize 切块并发压缩；每块以上一块末尾窗口容量
// 以内的原文为预置字典。输出与 workers 取值无关，逐字节确定。
func CompressParallel(data []byte, blockSize, workers int, cfg Config) ([]byte, error) {
	if blockSize <= 0 || workers <= 0 {
		return nil, ErrBadParallel
	}
	if _, err := match.New(cfg.WindowCap, cfg.ChainLen, maxMatch); err != nil {
		return nil, err
	}
	n := (len(data) + blockSize - 1) / blockSize
	recs := make([][]byte, n)
	var wg sync.WaitGroup
	wg.Add(workers)
	for w := 0; w < workers; w++ {
		go func(w int) {
			defer wg.Done()
			for i := w; i < n; i += workers {
				recs[i] = encodeBlock(data, i, blockSize, cfg)
			}
		}(w)
	}
	wg.Wait()
	var out bytes.Buffer
	sw := &wire.Writer{W: &out}
	sw.Header()
	for _, r := range recs {
		out.Write(r)
	}
	sw.Trailer(int64(len(data)), crc64.Checksum(data, table))
	return out.Bytes(), sw.Err
}

func encodeBlock(data []byte, idx, bs int, cfg Config) []byte {
	start := idx * bs
	m, _ := match.New(cfg.WindowCap, cfg.ChainLen, maxMatch)
	dict := data[max(start-bs, start-cfg.WindowCap, 0):start]
	block := data[start:min(start+bs, len(data))]
	m.Seed(dict, block)
	var buf bytes.Buffer
	encodeTokens(&wire.Writer{W: &buf}, m, block)
	return buf.Bytes()
}

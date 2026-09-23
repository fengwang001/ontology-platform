// Package stream 串起 roll/split/addr/store，提供对外的分块写入、重组与自检。
package stream

import (
	"bytes"
	"errors"
	"fmt"
	"sync"

	"ontology/addr"
	"ontology/split"
	"ontology/store"
)

var (
	// ErrClosed 表示 Flush 之后又调用了 Write。
	ErrClosed = errors.New("stream: write after flush")
	// ErrInvariant 表示 SelfCheck 发现不变量被破坏。
	ErrInvariant = errors.New("stream: invariant violation")
)

// Params 是对外分块参数。
type Params = split.Config

// Chunk 是块序列中的一个元素。
type Chunk struct {
	Start int64
	End   int64
	Addr  addr.Addr
}

// Streamer 先缓冲写入内容，Flush 时原子地完成分块、去重与提交。
type Streamer struct {
	mu     sync.RWMutex
	spl    *split.Splitter
	st     *store.Store
	buf    []byte
	writes int
	done   bool
	chunks []Chunk
}

// New 创建分块流。params 校验错误、资源上限均在此处或 Flush 时可判定。
func New(params Params, limits store.Limits) (*Streamer, error) {
	spl, err := split.New(params)
	if err != nil {
		return nil, err
	}
	return &Streamer{spl: spl, st: store.New(limits)}, nil
}

// Store 返回底层块存储（可直接按地址取回）。
func (s *Streamer) Store() *store.Store { return s.st }

// Writes 返回 Write 被调用的次数（零长 Write 也计数）。
func (s *Streamer) Writes() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.writes
}

// Write 缓冲一段字节；零长写入合法但不改变内容。Flush 后写入被拒绝。
func (s *Streamer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.done {
		return 0, ErrClosed
	}
	s.writes++
	if len(p) > 0 {
		s.buf = append(s.buf, p...)
	}
	return len(p), nil
}

// Flush 完成分块并原子提交。失败时回滚本次全部新增引用，状态不变、可继续重 Flush。
func (s *Streamer) Flush() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	spans := s.spl.Cut(s.buf)
	var staged []addr.Addr
	committed := make([]Chunk, 0, len(spans))
	cleanup := func() {
		for _, a := range staged {
			_ = s.st.Release(a)
		}
	}
	for _, sp := range spans {
		a, err := s.st.Add(s.buf[sp.Start:sp.End])
		if err != nil {
			cleanup()
			return err
		}
		staged = append(staged, a)
		committed = append(committed, Chunk{sp.Start, sp.End, a})
	}
	s.chunks = committed
	s.done = true
	return nil
}

// Chunks 返回块序列（地址即稳定快照，返回拷贝）。
func (s *Streamer) Chunks() []Chunk {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Chunk, len(s.chunks))
	copy(out, s.chunks)
	return out
}

// Reassemble 按块序列从 store 取回并拼接，逐字节复现原流。
func (s *Streamer) Reassemble() ([]byte, error) {
	s.mu.RLock()
	chunks := make([]Chunk, len(s.chunks))
	copy(chunks, s.chunks)
	s.mu.RUnlock()
	var b bytes.Buffer
	for _, c := range chunks {
		p, err := s.st.Get(c.Addr)
		if err != nil {
			return nil, err
		}
		b.Write(p)
	}
	return b.Bytes(), nil
}

// SelfCheck 一次性核验偏移、长度、引用计数与零引用残留。
func (s *Streamer) SelfCheck(minP, maxP int) error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	n := int64(len(s.buf))
	var next int64
	counts := map[addr.Addr]int64{}
	for i, c := range s.chunks {
		if c.Start != next {
			return fmt.Errorf("%w: 块 %d 偏移不衔接", ErrInvariant, i)
		}
		next = c.End
		l := c.End - c.Start
		switch {
		case i < len(s.chunks)-1 && (l < int64(minP) || l > int64(maxP)):
			return fmt.Errorf("%w: 非末块长度 %d 越界", ErrInvariant, l)
		case len(s.chunks) > 1 && i == len(s.chunks)-1 && (l < 1 || l > int64(maxP+minP-1)):
			return fmt.Errorf("%w: 末块长度 %d 越界", ErrInvariant, l)
		case len(s.chunks) == 1 && (l < 1 || l > int64(maxP)):
			return fmt.Errorf("%w: 唯一块长度 %d 越界", ErrInvariant, l)
		}
		counts[c.Addr]++
	}
	if next != n {
		return fmt.Errorf("%w: 未覆盖全流 %d!=%d", ErrInvariant, next, n)
	}
	snap := s.st.Snapshot()
	for a, want := range counts {
		if snap[a] != want {
			return fmt.Errorf("%w: 地址 %s 计数 %d!=序列出现 %d",
				ErrInvariant, a, snap[a], want)
		}
	}
	for a, refs := range snap {
		if refs <= 0 {
			return fmt.Errorf("%w: 零引用残留", ErrInvariant)
		}
		if _, used := counts[a]; !used {
			return fmt.Errorf("%w: 残留块 %s 不在序列中", ErrInvariant, a)
		}
	}
	return nil
}

// Package stream 串起 roll/split/addr/store：接收任意切分的 Write，
// 产出与写入方式无关的内容定义块序列，并支持重组与自检。
package stream

import (
	"bytes"
	"errors"
	"sync"

	"ontology/addr"
	"ontology/roll"
	"ontology/split"
	"ontology/store"
)

var (
	errLen      = errors.New("selfcheck: block length out of [min,max]")
	errGap      = errors.New("selfcheck: block offsets are not contiguous")
	errMissing  = errors.New("selfcheck: chunk address missing in store")
	errTotal    = errors.New("selfcheck: chunk span disagrees with total bytes")
	errRefs     = errors.New("selfcheck: reference count disagrees with chunk sequence")
	errResidual = errors.New("selfcheck: zero-reference residual block in store")
)

// Chunk 描述块序列中的一个块。
type Chunk struct {
	Start int64
	End   int64
	Addr  addr.Addr
}

// Stats 是只读统计。
type Stats struct {
	Writes     int
	ZeroWrites int
	TotalBytes int64
	Advances   int // 滚动哈希"进一出一"推进总次数
}

// Streamer 是对外的流式分块去重器。
type Streamer struct {
	mu sync.RWMutex

	cfg    split.Config
	store  *store.Store
	rh     *roll.Hasher
	buf    []byte
	start  int64
	total  int64
	chunks []Chunk
	writes int
	zeros  int

	advances int // 非导出计数器，不进入任何公开接口字段
}

// New 创建分块器；参数非法返回 split 包对应哨兵错误。
func New(cfg split.Config, limits store.Limits) (*Streamer, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	rh, err := roll.New(cfg.Window)
	if err != nil {
		return nil, err
	}
	return &Streamer{cfg: cfg, store: store.New(limits), rh: rh}, nil
}

func hit(h uint32) bool { return h&(1<<split.MaskBits-1) == 0 }

// Write 喂入一段字节。哈希器只对新字节做 O(1) 增量推进并跨 Write 保持状态，
// 因此切分只取决于累计字节流。先扫描出本批可闭合的块并尝试落库；任一 Put
// 失败，释放已加引用并恢复扫描前的哈希/缓冲状态（失败不留痕）。
func (s *Streamer) Write(p []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.writes++
	if len(p) == 0 {
		s.zeros++
		return nil
	}

	rhSnap := s.rh.Snapshot()
	bufSnap := append([]byte(nil), s.buf...)
	startSnap, advSnap, totalSnap := s.start, s.advances, s.total

	type edge struct {
		data []byte
		end  int64
	}
	var edges []edge
	curStartAbs := s.start // 当前正在累积的块起点（绝对偏移）
	cur := append([]byte(nil), s.buf...)
	blockLen := len(cur) // 当前块已有长度（跨 Write 延续）
	for _, b := range p {
		advBefore := s.rh.Advances()
		s.rh.Push(b)
		s.advances += s.rh.Advances() - advBefore
		cur = append(cur, b)
		blockLen++
		n := blockLen
		if n >= s.cfg.Min && (n >= s.cfg.Max || (s.rh.Full() && hit(s.rh.Sum()))) {
			edges = append(edges, edge{
				data: append([]byte(nil), cur...),
				end:  curStartAbs + int64(n),
			})
			curStartAbs += int64(n)
			cur = nil
			blockLen = 0
			s.rh.Reset()
		}
	}
	added := make([]addr.Addr, 0, len(edges))
	for _, e := range edges {
		a, err := s.store.Put(e.data)
		if err != nil {
			for _, x := range added {
				_ = s.store.Release(x)
			}
			*s.rh = rhSnap
			s.buf, s.start, s.advances, s.total = bufSnap, startSnap, advSnap, totalSnap
			return err
		}
		added = append(added, a)
	}
	for i, e := range edges {
		var st int64
		if i == 0 {
			st = startSnap
		} else {
			st = edges[i-1].end
		}
		s.chunks = append(s.chunks, Chunk{Start: st, End: e.end, Addr: added[i]})
	}
	if len(edges) > 0 {
		s.start = edges[len(edges)-1].end
	}
	s.buf = cur
	s.total = totalSnap + int64(len(p))
	return nil
}

// Flush 闭合流：残余 r>0 单独作为最后一块（允许短于 min）。
func (s *Streamer) Flush() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.buf) == 0 {
		return nil
	}
	data := append([]byte(nil), s.buf...)
	a, err := s.store.Put(data)
	if err != nil {
		return err // 未落库：状态不变，可继续写入
	}
	s.chunks = append(s.chunks, Chunk{Start: s.start, End: s.start + int64(len(data)), Addr: a})
	s.start += int64(len(data))
	s.buf = nil
	return nil
}

// Chunks 返回块序列快照副本。
func (s *Streamer) Chunks() []Chunk {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Chunk, len(s.chunks))
	copy(out, s.chunks)
	return out
}

// Get 按地址取回块内容。
func (s *Streamer) Get(a addr.Addr) ([]byte, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.store.Get(a)
}

// Reassemble 按块序列取回并拼接。
func (s *Streamer) Reassemble() ([]byte, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var b bytes.Buffer
	for _, c := range s.chunks {
		data, err := s.store.Get(c.Addr)
		if err != nil {
			return nil, err
		}
		b.Write(data)
	}
	return b.Bytes(), nil
}

// SelfCheck 一次性核验四条内部一致性，返回聚合错误（nil 即全部通过）。
func (s *Streamer) SelfCheck() error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	seen := map[addr.Addr]int{}
	for i, c := range s.chunks {
		if c.End-c.Start < int64(s.cfg.Min) || c.End-c.Start > int64(s.cfg.Max) {
			if !(i == len(s.chunks)-1 && c.End-c.Start < int64(s.cfg.Min)) {
				return errLen
			}
		}
		if i > 0 && c.Start != s.chunks[i-1].End {
			return errGap
		}
		if _, err := s.store.Get(c.Addr); err != nil {
			return errMissing
		}
		seen[c.Addr]++
	}
	end := int64(0)
	if len(s.chunks) > 0 {
		end = s.chunks[len(s.chunks)-1].End
	}
	if end != s.total {
		return errTotal
	}
	for a, n := range seen {
		if s.store.Refs(a) != n {
			return errRefs
		}
		if s.store.BlockCount() != len(seen) {
			return errResidual
		}
	}
	return nil
}

// Stats 返回只读统计。
func (s *Streamer) Stats() Stats {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return Stats{Writes: s.writes, ZeroWrites: s.zeros, TotalBytes: s.total, Advances: s.advances}
}

// Store 暴露底层存储以做按地址引用计数核验。
func (s *Streamer) Store() *store.Store { return s.store }

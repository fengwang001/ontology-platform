// Package ontology 提供块索引分隔键（separator）的最短化构建器。
package ontology

import (
	"bytes"
	"errors"
	"sort"
	"sync"
)

// 可区分的拒绝原因。调用方可用 errors.Is 精确判定。
var (
	ErrEmptyKey          = errors.New("ontology: key must not be empty")
	ErrLastNotBeforeNext = errors.New("ontology: last must be strictly less than next")
	ErrOutOfOrderBlock   = errors.New("ontology: block last must not be less than previous next")
	ErrAlreadyFinished   = errors.New("ontology: builder already finished")
	ErrNotFinished       = errors.New("ontology: seek before finish is not allowed")
)

// SeekOutOfBound 表示 key 大于全部分隔键，不属于任何已登记块。
var SeekOutOfBound = -1

// Builder 以串行等价的方式并发安全地登记相邻数据块并生成分隔键。
type Builder struct {
	mu       sync.RWMutex
	seps     [][]byte
	prevNext []byte
	finished bool
}

// AddBlock 登记一个非末尾块：last 为本块最大键，next 为下一块最小键。
//
// 校验顺序（只报第一个）：空键（先 last 后 next）→ last >= next →
// last < 上一块 next → 已 Finish。被拒绝时不改变已登记内容。
func (b *Builder) AddBlock(last, next []byte) ([]byte, error) {
	if len(last) == 0 {
		return nil, ErrEmptyKey
	}
	if len(next) == 0 {
		return nil, ErrEmptyKey
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	if bytes.Compare(last, next) >= 0 {
		return nil, ErrLastNotBeforeNext
	}
	if b.prevNext != nil && bytes.Compare(last, b.prevNext) < 0 {
		return nil, ErrOutOfOrderBlock
	}
	if b.finished {
		return nil, ErrAlreadyFinished
	}

	sep := makeSep(last, next)
	b.seps = append(b.seps, sep)
	b.prevNext = append([]byte(nil), next...)
	return append([]byte(nil), sep...), nil
}

// Finish 登记最后一块（无下一块）。
//
// 校验顺序（只报第一个）：空键 → last < 上一块 next → 已 Finish。
func (b *Builder) Finish(last []byte) ([]byte, error) {
	if len(last) == 0 {
		return nil, ErrEmptyKey
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	if b.prevNext != nil && bytes.Compare(last, b.prevNext) < 0 {
		return nil, ErrOutOfOrderBlock
	}
	if b.finished {
		return nil, ErrAlreadyFinished
	}

	sep := makeFinalSep(last)
	b.seps = append(b.seps, sep)
	b.finished = true
	return append([]byte(nil), sep...), nil
}

// Seek 返回第一个满足 sep >= key 的块下标；key 大于全部 sep 返回 SeekOutOfBound。
// Finish 之前调用会返回 ErrNotFinished。
func (b *Builder) Seek(key []byte) (int, error) {
	b.mu.RLock()
	defer b.mu.RUnlock()

	if !b.finished {
		return 0, ErrNotFinished
	}

	idx := sort.Search(len(b.seps), func(i int) bool {
		return bytes.Compare(b.seps[i], key) >= 0
	})
	if idx == len(b.seps) {
		return SeekOutOfBound, nil
	}
	return idx, nil
}

// Seps 返回全部分隔键的逐字节副本。
func (b *Builder) Seps() [][]byte {
	b.mu.RLock()
	defer b.mu.RUnlock()

	out := make([][]byte, len(b.seps))
	for i, sep := range b.seps {
		out[i] = append([]byte(nil), sep...)
	}
	return out
}

// makeSep 按规则在 last 与 next 之间构造尽量短的分隔键。
// 令 d 为最长公共前缀长度：
//   - 若 d 等于 last 或 next 的长度（一个是另一个的前缀），sep = last；
//   - 否则令 b = last[d]，仅当 b < 0xFF 且 b+1 < next[d] 时
//     sep = last[:d] + [b+1]；否则 sep = last。
func makeSep(last, next []byte) []byte {
	d := 0
	for d < len(last) && d < len(next) && last[d] == next[d] {
		d++
	}
	if d == len(last) || d == len(next) {
		return append([]byte(nil), last...)
	}
	bb := last[d]
	if bb < 0xFF && bb+1 < next[d] {
		sep := make([]byte, 0, d+1)
		sep = append(sep, last[:d]...)
		sep = append(sep, bb+1)
		return sep
	}
	return append([]byte(nil), last...)
}

// makeFinalSep 构造最后一块的分隔键：取 last 中第一个不等于 0xFF 的字节，
// 将其加 1 并截断其后内容；若全部字节都是 0xFF，则 sep = last。
func makeFinalSep(last []byte) []byte {
	for i, c := range last {
		if c != 0xFF {
			sep := make([]byte, 0, i+1)
			sep = append(sep, last[:i]...)
			sep = append(sep, c+1)
			return sep
		}
	}
	return append([]byte(nil), last...)
}

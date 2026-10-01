// Package sepkey 提供块索引分隔键最短化构建器。
//
// 构建器为相邻数据块生成介于两块之间且尽量短的分隔键：
// 键为字节串，按字节序比较。相同的登记序列重放得到逐字节相同的分隔键。
// AddBlock、Finish 与 Seek 可并发调用，结果等价于某个串行顺序。
package sepkey

import (
	"bytes"
	"errors"
	"sort"
	"sync"
)

// 可区分的拒绝原因。
var (
	// ErrEmptyKey 键为空（last 或 next 长度为零）。
	ErrEmptyKey = errors.New("sepkey: 键为空")
	// ErrOutOfOrder last 不小于 next（last 必须严格小于 next）。
	ErrOutOfOrder = errors.New("sepkey: last 不小于 next")
	// ErrNotMonotonic 本块 last 小于上一块的 next。
	ErrNotMonotonic = errors.New("sepkey: 本块 last 小于上一块的 next")
	// ErrFinished Finish 之后再 AddBlock 或再 Finish。
	ErrFinished = errors.New("sepkey: 已 Finish")
	// ErrNotFinished Finish 之前调用 Seek。
	ErrNotFinished = errors.New("sepkey: 尚未 Finish，禁止 Seek")
	// ErrOutOfRange Seek 的 key 大于全部分隔键。
	ErrOutOfRange = errors.New("sepkey: key 大于全部分隔键，越界")
)

// Builder 块索引分隔键构建器。零值即可使用，并发安全。
type Builder struct {
	mu       sync.RWMutex
	seps     [][]byte
	prevNext []byte // 上一块的 next；nil 表示尚无已登记块
	finished bool
}

// AddBlock 登记一个块：last 为本块最大键，next 为下一块最小键。
//
// 校验顺序（只报第一个错误）：键为空 → last 不小于 next →
// 本块 last 小于上一块的 next → 已 Finish。被拒绝时不改变已登记内容。
func (b *Builder) AddBlock(last, next []byte) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(last) == 0 || len(next) == 0 {
		return ErrEmptyKey
	}
	if bytes.Compare(last, next) >= 0 {
		return ErrOutOfOrder
	}
	if b.prevNext != nil && bytes.Compare(last, b.prevNext) < 0 {
		return ErrNotMonotonic
	}
	if b.finished {
		return ErrFinished
	}
	b.seps = append(b.seps, shorten(last, next))
	b.prevNext = append([]byte(nil), next...)
	return nil
}

// Finish 登记最后一块（无下一块）。
//
// 校验顺序（只报第一个错误）：键为空 → 小于上一块的 next → 已 Finish。
// 被拒绝时不改变已登记内容。
func (b *Builder) Finish(last []byte) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(last) == 0 {
		return ErrEmptyKey
	}
	if b.prevNext != nil && bytes.Compare(last, b.prevNext) < 0 {
		return ErrNotMonotonic
	}
	if b.finished {
		return ErrFinished
	}
	sep := append([]byte(nil), last...)
	for i, c := range sep {
		if c != 0xFF {
			sep[i] = c + 1
			sep = sep[:i+1]
			break
		}
	}
	b.seps = append(b.seps, sep)
	b.finished = true
	return nil
}

// Seek 返回第一个满足 sep ≥ key 的块下标。
// key 大于全部分隔键时返回 (-1, ErrOutOfRange)；
// Finish 之前调用返回 (-1, ErrNotFinished)。
func (b *Builder) Seek(key []byte) (int, error) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	if !b.finished {
		return -1, ErrNotFinished
	}
	i := sort.Search(len(b.seps), func(i int) bool {
		return bytes.Compare(b.seps[i], key) >= 0
	})
	if i == len(b.seps) {
		return -1, ErrOutOfRange
	}
	return i, nil
}

// Seps 返回全部分隔键的副本。
func (b *Builder) Seps() [][]byte {
	b.mu.RLock()
	defer b.mu.RUnlock()
	out := make([][]byte, len(b.seps))
	for i, s := range b.seps {
		out[i] = append([]byte(nil), s...)
	}
	return out
}

// shorten 生成介于 last 与 next 之间且尽量短的分隔键。
// 令 d 为 last 与 next 的最长公共前缀长度：
// 若 d 等于二者之一的长度则 sep=last；
// 否则设 b=last[d]，仅当 b<0xFF 且 b+1 严格小于 next[d] 时
// sep=last[:d]+[b+1]，否则 sep=last。
func shorten(last, next []byte) []byte {
	d := 0
	for d < len(last) && d < len(next) && last[d] == next[d] {
		d++
	}
	if d == len(last) || d == len(next) {
		return append([]byte(nil), last...)
	}
	if c := last[d]; c < 0xFF && c+1 < next[d] {
		sep := make([]byte, d+1)
		copy(sep, last[:d])
		sep[d] = c + 1
		return sep
	}
	return append([]byte(nil), last...)
}

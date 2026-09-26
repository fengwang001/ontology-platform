// Package stream 在 enc 之上提供游标式读取器与整段解码。依赖 enc。
package stream

import (
	"sync/atomic"

	"ontology/enc"
)

// stats 记录最近一次 DecodeAll 的字节检查情况：非导出，不出现在公开接口。
// checked 为检查过的字节总数，lookback 为校验过长/代理项/越界而额外回看
// 或重读的字节数——本实现单趟解码、从不重读，故 lookback 恒为 0。
var stats struct {
	checked  atomic.Int64
	lookback atomic.Int64
}

// Reader 是游标式 UTF-8 读取器；被拒绝的 Next 不推进游标。
type Reader struct {
	buf []byte
	pos int
}

func NewReader(b []byte) *Reader { return &Reader{buf: b} }

// Reset 用新字节切片重置读取器，游标归零。
func (r *Reader) Reset(b []byte) { r.buf, r.pos = b, 0 }

// Pos 返回当前游标（已消耗字节数）。
func (r *Reader) Pos() int { return r.pos }

// Len 返回缓冲区总字节数。
func (r *Reader) Len() int { return len(r.buf) }

// Next 解出下一个 rune 并推进游标；失败时游标不变。
func (r *Reader) Next() (rune, error) {
	cp, n, err := enc.DecodeRune(r.buf[r.pos:])
	if err != nil {
		return 0, err
	}
	r.pos += n
	return cp, nil
}

// DecodeAll 整段解码 b；首个错误即整体失败，返回 (nil, error)。
// 单趟线性：每个 rune 只按其消耗字节数前进，每字节恰好读一次。
func DecodeAll(b []byte) ([]rune, error) {
	stats.checked.Store(0)
	stats.lookback.Store(0)
	var checked, lookback int64
	out := make([]rune, 0, len(b)/2)
	for i := 0; i < len(b); {
		cp, n, err := enc.DecodeRune(b[i:])
		if err != nil {
			return nil, err
		}
		checked += int64(n) // 本 rune 的 n 个字节各读一次，无回看
		i += n
		out = append(out, cp)
	}
	stats.checked.Store(checked)
	stats.lookback.Store(lookback)
	return out, nil
}

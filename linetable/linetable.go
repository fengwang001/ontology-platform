// Package linetable 实现字节码指令到源码行号的增量行号表。
package linetable

import (
	"encoding/binary"
	"sort"
	"sync"
)

// Error 描述行号表操作失败的可区分原因。
type Error string

func (e Error) Error() string { return string(e) }

const (
	// ErrInvalidN：构造（或解码）时给定的指令总长 N 小于 1。
	ErrInvalidN Error = "linetable: N must be at least 1"
	// ErrPCOutOfRange：pc 不在 [0, N-1] 范围内。
	ErrPCOutOfRange Error = "linetable: pc out of range"
	// ErrLineNotPositive：追加的行号不是正整数。
	ErrLineNotPositive Error = "linetable: line must be positive"
	// ErrPCNotIncreasing：pc 不严格大于上一次成功追加的 pc。
	ErrPCNotIncreasing Error = "linetable: pc must be strictly greater than last appended pc"
	// ErrFirstPCNotZero：首条追加记录的 pc 不为 0。
	ErrFirstPCNotZero Error = "linetable: first appended pc must be 0"
	// ErrEmpty：查询时表中还没有任何条目。
	ErrEmpty Error = "linetable: line table is empty"

	// ErrVarintTooLong：变长整数超过 10 字节仍未结束（含超出 uint64 表示范围）。
	ErrVarintTooLong Error = "linetable: varint exceeds 10 bytes"
	// ErrVarintTruncated：字节流在续位标志（高位 1）之后截断。
	ErrVarintTruncated Error = "linetable: truncated varint"
	// ErrVarintNonCanonical：非最短形式（多于 1 字节且末字节为 0）。
	ErrVarintNonCanonical Error = "linetable: non-canonical varint"
	// ErrFirstPCDeltaNotZero：首条条目的 pc 差不为 0。
	ErrFirstPCDeltaNotZero Error = "linetable: first pc delta must be 0"
	// ErrPCDeltaNotPositive：非首条条目的 pc 差不为正。
	ErrPCDeltaNotPositive Error = "linetable: pc delta must be positive"
	// ErrPCExceedsN：累计 pc 不小于 N。
	ErrPCExceedsN Error = "linetable: cumulative pc must be less than N"
	// ErrLineDeltaZero：非首条条目的行差为 0。
	ErrLineDeltaZero Error = "linetable: line delta of non-first entry must not be 0"
	// ErrLineNotCumulativePositive：累计行号非正。
	ErrLineNotCumulativePositive Error = "linetable: cumulative line must be positive"
)

// LineTable 是按 pc 递增追加的行号表。
type LineTable struct {
	mu      sync.RWMutex
	n       int
	lastPC  int
	entries []entry
}

type entry struct {
	pc   int
	line int
}

// New 创建指令总长为 n 的行号表。
func New(n int) (*LineTable, error) {
	if n < 1 {
		return nil, ErrInvalidN
	}
	return &LineTable{n: n, lastPC: -1}, nil
}

// Append 追加一条 (pc, line) 记录。
//
// 校验按「pc 越界 → 行非正 → pc 不大于上一次成功追加的 pc → 首条 pc 不为 0」
// 的次序只返回第一个错误。若 line 与最后一个条目的行相同则合并：不新增条目，
// 只把上一次成功追加的 pc 更新为 pc。被拒绝的追加不改变任何状态。
func (t *LineTable) Append(pc, line int) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	if pc < 0 || pc >= t.n {
		return ErrPCOutOfRange
	}
	if line < 1 {
		return ErrLineNotPositive
	}
	if t.lastPC < 0 {
		if pc != 0 {
			return ErrFirstPCNotZero
		}
	} else if pc <= t.lastPC {
		return ErrPCNotIncreasing
	}
	// 全部校验通过后才变更状态，保证被拒绝的追加不留任何影响。
	t.lastPC = pc
	if len(t.entries) > 0 && t.entries[len(t.entries)-1].line == line {
		return nil
	}
	t.entries = append(t.entries, entry{pc: pc, line: line})
	return nil
}

// LineAt 查询起点不大于 pc 的最后一个条目的行号。
func (t *LineTable) LineAt(pc int) (line int, err error) {
	t.mu.RLock()
	defer t.mu.RUnlock()

	if len(t.entries) == 0 {
		return 0, ErrEmpty
	}
	if pc < 0 || pc >= t.n {
		return 0, ErrPCOutOfRange
	}
	i := sort.Search(len(t.entries), func(i int) bool {
		return t.entries[i].pc > pc
	})
	return t.entries[i-1].line, nil
}

// Encode 将条目表编码为带符号变长增量字节串。
//
// 每个条目依次写出两个无符号变长整数：(pc-上一条目pc) 与 (line-上一条目line)，
// 首条的上一条目视为 (pc 0, 行 0)。差值先 zigzag 映射再按 LEB128 写出。
// 编码只读：同一张表在未被修改时结果恒定。
func (t *LineTable) Encode() []byte {
	t.mu.RLock()
	defer t.mu.RUnlock()

	out := make([]byte, 0, len(t.entries)*4)
	prevPC, prevLine := 0, 0
	for _, e := range t.entries {
		out = binary.AppendUvarint(out, uint64(zigzag(int64(e.pc-prevPC))))
		out = binary.AppendUvarint(out, uint64(zigzag(int64(e.line-prevLine))))
		prevPC, prevLine = e.pc, e.line
	}
	return out
}

// Decode 从字节串还原指令总长为 n 的行号表。
//
// 字节级错误按字节顺序遇到第一处即报：超过 10 字节未结束 → 续位后截断 →
// 非最短形式；随后按条目检查「首条 pc 差不为 0 → 非首条 pc 差不为正 →
// 累计 pc 不小于 N → 非首条行差为 0 → 累计行非正」。解码失败不留下半成品。
// 对任意合法编码，解码后重新编码得到原字节串。
func Decode(n int, data []byte) (*LineTable, error) {
	if n < 1 {
		return nil, ErrInvalidN
	}

	t, err := New(n)
	if err != nil {
		return nil, err
	}

	pos := 0
	first := true
	prevPC, prevLine := 0, 0
	for pos < len(data) {
		pcDeltaU, advance, perr := readUvarint(data[pos:])
		if perr != nil {
			return nil, perr
		}
		pos += advance
		lineDeltaU, advance, lerr := readUvarint(data[pos:])
		if lerr != nil {
			return nil, lerr
		}
		pos += advance

		pcDelta := int(unzigzag(pcDeltaU))
		lineDelta := int(unzigzag(lineDeltaU))

		switch {
		case first && pcDelta != 0:
			return nil, ErrFirstPCDeltaNotZero
		case !first && pcDelta <= 0:
			return nil, ErrPCDeltaNotPositive
		}
		curPC := prevPC + pcDelta
		if curPC >= n {
			return nil, ErrPCExceedsN
		}
		if !first && lineDelta == 0 {
			return nil, ErrLineDeltaZero
		}
		curLine := prevLine + lineDelta
		if curLine <= 0 {
			return nil, ErrLineNotCumulativePositive
		}

		t.entries = append(t.entries, entry{pc: curPC, line: curLine})
		t.lastPC = curPC
		prevPC, prevLine = curPC, curLine
		first = false
	}

	return t, nil
}

// zigzag 把有符号整数映射为无符号整数：n>=0 → 2n；n<0 → -2n-1。
func zigzag(n int64) uint64 {
	return uint64((n << 1) ^ (n >> 63))
}

// unzigzag 是 zigzag 的逆映射。
func unzigzag(u uint64) int64 {
	return int64(u>>1) ^ -int64(u&1)
}

// readUvarint 从 data 读取一个 LEB128 无符号变长整数，返回值、消耗字节数与错误。
//
// 错误次序（按解析位置）：读到第 11 个仍带续位的字节 → ErrVarintTooLong；
// 续位之后没有更多字节 → ErrVarintTruncated；多于 1 字节且末字节为 0 →
// ErrVarintNonCanonical；移位后超出 uint64 → ErrVarintTooLong。
func readUvarint(data []byte) (uint64, int, error) {
	var x uint64
	for i := 0; i < 10; i++ {
		if i >= len(data) {
			return 0, 0, ErrVarintTruncated
		}
		b := data[i]
		if i == 9 && b > 1 {
			return 0, 0, ErrVarintTooLong
		}
		x |= uint64(b&0x7f) << (7 * i)
		if b&0x80 == 0 {
			if i > 0 && b == 0 {
				return 0, 0, ErrVarintNonCanonical
			}
			return x, i + 1, nil
		}
		if i == 9 {
			return 0, 0, ErrVarintTooLong
		}
		if i+1 >= len(data) {
			return 0, 0, ErrVarintTruncated
		}
	}
	return 0, 0, ErrVarintTooLong
}

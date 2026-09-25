// Package hcode 规范码分配与 Encode/Decode 的位级实现（码长表 + 首码表 O(1) 反查）。
package hcode

import (
	"errors"
	"sort"
	"sync/atomic"
)

var (
	// ErrUnknownSymbol 编码遇到不在码表里的符号。
	ErrUnknownSymbol = errors.New("hcode: unknown symbol")
	// ErrTruncated 位流不足以解出 n 个符号或码字被截断。
	ErrTruncated = errors.New("hcode: truncated bit stream")
	// ErrIllegalPadding 超出 n 个符号的填充位非零（或多出整字节）。
	ErrIllegalPadding = errors.New("hcode: illegal padding")
)

// Table 是规范 Huffman 码表，构建后只读，可并发使用。
type Table struct {
	code   map[byte]uint64
	length map[byte]int
	maxLen int
	count  []int    // count[l]：码长为 l 的符号数
	first  []uint64 // first[l]：码长 l 的首个规范码
	syms   []byte   // 符号按 (码长, 字典序) 排序
	off    []int    // off[l]：码长 l 的首符号在 syms 中的下标

	lastChecks atomic.Int64 // 非导出：最近一次解码单个符号检查的条目数
}

// New 由码长表构建规范码表（码长升序、符号字典序升序依次赋码）。
func New(lengths map[byte]int) *Table {
	t := &Table{code: map[byte]uint64{}, length: map[byte]int{}, maxLen: 1}
	for _, l := range lengths {
		if l > t.maxLen {
			t.maxLen = l
		}
	}
	t.count = make([]int, t.maxLen+1)
	t.first = make([]uint64, t.maxLen+1)
	t.off = make([]int, t.maxLen+1)
	for s, l := range lengths {
		t.count[l]++
		t.length[s] = l
		t.syms = append(t.syms, s)
	}
	sort.Slice(t.syms, func(i, j int) bool {
		li, lj := lengths[t.syms[i]], lengths[t.syms[j]]
		if li != lj {
			return li < lj
		}
		return t.syms[i] < t.syms[j]
	})
	var code uint64 // 各码长的首码：first[l] = (first[l-1] + count[l-1]) << 1
	sum := 0
	for l := 1; l <= t.maxLen; l++ {
		code = (code + uint64(t.count[l-1])) << 1
		t.first[l], t.off[l] = code, sum
		sum += t.count[l]
	}
	next := append([]uint64(nil), t.first...)
	for _, s := range t.syms {
		l := lengths[s]
		t.code[s] = next[l]
		next[l]++
	}
	return t
}

// CodeOf 返回符号的码字二进制串（高位在前）。
func (t *Table) CodeOf(s byte) (string, bool) {
	c, ok := t.code[s]
	if !ok {
		return "", false
	}
	b := make([]byte, t.length[s])
	for i := t.length[s] - 1; i >= 0; i-- {
		b[i] = byte('0' + c&1)
		c >>= 1
	}
	return string(b), true
}

// MaxLen 返回最长码长。
func (t *Table) MaxLen() int { return t.maxLen }

// Encode 把每个符号的码字从左到右拼接成大端比特串打包，末字节低位补 0。
func (t *Table) Encode(msg []byte) ([]byte, error) {
	out := make([]byte, 0, len(msg))
	var cur byte
	nb := 0
	for _, s := range msg {
		c, ok := t.code[s]
		if !ok {
			return nil, ErrUnknownSymbol
		}
		for i := t.length[s] - 1; i >= 0; i-- {
			cur = cur<<1 | byte(c>>uint(i)&1)
			if nb++; nb == 8 {
				out = append(out, cur)
				cur, nb = 0, 0
			}
		}
	}
	if nb > 0 {
		out = append(out, cur<<(8-nb))
	}
	return out, nil
}

// Decode 从位流逐个读码字（码长表 + 首码表反查），解出 n 个符号后校验填充。
func (t *Table) Decode(b []byte, n int) ([]byte, error) {
	out := make([]byte, 0, n)
	pos := 0
	total := len(b) * 8
	for len(out) < n {
		var code uint64
		matched := false
		for l := 1; l <= t.maxLen; l++ {
			if pos >= total {
				return nil, ErrTruncated
			}
			code = code<<1 | uint64(b[pos/8]>>uint(7-pos%8)&1)
			pos++
			t.lastChecks.Store(int64(l))
			if t.count[l] > 0 && code >= t.first[l] && code-t.first[l] < uint64(t.count[l]) {
				out = append(out, t.syms[t.off[l]+int(code-t.first[l])])
				matched = true
				break
			}
		}
		if !matched {
			return nil, ErrTruncated
		}
	}
	if total-pos >= 8 {
		return nil, ErrIllegalPadding
	}
	for ; pos < total; pos++ {
		if b[pos/8]>>uint(7-pos%8)&1 != 0 {
			return nil, ErrIllegalPadding
		}
	}
	return out, nil
}

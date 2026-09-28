// Package window 提供固定容量的滑动窗口环形缓冲。
package window

import "fmt"

// Window 保存最近至多 cap 个字节，按绝对位置取字节。
type Window struct {
	buf  []byte
	mask uint64
	size uint64
	lim  uint64
	base uint64 // 下一个写入位置（绝对位置）
}

// New 创建容量为 2 的幂、至少为 3 的窗口。
func New(capacity int) *Window {
	const minCap = 3
	if capacity < minCap {
		capacity = minCap
	}
	size := uint64(4)
	for size < uint64(capacity) {
		size <<= 1
	}
	return &Window{buf: make([]byte, size), mask: size - 1, size: size, lim: uint64(capacity)}
}

// Cap 返回构造时给定的容量上限。
func (w *Window) Cap() int { return int(w.lim) }

// Base 返回已写入字节总数（下一个写入的绝对位置）。
func (w *Window) Base() uint64 { return w.base }

// Put 追加一个字节。
func (w *Window) Put(b byte) {
	w.buf[w.base&w.mask] = b
	w.base++
}

// PutBytes 批量追加。
func (w *Window) PutBytes(p []byte) {
	for _, b := range p {
		w.buf[w.base&w.mask] = b
		w.base++
	}
}

// ByteAt 取绝对位置 pos 的字节；pos 必须落在现存窗口内。
func (w *Window) ByteAt(pos uint64) (byte, error) {
	if pos >= w.base || w.base-pos > w.lim {
		return 0, fmt.Errorf("window: position %d out of range [base=%d]", pos, w.base)
	}
	return w.buf[pos&w.mask], nil
}

// CopyBack 在 dst[dstOff:] 上按「距离 dist、长度 length」逐字节向前复制。
// 源字节通过 at 按绝对位置获取，天然支持 dist<length 的重叠回指；
// 内部以倍增段复制避免 O(length) 次调用。
func CopyBack(dst []byte, dstOff int, srcBase uint64, dist, length int,
	at func(pos uint64) byte) int {
	pos := dstOff
	end := pos + length
	period := dist
	if period > length {
		period = length
	}
	for i := 0; i < period; i++ {
		dst[pos+i] = at(srcBase + uint64(i))
	}
	pos += period
	for pos < end {
		k := pos - dstOff
		if k > end-pos {
			k = end - pos
		}
		copy(dst[pos:pos+k], dst[pos-dist:pos-dist+k])
		pos += k
	}
	return length
}

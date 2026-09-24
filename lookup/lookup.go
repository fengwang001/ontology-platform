// Package lookup 在只读字典上按序号取值、按值二分查找。
// 二分只能落在重启点上：非重启点条目脱离前驱无法独立解码。
package lookup

import (
	"errors"
	"strings"
	"sync/atomic"

	"ontology/block"
	"ontology/dict"
)

var ErrIndex = errors.New("lookup: index out of range")

var (
	decodeN  atomic.Int64
	compareN atomic.Int64
)

// ResetCounters 清零解压/比较计数器（非导出计数器的导出入口）。
func ResetCounters() { decodeN.Store(0); compareN.Store(0) }

// Counters 返回累计解压条数与字符串比较次数。
func Counters() (decodes, compares int64) { return decodeN.Load(), compareN.Load() }

// Get 返回全局第 idx 条；块内从重启点起解压，解压次数 <= K。
func Get(d *dict.Dict, idx int) (string, error) {
	if idx < 0 || idx >= d.Count {
		return "", ErrIndex
	}
	s, n, err := d.Entry(idx)
	decodeN.Add(int64(n))
	return s, err
}

// Find 返回 s 的序号；未命中时返回应插入位置，found=false。
func Find(d *dict.Dict, s string) (idx int, found bool) {
	lo, hi := 0, d.NumBlocks()
	for lo < hi { // 块级二分：第一个末值 >= s 的块
		mid := int(uint(lo+hi) >> 1)
		compareN.Add(1)
		if d.BlockLast(mid) < s {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	if lo == d.NumBlocks() {
		return d.Count, false
	}
	inBlock, found := searchBlock(d, lo, s)
	return lo*d.BlockSize + inBlock, found
}

// searchBlock 先在重启点数组上二分定位候选区间，再在区间内顺序比较。
func searchBlock(d *dict.Dict, b int, s string) (int, bool) {
	data := d.BlockData(b)
	k, count := d.K, d.BlockCount(b)
	nR := (count + k - 1) / k
	lo, hi := -1, nR-1
	for lo < hi { // 最后一个值 <= s 的重启点
		mid := int(uint(lo+hi+1) >> 1)
		rv, _, err := block.DecodeEntry(data, mid*k)
		if err != nil {
			return 0, false
		}
		decodeN.Add(1)
		compareN.Add(1)
		if rv <= s {
			lo = mid
		} else {
			hi = mid - 1
		}
	}
	start := 0
	if lo > 0 {
		start = lo * k
	}
	entries, err := block.DecodeN(data, start, k)
	if err != nil {
		return 0, false
	}
	decodeN.Add(int64(len(entries)))
	for i, e := range entries {
		compareN.Add(1)
		switch c := strings.Compare(e, s); {
		case c == 0:
			return start + i, true
		case c > 0:
			return start + i, false
		}
	}
	return start + len(entries), false
}

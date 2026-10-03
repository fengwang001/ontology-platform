package sparsebitset

import "sort"

// arrayThreshold 是数组表示允许的最大基数。
// 基数 <= 4096 的容器必须是有序数组，基数 > 4096 的容器必须是位图。
const arrayThreshold = 4096

// bitmapWords 是一个位图容器包含的 uint64 字数（1024 * 64 = 65536 位）。
const bitmapWords = 1024

// container 是稀疏位集中高 16 位相同的一段元素的容器，
// 以有序数组或位图两种表示之一存储低 16 位值。
// bitmap 为 nil 时是数组表示，非 nil（长度恒为 bitmapWords）时是位图表示。
type container struct {
	arr    []uint16
	bitmap []uint64
	card   int
}

func newArrayContainer() *container {
	return &container{arr: make([]uint16, 0, 8)}
}

func newBitmapContainer() *container {
	return &container{bitmap: make([]uint64, bitmapWords)}
}

func (c *container) isBitmap() bool {
	return c.bitmap != nil
}

// normalize 按基数把容器原地规范化为唯一确定的表示：
// 基数 <= 4096 为数组，基数 > 4096 为位图。
// 基数为 0 的容器由调用方从集合中移除，这里保持不变。
func (c *container) normalize() {
	switch {
	case c.card == 0:
	case c.card <= arrayThreshold && c.isBitmap():
		arr := make([]uint16, 0, c.card)
		for w := 0; w < bitmapWords; w++ {
			word := c.bitmap[w]
			for word != 0 {
				arr = append(arr, uint16(w<<6|trailingZeros64(word)))
				word &= word - 1
			}
		}
		c.arr, c.bitmap = arr, nil
	case c.card > arrayThreshold && !c.isBitmap():
		bm := make([]uint64, bitmapWords)
		for _, v := range c.arr {
			bm[v>>6] |= 1 << (v & 63)
		}
		c.arr, c.bitmap = nil, bm
	}
}

// contains 报告值 v 是否在容器内。
func (c *container) contains(v uint16) bool {
	if c.isBitmap() {
		return c.bitmap[v>>6]&(1<<(v&63)) != 0
	}
	i := sort.Search(len(c.arr), func(i int) bool { return c.arr[i] >= v })
	return i < len(c.arr) && c.arr[i] == v
}

// add 向容器加入值 v，返回基数是否增加。
func (c *container) add(v uint16) bool {
	if c.isBitmap() {
		mask := uint64(1) << (v & 63)
		if c.bitmap[v>>6]&mask != 0 {
			return false
		}
		c.bitmap[v>>6] |= mask
		c.card++
		return true
	}
	i := sort.Search(len(c.arr), func(i int) bool { return c.arr[i] >= v })
	if i < len(c.arr) && c.arr[i] == v {
		return false
	}
	c.arr = append(c.arr, 0)
	copy(c.arr[i+1:], c.arr[i:])
	c.arr[i] = v
	c.card++
	return true
}

// remove 从容器删除值 v，返回基数是否减少。
func (c *container) remove(v uint16) bool {
	if c.isBitmap() {
		mask := uint64(1) << (v & 63)
		if c.bitmap[v>>6]&mask == 0 {
			return false
		}
		c.bitmap[v>>6] &^= mask
		c.card--
		return true
	}
	i := sort.Search(len(c.arr), func(i int) bool { return c.arr[i] >= v })
	if i >= len(c.arr) || c.arr[i] != v {
		return false
	}
	copy(c.arr[i:], c.arr[i+1:])
	c.arr = c.arr[:len(c.arr)-1]
	c.card--
	return true
}

// fillRange 向容器加入半开区间 [lo, hi) 内全部低 16 位值，
// hi 可取到 1<<16。调用方保证 lo < hi。完成后容器已规范化。
func (c *container) fillRange(lo uint16, hi uint32) {
	// 先算合并后的基数，据此一次性决定最终表示。
	merged := c.card - c.countRange(lo, hi) + int(hi-uint32(lo))
	if merged > arrayThreshold {
		if !c.isBitmap() {
			c.card = merged
			c.normalize() // 数组转位图，已有点来自原数组
		}
		c.bitmapFillRange(lo, hi)
		c.card = merged
		return
	}
	if c.isBitmap() {
		c.bitmapFillRange(lo, hi)
		c.card = merged
		c.normalize() // 基数降回阈值内，位图转数组
		return
	}
	c.arrFillRange(lo, hi, merged)
}

// countRange 返回容器内落在半开区间 [lo, hi) 中的元素个数。
func (c *container) countRange(lo uint16, hi uint32) int {
	if c.isBitmap() {
		n := 0
		firstWord, lastWord := int(lo>>6), int((hi-1)>>6)
		firstMask := ^uint64(0) << (lo & 63)
		lastMask := ^uint64(0) >> (63 - ((hi - 1) & 63))
		if firstWord == lastWord {
			return popcount64(c.bitmap[firstWord] & firstMask & lastMask)
		}
		n += popcount64(c.bitmap[firstWord] & firstMask)
		for w := firstWord + 1; w < lastWord; w++ {
			n += popcount64(c.bitmap[w])
		}
		return n + popcount64(c.bitmap[lastWord]&lastMask)
	}
	start := sort.Search(len(c.arr), func(i int) bool { return c.arr[i] >= lo })
	end := sort.Search(len(c.arr), func(i int) bool { return uint32(c.arr[i]) >= hi })
	return end - start
}

// bitmapFillRange 在位图上置位半开区间 [lo, hi)，不维护基数。
func (c *container) bitmapFillRange(lo uint16, hi uint32) {
	firstWord, lastWord := int(lo>>6), int((hi-1)>>6)
	firstMask := ^uint64(0) << (lo & 63)
	lastMask := ^uint64(0) >> (63 - ((hi - 1) & 63))
	if firstWord == lastWord {
		c.bitmap[firstWord] |= firstMask & lastMask
		return
	}
	c.bitmap[firstWord] |= firstMask
	for w := firstWord + 1; w < lastWord; w++ {
		c.bitmap[w] = ^uint64(0)
	}
	c.bitmap[lastWord] |= lastMask
}

// arrFillRange 在数组表示上并入半开区间 [lo, hi)，merged 为合并后的基数。
func (c *container) arrFillRange(lo uint16, hi uint32, merged int) {
	start := sort.Search(len(c.arr), func(i int) bool { return c.arr[i] >= lo })
	end := sort.Search(len(c.arr), func(i int) bool { return uint32(c.arr[i]) >= hi })
	out := make([]uint16, 0, merged)
	out = append(out, c.arr[:start]...)
	for v := uint32(lo); v < hi; v++ {
		out = append(out, uint16(v))
	}
	out = append(out, c.arr[end:]...)
	c.arr, c.card = out, merged
}

// rank 返回容器内不大于 v 的元素个数。
func (c *container) rank(v uint16) int {
	if c.isBitmap() {
		w := int(v >> 6)
		n := 0
		for i := 0; i < w; i++ {
			n += popcount64(c.bitmap[i])
		}
		return n + popcount64(c.bitmap[w]<<(63-(v&63)))
	}
	return sort.Search(len(c.arr), func(i int) bool { return c.arr[i] > v })
}

// selectAt 返回容器内第 k 小（从 0 起）的值，k 必须小于基数。
func (c *container) selectAt(k int) uint16 {
	if c.isBitmap() {
		for w := 0; w < bitmapWords; w++ {
			word := c.bitmap[w]
			cnt := popcount64(word)
			if k < cnt {
				for ; k > 0; k-- {
					word &= word - 1
				}
				return uint16(w<<6 | trailingZeros64(word))
			}
			k -= cnt
		}
		return 0
	}
	return c.arr[k]
}

// intersect 返回两个容器交集规范化后的容器，交为空时返回 nil。
func intersect(a, b *container) *container {
	var out *container
	switch {
	case a.isBitmap() && b.isBitmap():
		out = newBitmapContainer()
		for w := 0; w < bitmapWords; w++ {
			word := a.bitmap[w] & b.bitmap[w]
			out.bitmap[w] = word
			out.card += popcount64(word)
		}
	case a.isBitmap():
		out = intersectArrayBitmap(b, a)
	default:
		out = intersectArrayBitmap(a, b)
	}
	if out.card == 0 {
		return nil
	}
	out.normalize()
	return out
}

// intersectArrayBitmap 计算数组容器 a 与容器 b（数组或位图）的交集。
func intersectArrayBitmap(a, b *container) *container {
	out := newArrayContainer()
	if b.isBitmap() {
		for _, v := range a.arr {
			if b.bitmap[v>>6]&(1<<(v&63)) != 0 {
				out.arr = append(out.arr, v)
			}
		}
	} else {
		i, j := 0, 0
		for i < len(a.arr) && j < len(b.arr) {
			switch {
			case a.arr[i] < b.arr[j]:
				i++
			case a.arr[i] > b.arr[j]:
				j++
			default:
				out.arr = append(out.arr, a.arr[i])
				i++
				j++
			}
		}
	}
	out.card = len(out.arr)
	return out
}

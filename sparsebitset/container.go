package sparsebitset

import (
	"math/bits"
	"sort"
)

// container holds the values sharing one high-16-bit key, either as a
// sorted array (cardinality <= arrayMaxCardinality) or as a bitmap of
// bitmapWords uint64 words (cardinality > arrayMaxCardinality).
type container struct {
	array  []uint16
	bitmap []uint64
	card   int
}

func (c *container) isBitmap() bool { return c.bitmap != nil }

func (c *container) contains(v uint16) bool {
	if c.isBitmap() {
		return c.bitmap[v>>6]&(uint64(1)<<(v&63)) != 0
	}
	i := sort.Search(len(c.array), func(i int) bool { return c.array[i] >= v })
	return i < len(c.array) && c.array[i] == v
}

// add inserts v and reports whether the element was new. An array
// container whose cardinality reaches arrayMaxCardinality+1 is converted
// to a bitmap.
func (c *container) add(v uint16) bool {
	if c.isBitmap() {
		mask := uint64(1) << (v & 63)
		word := &c.bitmap[v>>6]
		if *word&mask != 0 {
			return false
		}
		*word |= mask
		c.card++
		return true
	}
	i := sort.Search(len(c.array), func(i int) bool { return c.array[i] >= v })
	if i < len(c.array) && c.array[i] == v {
		return false
	}
	c.array = append(c.array, 0)
	copy(c.array[i+1:], c.array[i:])
	c.array[i] = v
	c.card++
	if c.card > arrayMaxCardinality {
		c.toBitmap()
	}
	return true
}

// remove deletes v and reports whether it was present. A bitmap container
// whose cardinality drops to arrayMaxCardinality is converted to an array.
func (c *container) remove(v uint16) bool {
	if c.isBitmap() {
		mask := uint64(1) << (v & 63)
		word := &c.bitmap[v>>6]
		if *word&mask == 0 {
			return false
		}
		*word &^= mask
		c.card--
		if c.card <= arrayMaxCardinality {
			c.toArray()
		}
		return true
	}
	i := sort.Search(len(c.array), func(i int) bool { return c.array[i] >= v })
	if i == len(c.array) || c.array[i] != v {
		return false
	}
	c.array = append(c.array[:i], c.array[i+1:]...)
	c.card--
	return true
}

// addRange inserts every value in [lo, hi), where hi <= 1<<16, and then
// normalizes the representation so it depends only on the final
// cardinality.
func (c *container) addRange(lo, hi uint32) {
	if lo >= hi {
		return
	}
	if !c.isBitmap() && c.card+int(hi-lo) > arrayMaxCardinality {
		c.toBitmap()
	}
	if c.isBitmap() {
		setBits(c.bitmap, lo, hi)
		c.card = popcount(c.bitmap)
		if c.card <= arrayMaxCardinality {
			c.toArray()
		}
		return
	}
	merged := make([]uint16, 0, c.card+int(hi-lo))
	i, v := 0, lo
	for i < len(c.array) && v < hi {
		av := uint32(c.array[i])
		switch {
		case av < v:
			merged = append(merged, c.array[i])
			i++
		case av == v:
			merged = append(merged, c.array[i])
			i++
			v++
		default:
			merged = append(merged, uint16(v))
			v++
		}
	}
	merged = append(merged, c.array[i:]...)
	for ; v < hi; v++ {
		merged = append(merged, uint16(v))
	}
	c.array = merged
	c.card = len(merged)
}

// rank returns the number of elements <= v.
func (c *container) rank(v uint16) int {
	if c.isBitmap() {
		word := v >> 6
		n := 0
		for w := uint16(0); w < word; w++ {
			n += bits.OnesCount64(c.bitmap[w])
		}
		n += bits.OnesCount64(c.bitmap[word] & (^(uint64(0)) >> (63 - (v & 63))))
		return n
	}
	return sort.Search(len(c.array), func(i int) bool { return c.array[i] > v })
}

// selectK returns the k-th smallest element (0-based); k must be valid.
func (c *container) selectK(k int) uint16 {
	if c.isBitmap() {
		for w := 0; w < bitmapWords; w++ {
			word := c.bitmap[w]
			n := bits.OnesCount64(word)
			if k >= n {
				k -= n
				continue
			}
			for b := 0; b < 64; b++ {
				if word&(uint64(1)<<b) != 0 {
					if k == 0 {
						return uint16(w<<6 | b)
					}
					k--
				}
			}
		}
		return 0
	}
	return c.array[k]
}

func (c *container) toBitmap() {
	bitmap := make([]uint64, bitmapWords)
	for _, v := range c.array {
		bitmap[v>>6] |= uint64(1) << (v & 63)
	}
	c.array = nil
	c.bitmap = bitmap
}

func (c *container) toArray() {
	array := make([]uint16, 0, c.card)
	for w := 0; w < bitmapWords; w++ {
		word := c.bitmap[w]
		for word != 0 {
			b := bits.TrailingZeros64(word)
			array = append(array, uint16(w<<6|b))
			word &= word - 1
		}
	}
	c.bitmap = nil
	c.array = array
}

// andContainers intersects two containers and returns a normalized result
// container, or nil when the intersection is empty.
func andContainers(a, b *container) *container {
	switch {
	case !a.isBitmap() && !b.isBitmap():
		merged := make([]uint16, 0, min(len(a.array), len(b.array)))
		i, j := 0, 0
		for i < len(a.array) && j < len(b.array) {
			switch {
			case a.array[i] < b.array[j]:
				i++
			case a.array[i] > b.array[j]:
				j++
			default:
				merged = append(merged, a.array[i])
				i++
				j++
			}
		}
		if len(merged) == 0 {
			return nil
		}
		return &container{array: merged, card: len(merged)}
	case a.isBitmap() && b.isBitmap():
		bitmap := make([]uint64, bitmapWords)
		card := 0
		for w := 0; w < bitmapWords; w++ {
			bitmap[w] = a.bitmap[w] & b.bitmap[w]
			card += bits.OnesCount64(bitmap[w])
		}
		return normalize(&container{bitmap: bitmap, card: card})
	default:
		arr, bmp := a, b
		if arr.isBitmap() {
			arr, bmp = b, a
		}
		merged := make([]uint16, 0, len(arr.array))
		for _, v := range arr.array {
			if bmp.bitmap[v>>6]&(uint64(1)<<(v&63)) != 0 {
				merged = append(merged, v)
			}
		}
		if len(merged) == 0 {
			return nil
		}
		return &container{array: merged, card: len(merged)}
	}
}

// normalize enforces the representation invariant: nil when empty, array
// when card <= arrayMaxCardinality, bitmap otherwise.
func normalize(c *container) *container {
	if c.card == 0 {
		return nil
	}
	if c.isBitmap() && c.card <= arrayMaxCardinality {
		c.toArray()
	}
	return c
}

// setBits sets every bit in [lo, hi) where hi <= 1<<16 and lo < hi.
func setBits(bitmap []uint64, lo, hi uint32) {
	wlo, whi := lo>>6, (hi-1)>>6
	maskLo := ^uint64(0) << (lo & 63)
	maskHi := ^uint64(0) >> (63 - ((hi - 1) & 63))
	if wlo == whi {
		bitmap[wlo] |= maskLo & maskHi
		return
	}
	bitmap[wlo] |= maskLo
	for w := wlo + 1; w < whi; w++ {
		bitmap[w] = ^uint64(0)
	}
	bitmap[whi] |= maskHi
}

func popcount(bitmap []uint64) int {
	n := 0
	for _, word := range bitmap {
		n += bits.OnesCount64(word)
	}
	return n
}

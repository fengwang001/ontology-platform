// Package ontology implements an adaptive sparse bitset keyed by the high
// 16 bits of a uint32 value.
package ontology

// arrayThreshold is the inclusive maximum cardinality for an array container.
// A container with exactly arrayThreshold elements must remain an array; an
// array grows into a bitmap only when it reaches arrayThreshold+1.
const arrayThreshold = 4096

// bitmapWords is the number of uint64 words covering all 2^16 low values.
const bitmapWords = 1024

// container holds the elements of one high-16-bit key. Exactly one of vals
// (sorted, unique) or bits is non-nil depending on the canonical representation.
type container struct {
	vals []uint16
	bits []uint64
	card int
}

const kindArray = "array"
const kindBitmap = "bitmap"

// newArrayContainer takes ownership of vals, which must be sorted, unique and
// contain at most arrayThreshold elements.
func newArrayContainer(vals []uint16) *container {
	return &container{vals: vals, card: len(vals)}
}

func (c *container) isBitmap() bool { return c.bits != nil }

// normalize converts a bitmap container back to an array container once its
// cardinality is at most arrayThreshold. Array containers are left untouched,
// and empty containers are the caller's responsibility to drop.
func (c *container) normalize() *container {
	if c.isBitmap() && c.card <= arrayThreshold {
		vals := make([]uint16, 0, c.card)
		for wordIdx, word := range c.bits {
			for word != 0 {
				bit := word & (^word + 1)
				offset := trailingOnes(bit)
				vals = append(vals, uint16(wordIdx*64+offset))
				word &= word - 1
			}
		}
		return &container{vals: vals, card: len(vals)}
	}
	return c
}

func trailingOnes(x uint64) int {
	n := 0
	for x&1 == 0 {
		x >>= 1
		n++
	}
	return n
}

func (c *container) toBitmap() *container {
	bits := make([]uint64, bitmapWords)
	for _, v := range c.vals {
		bits[v>>6] |= 1 << (v & 63)
	}
	return &container{bits: bits, card: len(c.vals)}
}

func (c *container) kind() string {
	if c.isBitmap() {
		return kindBitmap
	}
	return kindArray
}

func (c *container) contains(v uint16) bool {
	if c.isBitmap() {
		return c.bits[v>>6]&(1<<(v&63)) != 0
	}
	_, found := searchUint16(c.vals, v)
	return found
}

// searchUint16 returns the first index i with vals[i] >= v and whether vals[i] == v.
func searchUint16(vals []uint16, v uint16) (int, bool) {
	lo, hi := 0, len(vals)
	for lo < hi {
		mid := int(uint(lo+hi) >> 1)
		if vals[mid] < v {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	return lo, lo < len(vals) && vals[lo] == v
}

// add inserts v and returns the resulting canonical container (which may be c
// itself converted in place, or a newly allocated bitmap).
func (c *container) add(v uint16) *container {
	if c.isBitmap() {
		idx := v >> 6
		mask := uint64(1) << (v & 63)
		if c.bits[idx]&mask == 0 {
			c.bits[idx] |= mask
			c.card++
		}
		return c
	}
	idx, found := searchUint16(c.vals, v)
	if found {
		return c
	}
	c.vals = append(c.vals, 0)
	copy(c.vals[idx+1:], c.vals[idx:])
	c.vals[idx] = v
	c.card++
	if c.card > arrayThreshold {
		return c.toBitmap()
	}
	return c
}

// remove deletes v. The caller discards containers that become empty.
func (c *container) remove(v uint16) *container {
	if c.isBitmap() {
		idx := v >> 6
		mask := uint64(1) << (v & 63)
		if c.bits[idx]&mask != 0 {
			c.bits[idx] &^= mask
			c.card--
		}
		return c.normalize()
	}
	idx, found := searchUint16(c.vals, v)
	if !found {
		return c
	}
	copy(c.vals[idx:], c.vals[idx+1:])
	c.vals = c.vals[:len(c.vals)-1]
	c.card--
	return c
}

// rank returns the number of elements <= v.
func (c *container) rank(v uint16) int {
	if c.isBitmap() {
		last := int(v >> 6)
		n := 0
		for i := 0; i < last; i++ {
			n += popCount(c.bits[i])
		}
		word := c.bits[last]
		mask := uint64(1)<<((v&63)+1) - 1
		n += popCount(word & mask)
		return n
	}
	idx, found := searchUint16(c.vals, v)
	if found {
		return idx + 1
	}
	return idx
}

func popCount(x uint64) int {
	x = x - ((x >> 1) & 0x5555555555555555)
	x = (x & 0x3333333333333333) + ((x >> 2) & 0x3333333333333333)
	x = (x + (x >> 4)) & 0x0f0f0f0f0f0f0f0f
	return int((x * 0x0101010101010101) >> 56)
}

// selectNth returns the k-th (0-based) smallest element. k must be < card.
func (c *container) selectNth(k int) uint16 {
	if c.isBitmap() {
		for wordIdx, word := range c.bits {
			n := popCount(word)
			if k < n {
				for j := 0; j < k; j++ {
					word &= word - 1
				}
				lowest := word & (^word + 1)
				return uint16(wordIdx*64 + trailingOnes(lowest))
			}
			k -= n
		}
		panic("ontology: selectNth out of range")
	}
	return c.vals[k]
}

// andContainer intersects two containers, returning nil when the result is empty.
func andContainer(a, b *container) *container {
	switch {
	case !a.isBitmap() && !b.isBitmap():
		vals := make([]uint16, 0, minInt(len(a.vals), len(b.vals)))
		i, j := 0, 0
		for i < len(a.vals) && j < len(b.vals) {
			switch {
			case a.vals[i] == b.vals[j]:
				vals = append(vals, a.vals[i])
				i++
				j++
			case a.vals[i] < b.vals[j]:
				i++
			default:
				j++
			}
		}
		if len(vals) == 0 {
			return nil
		}
		return newArrayContainer(vals)
	case a.isBitmap() && b.isBitmap():
		bits := make([]uint64, bitmapWords)
		card := 0
		for i := range bits {
			w := a.bits[i] & b.bits[i]
			bits[i] = w
			card += popCount(w)
		}
		if card == 0 {
			return nil
		}
		return (&container{bits: bits, card: card}).normalize()
	default:
		arr, bmp := a, b
		if a.isBitmap() {
			arr, bmp = b, a
		}
		vals := make([]uint16, 0, len(arr.vals))
		for _, v := range arr.vals {
			if bmp.bits[v>>6]&(1<<(v&63)) != 0 {
				vals = append(vals, v)
			}
		}
		if len(vals) == 0 {
			return nil
		}
		return newArrayContainer(vals)
	}
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// addLowRange unions all values in [lo, hi) (both within [0, 2^16)).
func (c *container) addLowRange(lo, hi uint32) *container {
	if lo >= hi {
		return c
	}
	if c.isBitmap() {
		fillRangeBits(c.bits, lo, hi)
		c.card = 0
		for _, w := range c.bits {
			c.card += popCount(w)
		}
		return c
	}
	rangeVals := make([]uint16, 0, hi-lo)
	n := hi - lo
	v := lo
	for i := uint32(0); i < n; i++ {
		rangeVals = append(rangeVals, uint16(v))
		v++
	}
	merged := mergeUnique(c.vals, rangeVals)
	res := newArrayContainer(merged)
	if res.card > arrayThreshold {
		return res.toBitmap()
	}
	return res
}

// fillRangeBits sets every bit with index in [lo, hi) on a 65536-bit bitmap.
func fillRangeBits(bits []uint64, lo, hi uint32) {
	startWord := lo >> 6
	endWord := (hi - 1) >> 6
	for w := startWord; w <= endWord; w++ {
		wordLo := uint32(0)
		if w == startWord {
			wordLo = lo & 63
		}
		wordHi := uint32(64)
		if w == endWord {
			wordHi = (hi - 1) & 63
		}
		highMask := ^uint64(0)
		if wordHi < 64 {
			highMask = ^uint64(0) >> (63 - wordHi)
		}
		mask := (^uint64(0) << wordLo) & highMask
		bits[w] |= mask
	}
}

// mergeUnique unions two sorted, unique uint16 slices into a new sorted slice.
func mergeUnique(a, b []uint16) []uint16 {
	out := make([]uint16, 0, len(a)+len(b))
	i, j := 0, 0
	for i < len(a) && j < len(b) {
		switch {
		case a[i] == b[j]:
			out = append(out, a[i])
			i++
			j++
		case a[i] < b[j]:
			out = append(out, a[i])
			i++
		default:
			out = append(out, b[j])
			j++
		}
	}
	out = append(out, a[i:]...)
	out = append(out, b[j:]...)
	return out
}

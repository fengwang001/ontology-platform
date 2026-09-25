// Package slab provides alignment, per-slab packing and offset<->(slab,slot)
// conversions for a fixed-size object cache. It depends on nothing.
package slab

// AlignUp returns the smallest multiple of align that is >= raw.
// align must be a power of two; raw must be positive (validated by caller).
func AlignUp(raw, align int) int {
	return (raw + align - 1) &^ (align - 1)
}

// IsPow2 reports whether n is a positive power of two.
func IsPow2(n int) bool {
	return n > 0 && n&(n-1) == 0
}

// PerSlab returns how many objects of aligned size sz fit in one slab of
// slabSize bytes (floor division). The remainder is permanent tail waste.
func PerSlab(slabSize, sz int) int {
	return slabSize / sz
}

// Waste returns the never-allocated tail bytes of each slab.
func Waste(slabSize, sz int) int {
	return slabSize - PerSlab(slabSize, sz)*sz
}

// Offset returns the byte offset of slot i in slab j.
func Offset(j, i, slabSize, sz int) int {
	return j*slabSize + i*sz
}

// Locate converts a byte offset to its slab index and slot index.
func Locate(off, slabSize, sz int) (j, i int) {
	return off / slabSize, (off % slabSize) / sz
}

// IntHeap is a min-heap of ints (container/heap.Interface), used by the
// cache for free-slot sets and per-state slab sets.
type IntHeap []int

func (h IntHeap) Len() int           { return len(h) }
func (h IntHeap) Less(i, j int) bool { return h[i] < h[j] }
func (h IntHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }
func (h *IntHeap) Push(x any)        { *h = append(*h, x.(int)) }
func (h *IntHeap) Pop() any          { n := len(*h) - 1; v := (*h)[n]; *h = (*h)[:n]; return v }

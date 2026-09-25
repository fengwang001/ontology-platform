// Package slab holds the pure packing math: alignment, per-slot packing and
// byte-offset <-> (slab index, slot index) conversion. It depends on nothing.
package slab

// IsPowerOfTwo reports whether n is a positive power of two.
func IsPowerOfTwo(n int) bool { return n > 0 && n&(n-1) == 0 }

// AlignUp returns the smallest multiple of align (a power of two) that is not
// smaller than n. When n is already a multiple of align it is returned
// unchanged: AlignUp(8,8) == 8.
func AlignUp(n, align int) int { return (n + align - 1) &^ (align - 1) }

// Geometry is the immutable packing geometry shared by every slab of a cache.
type Geometry struct {
	Size     int // aligned object size sz
	PerSlab  int // floor(SlabSize / Size); tail waste is never handed out
	SlabSize int
}

// Pack derives the geometry from already validated inputs (rawSize > 0,
// align a power of two). PerSlab is floor division, never rounded up.
func Pack(rawSize, align, slabSize int) Geometry {
	sz := AlignUp(rawSize, align)
	return Geometry{Size: sz, PerSlab: slabSize / sz, SlabSize: slabSize}
}

// Waste is the never-allocated tail of each slab, in bytes.
func (g Geometry) Waste() int { return g.SlabSize - g.PerSlab*g.Size }

// Offset is the byte offset of slot i inside slab j: j*SlabSize + i*Size.
func (g Geometry) Offset(j, i int) int { return j*g.SlabSize + i*g.Size }

// Split is the inverse of Offset for non-negative off: it returns the owning
// slab index and the slot index inside that slab. The caller decides whether
// the slot falls inside the packed region or the tail-waste gap.
func (g Geometry) Split(off int) (j, i int) {
	return off / g.SlabSize, (off % g.SlabSize) / g.Size
}

// Contains reports whether the object at off is legally packed: its in-slot
// offset lies on the Size grid and [off, off+Size) stays fully inside its
// slab (never in tail waste). Slabs restart the grid, so with a SlabSize that
// is not a multiple of Size the absolute offset of a later slab (e.g. 60 with
// Size 16) is not itself a multiple of Size; the within-slab position is.
func (g Geometry) Contains(off int) bool {
	if off < 0 {
		return false
	}
	in := off % g.SlabSize
	return in%g.Size == 0 && in+g.Size <= g.SlabSize
}

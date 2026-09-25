// Package arena holds the pure arithmetic of linear (bump) allocation:
// alignment rounding, power-of-two checks, global-offset math and the
// decision of whether an aligned object fits in the current arena.
// It depends on no other package.
package arena

// IsPowerOfTwo reports whether a is positive and a power of two
// (1, 2, 4, 8, ...). align=0 and negative align are rejected.
func IsPowerOfTwo(a int) bool {
	return a >= 1 && a&(a-1) == 0
}

// AlignUp rounds n up to the next multiple of align.
// align must be a power of two; callers validate that beforehand.
// For n >= 1 the result is always >= align.
func AlignUp(n, align int) int {
	return (n + align - 1) &^ (align - 1)
}

// GlobalOff converts an arena index j and a local bump position inside it
// into the global byte offset. Arena j occupies the global byte interval
// [j*arenaSize, (j+1)*arenaSize).
func GlobalOff(j, bump, arenaSize int) int {
	return j*arenaSize + bump
}

// IndexOf returns the arena index that owns global offset off.
func IndexOf(off, arenaSize int) int {
	return off / arenaSize
}

// Fits reports whether an aligned object of size sz can be served from an
// arena whose bump is at bump: the remaining tail (arenaSize-bump) must be
// at least sz. Objects never straddle an arena boundary, so a miss means the
// caller moves to the next arena and abandons the tail bytes.
func Fits(bump, sz, arenaSize int) bool {
	return arenaSize-bump >= sz
}

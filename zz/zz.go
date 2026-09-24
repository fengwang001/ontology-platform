// Package zz maps signed integers to and from unsigned integers using the
// zigzag scheme: 0,-1,1,-2,2,... become 0,1,2,3,4,...
// It depends on no other package in this module.
package zz

// Zig maps an int64 to a uint64.
// Small non-negative and small negative values both map to small unsigned
// values, so they get short varint encodings.
func Zig(v int64) uint64 {
	return uint64(v)<<1 ^ uint64(v>>63)
}

// Unzig is the inverse of Zig.
func Unzig(u uint64) int64 {
	return int64(u>>1) ^ -int64(u&1)
}

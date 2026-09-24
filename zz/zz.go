// Package zz maps between signed and unsigned integers (zigzag).
package zz

// Encode maps int64 to uint64: 0,-1,1,-2,2,... -> 0,1,2,3,4,...
// Small magnitudes stay small, so varint encodings stay short.
func Encode(v int64) uint64 {
	return uint64(v<<1) ^ uint64(v>>63)
}

// Decode is the exact inverse of Encode.
func Decode(u uint64) int64 {
	return int64(u>>1) ^ -int64(u&1)
}

// Package zz provides zigzag mapping between signed and unsigned integers.
package zz

// Encode maps a signed integer to unsigned: 0,-1,1,-2,... -> 0,1,2,3,...
func Encode(v int64) uint64 { return uint64(v<<1) ^ uint64(v>>63) }

// Decode is the exact inverse of Encode.
func Decode(u uint64) int64 { return int64(u>>1) ^ -int64(u&1) }

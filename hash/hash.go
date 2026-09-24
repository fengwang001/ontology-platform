// Package hash 提供布谷鸟过滤器的指纹与候选桶定位函数，不依赖其他包。
package hash

import "errors"

// ErrInvalidParams 表示构造参数非法：numBuckets 非 2 的幂或 < 2，
// 或 entriesPerBucket < 1，或 maxKicks < 1。
var ErrInvalidParams = errors.New("hash: invalid parameters")

// ValidParams 校验构造参数：numBuckets 是 2 的幂且 >= 2，
// entriesPerBucket >= 1，maxKicks >= 1。
func ValidParams(numBuckets, entriesPerBucket, maxKicks int) bool {
	return numBuckets >= 2 && numBuckets&(numBuckets-1) == 0 &&
		entriesPerBucket >= 1 && maxKicks >= 1
}

// Fingerprint 返回键 x 的指纹 f(x) = 1 + (x mod 7)，取值 1..7；0 表示空槽。
// x 必须非负（调用方负责拒绝负键）。
func Fingerprint(x int64) int {
	return 1 + int(x%7)
}

// Offset 返回指纹 f 的桶偏移 o(f) = (f mod 3) + 1，取值 1,2,3，恒非 0。
func Offset(f int) int {
	return f%3 + 1
}

// I1 返回第一候选桶 i1(x) = x mod numBuckets。
func I1(x int64, numBuckets int) int {
	return int(x % int64(numBuckets))
}

// I2 返回第二候选桶 i2(x) = i1(x) XOR o(f(x))。
// 末尾对 numBuckets 取掩码：numBuckets >= 4 时为恒等（o <= 3 < numBuckets），
// numBuckets = 2 时保证下标不越界；掩码与 XOR 可交换，对称性保持。
func I2(x int64, numBuckets int) int {
	return (I1(x, numBuckets) ^ Offset(Fingerprint(x))) & (numBuckets - 1)
}

// Alternate 返回桶 b 关于指纹 f 的另一个候选桶：b XOR o(f)。
// 满足对称性 alternate(alternate(b, f), f) == b。
func Alternate(b, f int) int {
	return b ^ Offset(f)
}

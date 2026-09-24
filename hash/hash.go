// Package hash 提供布谷鸟过滤器的指纹与候选桶定位函数。
package hash

import "errors"

// ErrInvalidParams 表示构造参数非法（numBuckets 非 2 的幂或 <2，entriesPerBucket<1，maxKicks<1）。
var ErrInvalidParams = errors.New("hash: invalid parameters")

// Validate 校验构造参数：numBuckets 是 2 的幂且 >=2，entriesPerBucket>=1，maxKicks>=1。
func Validate(numBuckets, entriesPerBucket, maxKicks int) error {
	if numBuckets < 2 || numBuckets&(numBuckets-1) != 0 {
		return ErrInvalidParams
	}
	if entriesPerBucket < 1 || maxKicks < 1 {
		return ErrInvalidParams
	}
	return nil
}

// Fingerprint 返回键 x 的指纹 f(x)=1+(x mod 7)，取值 1..7；0 保留为空槽标记。
// 调用方保证 x 非负。
func Fingerprint(x int64) uint8 {
	return uint8(1 + x%7)
}

// Offset 返回指纹 f 的桶偏移 o(f)=(f mod 3)+1，取值 1,2,3，恒非 0。
func Offset(f uint8) int {
	return int(f%3) + 1
}

// First 返回第一候选桶 i1(x)=x mod numBuckets。numBuckets 为 2 的幂。
func First(x int64, numBuckets int) int {
	return int(x & int64(numBuckets-1))
}

// Alternate 返回桶 b 关于指纹 f 的另一个候选桶 b XOR o(f)。
// 对称性：Alternate(Alternate(b,f),f)==b。
func Alternate(b int, f uint8) int {
	return b ^ Offset(f)
}

// Second 返回第二候选桶 i2(x)=i1(x) XOR o(f(x))。
func Second(x int64, numBuckets int) int {
	f := Fingerprint(x)
	return Alternate(First(x, numBuckets), f)
}

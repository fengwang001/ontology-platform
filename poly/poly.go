// Package poly 提供 CRC-32 反射多项式的合法性判定。
package poly

import "errors"

// ErrInvalid 表示多项式非法：为 0 或最高位（bit31）未置位。
var ErrInvalid = errors.New("poly: 非法多项式（为 0 或 bit31 未置位，非反射形式）")

// IEEE 802.3 反射多项式。
const IEEE uint32 = 0xEDB88320

// Reflected 报告 poly 是否为反射形式：反射后的 32 次多项式最高位必为 1。
func Reflected(poly uint32) bool {
	return poly&0x80000000 != 0
}

// Valid 报告 poly 是否合法：非零且为反射形式。
func Valid(poly uint32) bool {
	return poly != 0 && Reflected(poly)
}

// Check 校验 poly，非法时返回 ErrInvalid。
func Check(poly uint32) error {
	if !Valid(poly) {
		return ErrInvalid
	}
	return nil
}

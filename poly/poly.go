// Package poly 判定反射 CRC-32 多项式的合法性、提供位反射。不依赖其他包。
package poly

// IEEE 是 IEEE 802.3 反射多项式（x^32+x^26+x^23+x^22+x^16+x^12+x^11+
// x^10+x^8+x^7+x^5+x^4+x^2+x+1 的反射形式）。
const IEEE = 0xEDB88320

// Valid 报告 p 是否是合法的反射多项式：非零且最高位 bit31 置位。
// 反射算法每轮右移一位，只有 bit31 置位才能保证 32 次移位后余项完整。
func Valid(p uint32) bool {
	return p != 0 && p&0x80000000 != 0
}

// Reflect 返回 32 位字的位反转：把正常（MSB-first）多项式映到它的
// 反射形式，例如 Reflect(0x04C11DB7) == IEEE。
func Reflect(p uint32) uint32 {
	var r uint32
	for i := 0; i < 32; i++ {
		if p&(1<<i) != 0 {
			r |= 1 << (31 - i)
		}
	}
	return r
}

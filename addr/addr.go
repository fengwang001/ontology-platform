// Package addr 由块内容导出稳定的内容地址（SHA-256），并提供相等判定。
//
// 地址是纯函数：相同内容必得到相同地址，不依赖本模块之外的任何包，也不持有状态。
package addr

import (
	"crypto/sha256"
	"encoding/hex"
)

// Size 是地址字节长度（SHA-256 = 32）。
const Size = sha256.Size

// Address 是一块内容的强哈希地址。
type Address [Size]byte

// Of 返回 data 的内容地址。
func Of(data []byte) Address {
	return Address(sha256.Sum256(data))
}

// String 返回小写十六进制表示。
func (a Address) String() string {
	return hex.EncodeToString(a[:])
}

// Equal 判定两个地址是否相同。
func (a Address) Equal(other Address) bool {
	return a == other
}

// IsZero 报告地址是否为零值（未初始化）。
func (a Address) IsZero() bool {
	return a == Address{}
}

// Package addr 由块内容导出稳定的强哈希内容地址。不依赖其他包。
package addr

import (
	"crypto/sha256"
	"encoding/hex"
)

// Size 是地址字节数（SHA-256）。
const Size = sha256.Size

// Addr 是一块内容的强哈希地址。
type Addr [Size]byte

// Of 由块内容计算地址：相同内容必然得到相同地址，不同内容（概率上）不共享。
func Of(data []byte) Addr {
	return Addr(sha256.Sum256(data))
}

// String 返回小写十六进制表示。
func (a Addr) String() string { return hex.EncodeToString(a[:]) }

// Equal 判定两个地址是否相同（内容相等判定）。
func (a Addr) Equal(b Addr) bool { return a == b }

// IsZero 判定是否为零值地址（非任何真实块的地址）。
func (a Addr) IsZero() bool { return a == Addr{} }

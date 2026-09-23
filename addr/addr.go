// Package addr 由块内容导出稳定的强哈希内容地址。
package addr

import (
	"crypto/sha256"
	"encoding/hex"
)

// Size 是地址字节长度（SHA-256）。
const Size = sha256.Size

// Addr 是块的内容地址。
type Addr [Size]byte

// Of 返回内容 p 的地址：SHA-256(p)。相同内容必得相同地址。
func Of(p []byte) Addr {
	return Addr(sha256.Sum256(p))
}

// String 返回十六进制表示。
func (a Addr) String() string { return hex.EncodeToString(a[:]) }

// Equal 报告两个地址是否相同。
func (a Addr) Equal(b Addr) bool { return a == b }

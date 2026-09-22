package digest

import (
	"crypto/sha256"
	"encoding/hex"
)

// Fingerprint 是请求体内容的指纹。
// 相同内容一定得到相同指纹；不同内容（以压倒性概率）得到不同指纹。
type Fingerprint struct {
	sum string
}

// Of 根据请求体计算指纹。nil 与空切片都视为空内容。
func Of(body []byte) Fingerprint {
	sum := sha256.Sum256(body)
	return Fingerprint{sum: hex.EncodeToString(sum[:])}
}

// String 返回指纹的十六进制表示。
func (f Fingerprint) String() string {
	return f.sum
}

// Equal 判定两个指纹是否来自相同内容的请求体。
func (f Fingerprint) Equal(other Fingerprint) bool {
	return f.sum == other.sum
}

// Package verify 做补丁应用结果的最终完整性校验与损坏检测。
package verify

import (
	"crypto/sha256"
	"errors"

	"ontology/diff"
)

// ErrChecksumMismatch 表示应用结果与源端整体强校验和不一致，
// 例如「签名生成后目标端数据被改动」导致的静默错误复用。
var ErrChecksumMismatch = errors.New("verify: result does not match source checksum")

// Final 校验 result 是否与补丁声明的源端一致：先比长度，再比整体 SHA-256。
func Final(result []byte, p diff.Patch) error {
	if len(result) != p.SrcLen {
		return ErrChecksumMismatch
	}
	if sha256.Sum256(result) != p.SrcHash {
		return ErrChecksumMismatch
	}
	return nil
}

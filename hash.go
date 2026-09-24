package ontology

import (
	"crypto/sha256"
	"encoding/binary"
)

// HashFunc 把字符串映射到环上的位置。必须确定性。
type HashFunc func(string) uint64

// defaultHash 取 SHA-256 摘要的前 8 字节（大端）作为环上位置，
// 雪崩效应强，虚拟节点位置均匀，
// 不依赖任何随机种子（不使用 maphash）。
func defaultHash(s string) uint64 {
	sum := sha256.Sum256([]byte(s))
	return binary.BigEndian.Uint64(sum[:8])
}

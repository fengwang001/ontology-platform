// Package manifest 定义导出清单：块列表、总校验和、快照版本，
// 并提供带自校验的二进制编码（MANF 魔数 + JSON + CRC32）。
package manifest

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"hash/crc32"
)

// Magic 标识清单段的开始，与块魔数首字节不同，便于截断分类。
const Magic = "MANF"

// ErrMalformed 表示清单字节无法解析（截断或校验和不符）。
var ErrMalformed = errors.New("manifest: 不完整或校验和不符")

// BlockMeta 描述一个已落盘的块。
type BlockMeta struct {
	Index  int
	Offset int64
	Length int
	CRC    uint32
}

// Manifest 是一次导出的清单。
type Manifest struct {
	Version   uint64 // 快照版本水位
	BlockSize int
	Keys      int
	Total     uint32 // 逐条目 CRC32 的 XOR，与导出顺序无关
	Blocks    []BlockMeta
}

// Encode 编码为 MANF(4) + 长度(4) + JSON + CRC32(JSON)(4)。
func Encode(m *Manifest) []byte {
	j, _ := json.Marshal(m)
	out := make([]byte, 0, len(j)+12)
	out = append(out, Magic...)
	out = binary.BigEndian.AppendUint32(out, uint32(len(j)))
	out = append(out, j...)
	out = binary.BigEndian.AppendUint32(out, crc32.ChecksumIEEE(j))
	return out
}

// Decode 从 b 的开头解析清单，返回清单与消费的字节数。
func Decode(b []byte) (*Manifest, int, error) {
	if len(b) < 8 || string(b[:4]) != Magic {
		return nil, 0, ErrMalformed
	}
	n := int(binary.BigEndian.Uint32(b[4:8]))
	if n < 0 || len(b) < 8+n+4 {
		return nil, 0, ErrMalformed
	}
	j := b[8 : 8+n]
	if crc32.ChecksumIEEE(j) != binary.BigEndian.Uint32(b[8+n:12+n]) {
		return nil, 0, ErrMalformed
	}
	var m Manifest
	if err := json.Unmarshal(j, &m); err != nil {
		return nil, 0, ErrMalformed
	}
	return &m, 12 + n, nil
}

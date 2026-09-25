// Package manifest 定义导出清单：块列表、总校验和与快照版本。
package manifest

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
)

// Block 描述一个数据块（偏移相对数据区起点，即清单行之后）。
type Block struct {
	Index  int   `json:"index"`
	Offset int64 `json:"offset"`
	Length int   `json:"length"`
}

// Manifest 是导出文件的自描述清单（文件首行，JSON + 换行）。
type Manifest struct {
	Version    uint64  `json:"version"`
	KeyCount   int     `json:"key_count"`
	BlockSize  int     `json:"block_size"`
	BlockCount int     `json:"block_count"`
	RootHash   string  `json:"root_hash"` // 全部记录摘要异或后的十六进制
	Blocks     []Block `json:"blocks"`
}

// Encode 把清单编码成单行 JSON 并追加换行。
func Encode(m *Manifest) ([]byte, error) {
	raw, err := json.Marshal(m)
	if err != nil {
		return nil, err
	}
	return append(raw, '\n'), nil
}

// Decode 解析一行清单 JSON。
func Decode(line []byte) (*Manifest, error) {
	m := &Manifest{}
	if err := json.Unmarshal(line, m); err != nil {
		return nil, err
	}
	return m, nil
}

// RecordDigest 计算单条记录对总校验和的贡献：sha256(klen||k||vlen? 不，见下)。
// 摘要输入为 8 字节 klen、键、8 字节 vlen、值；空值与不存在由记录是否出现区分。
func RecordDigest(key string, value []byte) [32]byte {
	h := sha256.New()
	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], uint64(len(key)))
	h.Write(buf[:])
	h.Write([]byte(key))
	binary.BigEndian.PutUint64(buf[:], uint64(len(value)))
	h.Write(buf[:])
	h.Write(value)
	var out [32]byte
	copy(out[:], h.Sum(nil))
	return out
}

// XOR 把 src 逐字节异或进 dst（可交换、可结合，故与导出顺序无关）。
func XOR(dst, src *[32]byte) {
	for i := 0; i < 32; i++ {
		dst[i] ^= src[i]
	}
}

// RootHex 返回根哈希的十六进制字符串。
func RootHash(root *[32]byte) string {
	const hexd = "0123456789abcdef"
	out := make([]byte, 64)
	for i, b := range root {
		out[2*i] = hexd[b>>4]
		out[2*i+1] = hexd[b&0xf]
	}
	return string(out)
}

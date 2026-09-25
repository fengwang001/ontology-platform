package manifest

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"os"
	"path/filepath"
)

// FileName 是清单在导出目录中的文件名。
const FileName = "manifest.json"

// BlockInfo 描述一个已写出的块在分块文件中的位置与 CRC。
type BlockInfo struct {
	Offset int64  `json:"offset"`
	Length int    `json:"length"`
	CRC    uint32 `json:"crc"`
}

// Manifest 是一次导出的清单：版本水位、块表与有序无关总校验和。
type Manifest struct {
	Version   uint64            `json:"version"`
	BlockSize int               `json:"block_size"`
	Records   int               `json:"records"`
	Checksum  []byte            `json:"checksum"` // 逐记录 SHA-256 的 XOR 折叠
	Blocks    map[int]BlockInfo `json:"blocks"`

	folded bool
}

// New 创建空清单；Checksum 初始为 32 字节零，随记录 XOR 折叠。
func New(version uint64, blockSize, records int) Manifest {
	return Manifest{
		Version:   version,
		BlockSize: blockSize,
		Records:   records,
		Checksum:  make([]byte, sha256.Size),
		Blocks:    make(map[int]BlockInfo),
	}
}

// Add 记录一个块。
func (m *Manifest) Add(no, offset, length int, crc uint32) {
	m.Blocks[no] = BlockInfo{Offset: int64(offset), Length: length, CRC: crc}
}

// FoldRecord 把一条规范化记录并入总校验和（XOR，与顺序无关）。
func (m *Manifest) FoldRecord(key string, value []byte) {
	h := sha256.New()
	var buf [binary.MaxVarintLen64]byte
	n := binary.PutUvarint(buf[:], uint64(len(key)))
	h.Write(buf[:n])
	h.Write([]byte(key))
	n = binary.PutUvarint(buf[:], uint64(len(value)))
	h.Write(buf[:n])
	h.Write(value)
	sum := h.Sum(nil)
	for i := range m.Checksum {
		m.Checksum[i] ^= sum[i]
	}
}

// Finish 标记清单可落盘。
func (m *Manifest) Finish() { m.folded = true }

// Complete 报告块号 0..N-1 是否连续齐全。
func (m Manifest) Complete() bool {
	for i := 0; i < len(m.Blocks); i++ {
		if _, ok := m.Blocks[i]; !ok {
			return false
		}
	}
	return m.folded || len(m.Blocks) == 0
}

// Save 把清单写到 dir。
func Save(dir string, m Manifest) error {
	data, err := json.MarshalIndent(m, "", " ")
	if err != nil {
		return err
	}
	tmp := filepath.Join(dir, FileName+".tmp")
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(dir, FileName))
}

// Load 读取清单；文件不存在时返回包装了 os.ErrNotExist 的错误。
func Load(dir string) (Manifest, error) {
	var m Manifest
	data, err := os.ReadFile(filepath.Join(dir, FileName))
	if err != nil {
		return m, err
	}
	if err := json.Unmarshal(data, &m); err != nil {
		return m, err
	}
	if m.Blocks == nil {
		m.Blocks = make(map[int]BlockInfo)
	}
	return m, nil
}

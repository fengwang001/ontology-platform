package manifest

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"os"
)

// 清单区位于文件头部：[4B 长度 LE][JSON][空格填充至 Reserve]。

// Chunk 描述一个已完成块。
type Chunk struct {
	Index    uint32 `json:"index"`
	BodyLen  uint32 `json:"body_len"`
	BodyCRC  uint32 `json:"body_crc"`
}

// Manifest 是导出文件的自描述清单。
type Manifest struct {
	Version    uint64  `json:"version"`
	ChunkSize  int     `json:"chunk_size"`
	Order      string  `json:"order"`
	KeyCount   int     `json:"key_count"`
	TotalBytes int64   `json:"total_bytes"`
	TotalCRC   uint32  `json:"total_crc"`
	Complete   bool    `json:"complete"`
	Reserve    int     `json:"reserve"`
	Chunks     []Chunk `json:"chunks"`
}

const minReserve = 1 << 20

// ReserveSize 按“每条记录至多一个块”的上界预留清单槽位。
func ReserveSize(chunkSize, keyCount int) int {
	const perChunk = 64
	const overhead = 512
	r := overhead + keyCount*perChunk
	if r < minReserve {
		r = minReserve
	}
	return r
}

// RegionSize 返回包含 4 字节长度前缀的清单区总字节数。
func RegionSize(reserve int) int { return 4 + reserve }

// Marshal 把清单编码为恰好 reserve 字节的区段内容。
func (m *Manifest) Marshal(reserve int) ([]byte, error) {
	raw, err := json.Marshal(m)
	if err != nil {
		return nil, err
	}
	if len(raw) > reserve {
		return nil, errors.New("manifest: reserve too small")
	}
	buf := make([]byte, 4+reserve)
	binary.LittleEndian.PutUint32(buf, uint32(len(raw)))
	copy(buf[4:], raw)
	for i := 4 + len(raw); i < len(buf); i++ {
		buf[i] = ' '
	}
	return buf, nil
}

// Unmarshal 从区段内容解码清单。
func Unmarshal(region []byte) (*Manifest, error) {
	if len(region) < 4 {
		return nil, io.ErrUnexpectedEOF
	}
	n := int(binary.LittleEndian.Uint32(region))
	if n <= 0 || 4+n > len(region) {
		return nil, io.ErrUnexpectedEOF
	}
	var m Manifest
	if err := json.Unmarshal(bytes.TrimRight(region[4:4+n], " "), &m); err != nil {
		return nil, err
	}
	return &m, nil
}

// WriteRegion 把清单写回文件头部固定预留区。
func WriteRegion(f *os.File, m *Manifest) error {
	buf, err := m.Marshal(m.Reserve)
	if err != nil {
		return err
	}
	if _, err := f.WriteAt(buf, 0); err != nil {
		return err
	}
	return f.Sync()
}

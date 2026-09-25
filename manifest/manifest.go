// Package manifest 定义导出清单及其在导出文件头部的帧格式。
package manifest

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"hash/crc32"
)

// ErrFrame 表示清单帧不完整或自身校验失败。
var ErrFrame = errors.New("manifest: 清单帧不完整或校验失败")

// 头部帧布局：[magic 4B][jsonLen 4B][json][crc32(json) 4B]。
const (
	headerLen = 8
	crcLen    = 4
	magic     = 0x4d4e4631 // "MNF1"
)

// Chunk 描述一个导出块；Offset 相对块区起点（即清单帧之后）。
type Chunk struct {
	Index  int
	Offset int64
	Length int64
	CRC32  uint32
}

// Manifest 是导出清单：块列表、总校验和、快照版本。
type Manifest struct {
	Version   uint64
	ChunkSize int
	KeyCount  int
	TotalCRC  uint32
	Chunks    []Chunk
}

// Encode 把清单编码为头部帧字节。
func (m *Manifest) Encode() ([]byte, error) {
	js, err := json.Marshal(m)
	if err != nil {
		return nil, err
	}
	out := make([]byte, 0, headerLen+len(js)+crcLen)
	var hdr [headerLen]byte
	binary.LittleEndian.PutUint32(hdr[0:4], magic)
	binary.LittleEndian.PutUint32(hdr[4:8], uint32(len(js)))
	out = append(out, hdr[:]...)
	out = append(out, js...)
	var crc [crcLen]byte
	binary.LittleEndian.PutUint32(crc[:], crc32.ChecksumIEEE(js))
	return append(out, crc[:]...), nil
}

// DecodeFrame 从文件字节头部解码清单帧，返回清单与块区起始偏移。
func DecodeFrame(file []byte) (m *Manifest, chunksStart int64, err error) {
	if len(file) < headerLen {
		return nil, 0, ErrFrame
	}
	if binary.LittleEndian.Uint32(file[0:4]) != magic {
		return nil, 0, ErrFrame
	}
	n := int64(binary.LittleEndian.Uint32(file[4:8]))
	end := int64(headerLen) + n + crcLen
	if n < 0 || end > int64(len(file)) {
		return nil, 0, ErrFrame
	}
	js := file[headerLen : headerLen+n]
	if crc32.ChecksumIEEE(js) != binary.LittleEndian.Uint32(file[headerLen+n:end]) {
		return nil, 0, ErrFrame
	}
	m = &Manifest{}
	if err := json.Unmarshal(js, m); err != nil {
		return nil, 0, ErrFrame
	}
	return m, end, nil
}

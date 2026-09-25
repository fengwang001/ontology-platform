// Package verify 校验导出文件的完整性与内部一致性。
package verify

import (
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"os"

	"ontology/export"
	"ontology/manifest"
)

var (
	// ErrManifest 清单帧不完整或自身校验失败。
	ErrManifest = errors.New("verify: 清单不完整")
	// ErrChunkHeader 块头不完整或魔数错误。
	ErrChunkHeader = errors.New("verify: 块头不完整")
	// ErrChunkBody 块体不完整。
	ErrChunkBody = errors.New("verify: 块体不完整")
	// ErrCRC 块体或总校验和 CRC 不匹配。
	ErrCRC = errors.New("verify: CRC 不匹配")
	// ErrChunkOrder 块缺失、乱序或尾部多出数据。
	ErrChunkOrder = errors.New("verify: 块缺失或乱序")
)

// File 校验 path 指向的导出文件，成功时返回其清单。
func File(path string) (*manifest.Manifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	m, base, err := manifest.DecodeFrame(data)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrManifest, err)
	}
	var total uint32
	off := base
	for i := 0; i < len(m.Chunks); i++ {
		rest := data[off:]
		idx, plen, crc, ok := export.ParseChunk(rest)
		if !ok {
			return nil, fmt.Errorf("%w: 块 %d", ErrChunkHeader, i)
		}
		if idx != i {
			return nil, fmt.Errorf("%w: 期望块 %d 实为块 %d", ErrChunkOrder, i, idx)
		}
		if len(rest) < export.HeaderLen+plen {
			return nil, fmt.Errorf("%w: 块 %d", ErrChunkBody, i)
		}
		payload := rest[export.HeaderLen : export.HeaderLen+plen]
		if crc32.ChecksumIEEE(payload) != crc {
			return nil, fmt.Errorf("%w: 块 %d", ErrCRC, i)
		}
		mc := m.Chunks[i]
		if mc.Offset != off-base || mc.Length != int64(export.HeaderLen+plen) || mc.CRC32 != crc {
			return nil, fmt.Errorf("%w: 块 %d 与清单不符", ErrChunkOrder, i)
		}
		t, err := sumEntries(payload)
		if err != nil {
			return nil, fmt.Errorf("%w: 块 %d 条目损坏", ErrChunkBody, i)
		}
		total += t
		off += int64(export.HeaderLen + plen)
	}
	if off != int64(len(data)) {
		return nil, fmt.Errorf("%w: 尾部多出 %d 字节", ErrChunkOrder, len(data)-int(off))
	}
	if total != m.TotalCRC {
		return nil, fmt.Errorf("%w: 总校验和", ErrCRC)
	}
	return m, nil
}

// sumEntries 解析块载荷并累加每个条目的 CRC32（与导出端同算法）。
func sumEntries(p []byte) (uint32, error) {
	var sum uint32
	for len(p) > 0 {
		if len(p) < 8 {
			return 0, errors.New("条目头越界")
		}
		n := 8 + int(binary.LittleEndian.Uint32(p[0:4])) + int(binary.LittleEndian.Uint32(p[4:8]))
		if n > len(p) {
			return 0, errors.New("条目体越界")
		}
		sum += crc32.ChecksumIEEE(p[:n])
		p = p[n:]
	}
	return sum, nil
}

package doublewrite

import (
	"encoding/binary"
	"errors"
	"hash/crc32"
)

// 页布局（小端），全部长度均为页大小 pageSize 的定长缓冲：
//
//	偏移 0  uint32  magic（pageMagic）
//	偏移 4  uint32  页号 pageID
//	偏移 8  uint64  版本 version（从 1 开始单调递增，0 表示不可用）
//	偏移 16 uint32  有效负载长度 payloadLen
//	偏移 20 uint32  校验和 crc（覆盖除 crc 字段外的整页字节，尾部补零也参与）
//	偏移 24 ...     有效负载 payload，其余字节补 0
const (
	pageHeaderSize = 24
	pageMagic      = uint32(0x44575031) // "DWP1"
	pageIDZero     = uint32(0)
)

// 完成标记布局，固定 markerSectorCount 个扇区：
//
//	偏移 0  uint32 magic（markerMagic）
//	偏移 4  uint64 批次序号 batchSeq
//	偏移 12 uint32 批内页数 pageCount
//	偏移 16 uint32 校验和 crc（覆盖除 crc 字段外的整个标记缓冲）
//
// 标记整体按扇区写入，只有所有扇区落盘且 CRC 校验通过才算“有效”，
// 因此它充当整批双写区数据已完整落盘的提交点。
const (
	markerSectorCount = 1
	markerSize        = markerSectorCount * SectorSize
	markerMagic       = uint32(0x44574d31) // "DWM1"
	markerHeaderSize  = 20
)

var (
	errPageBufferSize   = errors.New("doublewrite: page buffer has wrong size")
	errMarkerBufferSize = errors.New("doublewrite: marker buffer has wrong size")
	errPayloadTooLarge  = errors.New("doublewrite: payload larger than page body")
)

var crcTable = crc32.MakeTable(crc32.Castagnoli)

// checksum 计算 raw 中除 crc 字段（off,off+4）外全部字节的 CRC32-C。
func checksum(raw []byte, off int) uint32 {
	h := crc32.New(crcTable)
	h.Write(raw[:off])
	h.Write(raw[off+4:])
	return h.Sum32()
}

// encodePage 把一页编码进长度恰为 pageSize 的缓冲，尾部补零。
func encodePage(pageSize int, pageID uint32, version uint64, payload []byte) []byte {
	if len(payload) > pageSize-pageHeaderSize {
		panic(errPayloadTooLarge)
	}
	raw := make([]byte, pageSize)
	binary.LittleEndian.PutUint32(raw[0:4], pageMagic)
	binary.LittleEndian.PutUint32(raw[4:8], pageID)
	binary.LittleEndian.PutUint64(raw[8:16], version)
	binary.LittleEndian.PutUint32(raw[16:20], uint32(len(payload)))
	copy(raw[pageHeaderSize:], payload)
	binary.LittleEndian.PutUint32(raw[20:24], checksum(raw, 20))
	return raw
}

// decodePage 校验并解析一页。任何字段不符（含撕裂写入、静默损坏）都返回 ok=false。
func decodePage(pageSize int, raw []byte) (pageID uint32, version uint64, payload []byte, ok bool) {
	if len(raw) != pageSize {
		return 0, 0, nil, false
	}
	if binary.LittleEndian.Uint32(raw[0:4]) != pageMagic {
		return 0, 0, nil, false
	}
	if binary.LittleEndian.Uint32(raw[20:24]) != checksum(raw, 20) {
		return 0, 0, nil, false
	}
	id := binary.LittleEndian.Uint32(raw[4:8])
	ver := binary.LittleEndian.Uint64(raw[8:16])
	n := int(binary.LittleEndian.Uint32(raw[16:20]))
	if n > pageSize-pageHeaderSize {
		return 0, 0, nil, false
	}
	payload = make([]byte, n)
	copy(payload, raw[pageHeaderSize:pageHeaderSize+n])
	return id, ver, payload, true
}

// encodeMarker 编码批次完成标记。
func encodeMarker(batchSeq uint64, pageCount uint32) []byte {
	raw := make([]byte, markerSize)
	binary.LittleEndian.PutUint32(raw[0:4], markerMagic)
	binary.LittleEndian.PutUint64(raw[4:12], batchSeq)
	binary.LittleEndian.PutUint32(raw[12:16], pageCount)
	binary.LittleEndian.PutUint32(raw[16:20], checksum(raw, 16))
	return raw
}

// decodeMarker 校验完成标记。部分扇区落盘或损坏都返回 ok=false。
func decodeMarker(raw []byte) (batchSeq uint64, pageCount uint32, ok bool) {
	if len(raw) != markerSize {
		return 0, 0, false
	}
	if binary.LittleEndian.Uint32(raw[0:4]) != markerMagic {
		return 0, 0, false
	}
	if binary.LittleEndian.Uint32(raw[16:20]) != checksum(raw, 16) {
		return 0, 0, false
	}
	return binary.LittleEndian.Uint64(raw[4:12]),
		binary.LittleEndian.Uint32(raw[12:16]), true
}

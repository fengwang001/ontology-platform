package walstore

import (
	"encoding/binary"
	"hash/crc32"
)

// 记录格式（大端）：
//
//	magic   uint32  固定 "WAL1"
//	length  uint32  负载字节数
//	crc     uint32  负载的 CRC32-IEEE
//	payload []byte  count uint32 + count 个 (klen, vlen, key, val)
//
// 一条记录对应一次 Commit 的整个批次，只有完整落盘且 CRC
// 校验通过才会在恢复时被应用，从而保证批次原子可见。
const (
	recordMagic      = 0x57414C31 // "WAL1"
	recordHeaderSize = 12
)

func encodeRecord(batch map[string]string) []byte {
	size := 4
	for k, v := range batch {
		size += 8 + len(k) + len(v)
	}
	payload := make([]byte, 0, size)
	payload = binary.BigEndian.AppendUint32(payload, uint32(len(batch)))
	for k, v := range batch {
		payload = binary.BigEndian.AppendUint32(payload, uint32(len(k)))
		payload = binary.BigEndian.AppendUint32(payload, uint32(len(v)))
		payload = append(payload, k...)
		payload = append(payload, v...)
	}
	rec := make([]byte, 0, recordHeaderSize+len(payload))
	rec = binary.BigEndian.AppendUint32(rec, recordMagic)
	rec = binary.BigEndian.AppendUint32(rec, uint32(len(payload)))
	rec = binary.BigEndian.AppendUint32(rec, crc32.ChecksumIEEE(payload))
	return append(rec, payload...)
}

// decodeRecord 解析 buf 开头的一条记录，返回批次与记录总字节数。
// 记录不完整或损坏（含 CRC 不符）时 ok=false，调用方应把该位置
// 视为日志的逻辑末尾。
func decodeRecord(buf []byte) (batch map[string]string, total int, ok bool) {
	if len(buf) < recordHeaderSize {
		return nil, 0, false
	}
	if binary.BigEndian.Uint32(buf[0:4]) != recordMagic {
		return nil, 0, false
	}
	length := binary.BigEndian.Uint32(buf[4:8])
	crc := binary.BigEndian.Uint32(buf[8:12])
	total = recordHeaderSize + int(length)
	if total > len(buf) {
		return nil, 0, false
	}
	payload := buf[recordHeaderSize:total]
	if crc32.ChecksumIEEE(payload) != crc {
		return nil, 0, false
	}
	batch, ok = decodePayload(payload)
	if !ok {
		return nil, 0, false
	}
	return batch, total, true
}

func decodePayload(p []byte) (map[string]string, bool) {
	if len(p) < 4 {
		return nil, false
	}
	count := binary.BigEndian.Uint32(p[0:4])
	p = p[4:]
	batch := make(map[string]string, count)
	for i := uint32(0); i < count; i++ {
		if len(p) < 8 {
			return nil, false
		}
		kl := int(binary.BigEndian.Uint32(p[0:4]))
		vl := int(binary.BigEndian.Uint32(p[4:8]))
		p = p[8:]
		if len(p) < kl+vl {
			return nil, false
		}
		batch[string(p[:kl])] = string(p[kl : kl+vl])
		p = p[kl+vl:]
	}
	if len(p) != 0 {
		return nil, false
	}
	return batch, true
}

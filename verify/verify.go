// Package verify 定义补丁与结果完整性校验的错误类别（可用 errors.Is
// 区分），并提供 CRC 密封/校验与结果强校验和比对。
package verify

import (
	"encoding/binary"
	"errors"
	"hash/crc32"

	"ontology/chunk"
)

// 四类可判定的损坏/不一致错误，均可用 errors.Is 区分。
var (
	// ErrTruncatedHeader 补丁头部不完整。
	ErrTruncatedHeader = errors.New("verify: truncated patch header")
	// ErrTruncatedInstruction 补丁指令不完整。
	ErrTruncatedInstruction = errors.New("verify: truncated patch instruction")
	// ErrTruncatedData 补丁新数据段不完整。
	ErrTruncatedData = errors.New("verify: truncated patch data")
	// ErrCRCMismatch 补丁 CRC 缺失或不匹配。
	ErrCRCMismatch = errors.New("verify: patch CRC mismatch")
	// ErrBlockOutOfRange 复用块指令的块号超出目标端块数。
	ErrBlockOutOfRange = errors.New("verify: copy instruction block out of range")
	// ErrChecksumMismatch 应用结果的强校验和与源端不一致。
	ErrChecksumMismatch = errors.New("verify: result checksum mismatch")
)

// Seal 在 data 末尾追加 4 字节 CRC32（IEEE），返回新切片。
func Seal(data []byte) []byte {
	return binary.BigEndian.AppendUint32(data, crc32.ChecksumIEEE(data))
}

// Open 校验 data 末尾 4 字节 CRC32，返回去掉 CRC 的负载。
// CRC 缺失或不匹配都报 ErrCRCMismatch。
func Open(data []byte) ([]byte, error) {
	if len(data) < 4 {
		return nil, ErrCRCMismatch
	}
	payload, tail := data[:len(data)-4], data[len(data)-4:]
	if crc32.ChecksumIEEE(payload) != binary.BigEndian.Uint32(tail) {
		return nil, ErrCRCMismatch
	}
	return payload, nil
}

// CheckResult 比对应用结果的强校验和与源端期望值，不一致时报
// ErrChecksumMismatch。
func CheckResult(result []byte, want chunk.Strong) error {
	if chunk.SumStrong(result) != want {
		return ErrChecksumMismatch
	}
	return nil
}

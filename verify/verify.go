// Package verify 定义补丁与结果完整性校验的哨兵错误，并提供损坏检测。
package verify

import (
	"errors"
	"fmt"

	"ontology/chunk"
)

// 各类可判定错误，均可用 errors.Is 区分。
var (
	// ErrHeaderIncomplete 补丁头部不完整（不足 36 字节或 magic 错误）。
	ErrHeaderIncomplete = errors.New("verify: patch header incomplete")
	// ErrInstrIncomplete 指令的定长部分（op/块号/数据长度）不完整或非法。
	ErrInstrIncomplete = errors.New("verify: patch instruction incomplete")
	// ErrDataIncomplete 数据指令的 payload 不完整。
	ErrDataIncomplete = errors.New("verify: patch data segment incomplete")
	// ErrCRCMismatch 尾部 CRC 缺失或与内容不符。
	ErrCRCMismatch = errors.New("verify: patch CRC mismatch")
	// ErrOutOfRange 复用指令引用了超出目标块范围的块号。
	ErrOutOfRange = errors.New("verify: block index out of range")
	// ErrMismatch 应用结果与源端整体强校验和不一致。
	ErrMismatch = errors.New("verify: result does not match source")
)

// CheckResult 校验应用结果的整体强校验和是否与源端一致。
func CheckResult(wantHash [16]byte, got []byte) error {
	if h := chunk.Strong(got); h != wantHash {
		return fmt.Errorf("%w: want %x got %x", ErrMismatch, wantHash[:4], h[:4])
	}
	return nil
}

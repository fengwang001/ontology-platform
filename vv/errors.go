package vv

import "errors"

// 全工程共享的哨兵错误，均可通过 errors.Is 区分。
var (
	// ErrOverflow 计数器已达 uint64 上界，再递增会回绕。
	ErrOverflow = errors.New("vv: version counter overflow")
	// ErrUnknownReplica 向量中出现 registry 未登记的副本 ID。
	ErrUnknownReplica = errors.New("vv: unknown replica id")
	// ErrCounterRollback 同一来源分量较已见水位变小（时钟回拨/状态回滚）。
	ErrCounterRollback = errors.New("vv: counter rollback detected")
	// ErrPruneUnsafe 裁剪会改变仍活跃副本之间的偏序关系。
	ErrPruneUnsafe = errors.New("vv: pruning would change ordering of active replicas")
	// ErrHeaderTruncated 编码字节在头部结束前被截断。
	ErrHeaderTruncated = errors.New("vv: truncated header")
	// ErrEntryTruncated 编码字节在某个分量/条目字段中途被截断。
	ErrEntryTruncated = errors.New("vv: truncated entry")
	// ErrCRCTruncated 编码字节在 CRC 校验字段处被截断。
	ErrCRCTruncated = errors.New("vv: truncated crc")
	// ErrCRCMismatch CRC 完整但校验失败。
	ErrCRCMismatch = errors.New("vv: crc mismatch")
	// ErrInvalid 非法参数（如空格式魔数、cap 为 0）。
	ErrInvalid = errors.New("vv: invalid argument")
)

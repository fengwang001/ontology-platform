// Package spillgrp 管理右侧同键分组的内存缓冲与溢出文件：
// 自描述头、逐行长度前缀与 CRC32 校验，以及 building/spilling/sealed 状态机。
package spillgrp

import "errors"

// 损坏与执行期哨兵错误，全部可用 errors.Is 区分。
var (
	// ErrHeader：溢出文件头部不完整或魔数/版本不被识别。
	ErrHeader = errors.New("spillgrp: incomplete or invalid header")
	// ErrLength：某帧的 4 字节长度前缀不完整。
	ErrLength = errors.New("spillgrp: incomplete length prefix")
	// ErrTruncated：某帧声明的行体未完整落盘。
	ErrTruncated = errors.New("spillgrp: truncated row body")
	// ErrCRC：帧 CRC 缺失（截断落在 CRC 区间）或校验不匹配。
	ErrCRC = errors.New("spillgrp: crc missing or mismatch")
	// ErrSeek：重扫回退到组起点时 Seek 失败（可注入）。
	ErrSeek = errors.New("spillgrp: seek to group start failed")
	// ErrState：在未密封（未落盘完成）时尝试终态操作。
	ErrState = errors.New("spillgrp: group not sealed")
)

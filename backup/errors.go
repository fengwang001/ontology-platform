package backup

import "errors"

var (
	// ErrBlockIndexOutOfRange 块号不在 [0, BlockCount) 范围内。
	ErrBlockIndexOutOfRange = errors.New("backup: block index out of range")
	// ErrBlockSizeMismatch 写入或读取的块长度与卷块大小不一致。
	ErrBlockSizeMismatch = errors.New("backup: block size mismatch")
	// ErrBackupNotFound 指定 ID 的备份不存在。
	ErrBackupNotFound = errors.New("backup: backup not found")
	// ErrStorageLimitExceeded 完成本次备份后存储块总数将超过上限。
	ErrStorageLimitExceeded = errors.New("backup: stored block count would exceed limit")
	// ErrBackupInRestorePath 备份正处于某个未关闭还原会话的回溯路径上。
	ErrBackupInRestorePath = errors.New("backup: backup is on an open restore session path")
	// ErrSessionClosed 还原会话已关闭后再次读取。
	ErrSessionClosed = errors.New("backup: restore session closed")
)

package raid

import "errors"

// 可被 errors.Is 区分的拒绝原因。
var (
	// ErrBlockOutOfRange 逻辑块号越界。
	ErrBlockOutOfRange = errors.New("raid: logical block out of range")
	// ErrStripeOutOfRange 条带号越界。
	ErrStripeOutOfRange = errors.New("raid: stripe out of range")
	// ErrDiskOutOfRange 磁盘编号越界。
	ErrDiskOutOfRange = errors.New("raid: disk index out of range")
	// ErrDiskAlreadyFailed 已有一块盘失效，拒绝再标记另一块。
	ErrDiskAlreadyFailed = errors.New("raid: a disk is already failed")
	// ErrDiskNotFailed 目标盘未失效，拒绝重建。
	ErrDiskNotFailed = errors.New("raid: target disk is not failed")
	// ErrRebuildInProgress 重建进行中，拒绝再次发起。
	ErrRebuildInProgress = errors.New("raid: rebuild already in progress")
	// ErrTooFewDisks 盘数小于 3。
	ErrTooFewDisks = errors.New("raid: need at least 3 disks")
	// ErrGeometryMismatch 各盘块数或块大小不一致。
	ErrGeometryMismatch = errors.New("raid: device geometry mismatch")
	// ErrBlockSizeMismatch 写入数据长度不等于块大小。
	ErrBlockSizeMismatch = errors.New("raid: data length != block size")
	// ErrJournalCorrupt 意图日志超级块校验失败。
	ErrJournalCorrupt = errors.New("raid: journal superblock corrupt")
	// ErrCrashed 模拟断电：设备已掉电。
	ErrCrashed = errors.New("raid: simulated power loss")
)

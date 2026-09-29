package raid5

import "errors"

// 可区分的拒绝原因。调用方可用 errors.Is 精确判定。
var (
	// ErrBlockOutOfRange 逻辑块号越界（含长度为 0 或写过卷尾）。
	ErrBlockOutOfRange = errors.New("raid5: logical block out of range")
	// ErrTwoDisksFailed 已有一块盘失效时再次标记另一块盘失效。
	ErrTwoDisksFailed = errors.New("raid5: one disk already failed, cannot fail another")
	// ErrRebuildHealthyDisk 对未失效的盘执行换盘重建。
	ErrRebuildHealthyDisk = errors.New("raid5: cannot rebuild a healthy disk")
	// ErrRebuildInProgress 重建进行中再次发起重建。
	ErrRebuildInProgress = errors.New("raid5: rebuild already in progress")
	// ErrDiskIndexOutOfRange 盘号越界。
	ErrDiskIndexOutOfRange = errors.New("raid5: disk index out of range")
	// ErrDiskDead 访问的盘已失效（内部使用）。
	ErrDiskDead = errors.New("raid5: disk is dead")
	// ErrDoubleFault 两块及以上盘失效，数据不可恢复。
	ErrDoubleFault = errors.New("raid5: double fault, data unrecoverable")
	// ErrFormat 卷元数据损坏。
	ErrFormat = errors.New("raid5: volume format error")
)

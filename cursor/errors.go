package cursor

import "errors"

// ErrInvalidParam 表示 now 等参数越界。
var ErrInvalidParam = errors.New("cursor: invalid parameter")

// ErrClockRollback 表示 Pull 的 now 小于先前已接受的最大 now。
var ErrClockRollback = errors.New("cursor: clock rollback")

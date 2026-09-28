package txnlog

import "errors"

// ErrIllegalProducer 表示生产者 id 不在日志允许的生产者集合内。
var ErrIllegalProducer = errors.New("txnlog: illegal producer")

// ErrNoTransaction 表示生产者在没有进行中事务时写入提交/中止标记。
var ErrNoTransaction = errors.New("txnlog: marker without an in-progress transaction")

// ErrIllegalHighWatermark 表示新高水位非法（越界或小于当前高水位）。
var ErrIllegalHighWatermark = errors.New("txnlog: illegal high watermark")

// ErrIllegalReadOffset 表示读取起点非法（负数或大于稳定位点）。
var ErrIllegalReadOffset = errors.New("txnlog: illegal read offset")

// Package cron 实现五字段 Cron 表达式的下一次触发分钟计算，
// 以及 CronJob 式的错过触发补偿控制器。
package cron

import (
	"errors"
	"sync"
)

// 字段索引。
const (
	fieldMinute = iota
	fieldHour
	fieldDay
	fieldMonth
	fieldWeek
	fieldCount
)

// Policy 决定补偿触发时与仍在运行的活动任务之间的关系。
type Policy int

const (
	Allow Policy = iota + 1
	Forbid
	Replace
)

// fieldSpec 保存一个字段解析后的取值集合与通配标志。
type fieldSpec struct {
	values   []bool
	wildcard bool
}

// cronSpec 保存解析后的五个字段。
type cronSpec struct {
	fields [fieldCount]fieldSpec
}

// Job 是一个 CronJob 式补偿控制器，可被并发调用。
type Job struct {
	mu       sync.Mutex
	spec     *cronSpec
	deadline int64
	policy   Policy
	last     int64
	water    int64
	suspend  bool
	active   map[int64]struct{}
	nextID   int64
	skipped  int64
	replaced int64
}

var (
	ErrSyntax      = errors.New("cron: syntax error")
	ErrRange       = errors.New("cron: value out of range")
	ErrReversed    = errors.New("cron: range start greater than end")
	ErrStep        = errors.New("cron: invalid step")
	ErrFieldCount  = errors.New("cron: spec must have exactly 5 fields")
	ErrNoNext      = errors.New("cron: no next fire time")
	ErrIllegalTime = errors.New("cron: illegal time")
	ErrClockBack   = errors.New("cron: clock moved backwards")
	ErrTooMany     = errors.New("cron: too many missed fires")
	ErrInvalidArg  = errors.New("cron: invalid argument")
	ErrNoSuchTask  = errors.New("cron: task does not exist")
)

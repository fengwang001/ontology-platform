// Package progress 管理进度文件：已写入区间、CRC32 校验、截断分类与
// 最大可恢复前缀回退。
package progress

import (
	"errors"
	"os"
)

// State 是批次在进度头部的生命周期状态。
type State string

const (
	// Prepared 表示批次进行中。
	Prepared State = "prepared"
	// Committed 表示批次已提交完成。
	Committed State = "committed"
)

// Interval 是半开记录区间 [Start, End)。
type Interval struct {
	Start int
	End   int
}

// Progress 是进度文件的内存表示。
type Progress struct {
	BatchID   string
	Count     int
	State     State
	Intervals []Interval
}

var (
	// ErrHeaderTruncated 表示头部不完整。
	ErrHeaderTruncated = errors.New("progress header truncated")
	// ErrRecordTruncated 表示区间记录不完整。
	ErrRecordTruncated = errors.New("progress record truncated")
	// ErrCRCMismatch 表示 CRC 校验不匹配。
	ErrCRCMismatch = errors.New("progress crc mismatch")
)

// HighWater 返回最大已写入下标（区间并集上界）。
func (p *Progress) HighWater() int { return 0 }

// SetHighWater 以高水位重建区间，按粒度 chunk 切分。
func (p *Progress) SetHighWater(hwm, chunk int) {}

// Save 原子写入进度文件（临时文件 + rename）。
func (p *Progress) Save(path string) error { return nil }

// Load 读取进度文件；文件不存在返回 (nil, nil)。
// 截断/损坏时返回最大可恢复前缀与对应可判定错误。
func Load(path string) (*Progress, error) { return nil, nil }

var _ = os.ErrNotExist

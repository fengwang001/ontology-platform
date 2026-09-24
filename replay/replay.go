// Package replay 按事件序号区间跨段回放，并用稀疏索引定位。
package replay

import (
	"errors"

	"ontology/event"
)

var (
	// ErrBadRange 表示 from > to。
	ErrBadRange = errors.New("replay: from > to")
	// ErrStaleIndex 索引锚点偏移解不出合法记录，索引已失效。
	ErrStaleIndex = errors.New("replay: stale index anchor")
)

// Stats 记录一次回放的定位与读取开销。
type Stats struct {
	Skipped  int64 // 定位阶段跳过的事件数
	Bytes    int64 // 读取的字节数（含头与记录）
	Fallback bool  // 是否有段回退到全段扫描
	Stale    bool  // 是否检测到索引失效
}

// Result 是一次回放的输出。
type Result struct {
	Events []event.Event
	Stats  Stats
}

// Runner 在一个日志目录上回放。
type Runner struct {
	dir      string
	interval uint64
}

// New 创建回放器，interval 为索引锚点间隔。
func New(dir string, interval uint64) *Runner {
	return &Runner{dir: dir, interval: interval}
}

func (s *Stats) add(o Stats) {
	s.Skipped += o.Skipped
	s.Bytes += o.Bytes
	s.Fallback = s.Fallback || o.Fallback
	s.Stale = s.Stale || o.Stale
}

package segmentlog

import "fmt"

// SegmentInfo 是段的不可变快照，供测试与日志观测使用。
type SegmentInfo struct {
	ID       int
	Bytes    int
	Records  int
	MaxTime  string
	StartOff int64
	EndOff   int64
}

// SnapshotSegments 返回当前段列表的快照（调用时加锁）。
func (l *Log) SnapshotSegments() []SegmentInfo {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.snapshotSegmentsInfoLocked()
}

func (l *Log) snapshotSegmentsInfoLocked() []SegmentInfo {
	out := make([]SegmentInfo, len(l.segs))
	for i, s := range l.segs {
		out[i] = SegmentInfo{
			ID: s.id, Bytes: s.bytes, Records: len(s.records),
			MaxTime: s.maxTime.Format("2006-01-02T15:04:05.000000000"),
			StartOff: s.startOff, EndOff: s.endOff,
		}
	}
	return out
}

func (l *Log) snapshotSegmentsLocked() string {
	return fmt.Sprintf("%v", l.snapshotSegmentsInfoLocked())
}

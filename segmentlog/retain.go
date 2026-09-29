package segmentlog

import (
	"fmt"
	"time"
)

// Retain 先做时间阶段、再做大小阶段。
//
// 两个阶段都从最旧段起逐段判定，遇到第一个不满足删除条件的段即停止；
// 活动段永不删除。被删除段均为前缀整段，故剩余段依旧首尾相接，
// StartOffset 随删除字节数前移（单调不减）。
func (l *Log) Retain(now time.Time) (*Report, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	s := &l.state
	if !now.Equal(s.lastTime) && now.Before(s.lastTime) {
		s.debugf("RETAIN REJECT clock-backwards now=%s last=%s", formatTime(now), formatTime(s.lastTime))
		return nil, fmt.Errorf("%w: retain time %s is before last accepted time %s", ErrClockBackwards, formatTime(now), formatTime(s.lastTime))
	}

	report := &Report{
		Now:               now,
		StartOffsetBefore: s.startOff,
		TotalBefore:       s.total,
		TimePhase:         make([]Decision, 0, len(s.segments)),
		SizePhase:         make([]Decision, 0, len(s.segments)),
		Deleted:           make([]SegmentInfo, 0),
	}
	s.debugf("RETAIN begin now=%s cutoff(time)=%s %s", formatTime(now), formatTime(now.Add(-s.cfg.MaxAge)), s.describeSegments())

	// 阶段一：时间。段保留时间取段内记录时间戳最大值（LastTime）。
	if s.cfg.MaxAge > 0 {
		cutoff := now.Add(-s.cfg.MaxAge)
		for _, seg := range s.segments {
			info := seg.info()
			switch {
			case seg.active:
				report.TimePhase = append(report.TimePhase, Decision{"time", info, false, "active segment is never deleted"})
				s.debugf("RETAIN time seg=%d KEEP active", seg.id)
			case seg.lastTime.Before(cutoff):
				report.TimePhase = append(report.TimePhase, Decision{"time", info, true, fmt.Sprintf("lastTime %s < cutoff %s", formatTime(seg.lastTime), formatTime(cutoff))})
				s.debugf("RETAIN time seg=%d DELETE last=%s cutoff=%s", seg.id, formatTime(seg.lastTime), formatTime(cutoff))
				continue
			default:
				report.TimePhase = append(report.TimePhase, Decision{"time", info, false, fmt.Sprintf("lastTime %s >= cutoff %s", formatTime(seg.lastTime), formatTime(cutoff))})
				s.debugf("RETAIN time seg=%d KEEP last=%s cutoff=%s STOP", seg.id, formatTime(seg.lastTime), formatTime(cutoff))
			}
			break
		}

		drop := 0
		for _, d := range report.TimePhase {
			if !d.Delete {
				break
			}
			drop++
		}
		for i := 0; i < drop; i++ {
			seg := s.segments[i]
			report.Deleted = append(report.Deleted, seg.info())
			s.startOff += seg.bytes
			s.total -= seg.bytes
		}
		s.segments = append([]*segment(nil), s.segments[drop:]...)
	}

	// 阶段二：大小。从时间阶段后的新最旧段重新逐段判定。
	if s.cfg.MaxTotalBytes > 0 {
		for len(s.segments) > 0 {
			seg := s.segments[0]
			info := seg.info()
			switch {
			case seg.active:
				report.SizePhase = append(report.SizePhase, Decision{"size", info, false, "active segment is never deleted"})
				s.debugf("RETAIN size seg=%d KEEP active", seg.id)
			case s.total > s.cfg.MaxTotalBytes:
				report.SizePhase = append(report.SizePhase, Decision{"size", info, true, fmt.Sprintf("total %d > limit %d before removal", s.total, s.cfg.MaxTotalBytes)})
				s.debugf("RETAIN size seg=%d DELETE total=%d limit=%d", seg.id, s.total, s.cfg.MaxTotalBytes)
				report.Deleted = append(report.Deleted, seg.info())
				s.startOff += seg.bytes
				s.total -= seg.bytes
				s.segments = s.segments[1:]
				continue
			default:
				report.SizePhase = append(report.SizePhase, Decision{"size", info, false, fmt.Sprintf("total %d <= limit %d", s.total, s.cfg.MaxTotalBytes)})
				s.debugf("RETAIN size seg=%d KEEP total=%d limit=%d STOP", seg.id, s.total, s.cfg.MaxTotalBytes)
			}
			break
		}
	}

	report.StartOffsetAfter = s.startOff
	report.TotalAfter = s.total
	s.debugf("RETAIN end start=%d->%d total=%d->%d %s", report.StartOffsetBefore, s.startOff, report.TotalBefore, s.total, s.describeSegments())
	return report, nil
}

package segmentlog

import (
	"context"
	"slices"
	"strconv"
)

// Retain 执行两阶段保留并返回被删除的段数与逐步判定依据。
//
// 阶段一（时间）：从最旧段起，若段的保留时间（段内最大时间戳）早于
// now-RetentionMillis，则整段删除；遇到第一个未过期的段即停止。
// 阶段二（大小）：从（时间阶段后）最旧段起，若删除该段后能使总字节数
// 降到 MaxTotalBytes 以内，则整段删除；遇到第一个“删了仍超限或已不超限”
// 的段即停止。活动段在两个阶段中都永不删除。
func (l *Log) Retain(ctx context.Context, now int64) (removed int, steps []Step, err error) {
	if err := ctx.Err(); err != nil {
		return 0, nil, err
	}
	if now <= 0 {
		return 0, nil, reject(ErrInvalidArgument, "retain clock must be positive, got %d", now)
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	if now < l.lastRetainNow {
		return 0, nil, reject(ErrClockBackwards,
			"retain clock %d is earlier than previous retain clock %d", now, l.lastRetainNow)
	}
	l.lastRetainNow = now

	steps = make([]Step, 0)
	snapshot := func(idx int) SegmentInfo { return l.segmentInfoLocked(idx) }

	// 阶段一：时间保留。Retain 永远不删除活动段，因此至少保留最后一个段。
	if l.cfg.RetentionMillis > 0 {
		cutoff := now - l.cfg.RetentionMillis
		for len(l.segs) > 0 {
			oldest := l.segs[0]
			if len(l.segs) == 1 {
				// 头部即活动段：永不删除，无论是否过期。
				info := snapshot(0)
				steps = append(steps, Step{
					Phase: "time", Action: "keep", Segment: info,
					Reason:      reasonKeepActive("time", oldest.maxTime, cutoff),
					StartOffset: l.startOffset, TotalBytes: l.totalBytes,
				})
				break
			}
			info := snapshot(0)
			if oldest.maxTime >= cutoff {
				steps = append(steps, Step{
					Phase: "time", Action: "keep", Segment: info,
					Reason:      reasonKeepTime(oldest.maxTime, cutoff),
					StartOffset: l.startOffset, TotalBytes: l.totalBytes,
				})
				break
			}
			l.dropOldestLocked()
			removed++
			steps = append(steps, Step{
				Phase: "time", Action: "remove", Segment: info,
				Reason:      reasonRemoveTime(oldest.maxTime, cutoff),
				StartOffset: l.startOffset, TotalBytes: l.totalBytes,
			})
		}
	}

	// 阶段二：大小保留。仅当删除该段确实能使总量达标时才删除；
	// 遇到第一个不需要删（或删了仍超限）的段即停止。
	for len(l.segs) > 0 {
		oldest := l.segs[0]
		info := snapshot(0)
		if len(l.segs) == 1 {
			steps = append(steps, Step{
				Phase: "size", Action: "keep", Segment: info,
				Reason:      reasonKeepActive("size", 0, l.cfg.MaxTotalBytes),
				StartOffset: l.startOffset, TotalBytes: l.totalBytes,
			})
			break
		}
		if l.totalBytes <= l.cfg.MaxTotalBytes {
			steps = append(steps, Step{
				Phase: "size", Action: "keep", Segment: info,
				Reason:      reasonKeepSize(l.totalBytes, l.cfg.MaxTotalBytes),
				StartOffset: l.startOffset, TotalBytes: l.totalBytes,
			})
			break
		}
		if l.totalBytes-oldest.bytes > l.cfg.MaxTotalBytes {
			steps = append(steps, Step{
				Phase: "size", Action: "keep", Segment: info,
				Reason:      reasonStopSize(oldest.bytes, l.totalBytes, l.cfg.MaxTotalBytes),
				StartOffset: l.startOffset, TotalBytes: l.totalBytes,
			})
			break
		}
		l.dropOldestLocked()
		removed++
		steps = append(steps, Step{
			Phase: "size", Action: "remove", Segment: info,
			Reason:      reasonRemoveSize(oldest.bytes, l.totalBytes, l.cfg.MaxTotalBytes),
			StartOffset: l.startOffset, TotalBytes: l.totalBytes,
		})
	}

	return removed, steps, nil
}

func reasonRemoveTime(maxTime, cutoff int64) string {
	return "segment maxTime " + itoa(maxTime) + " < cutoff " + itoa(cutoff)
}

func reasonKeepTime(maxTime, cutoff int64) string {
	return "segment maxTime " + itoa(maxTime) + " >= cutoff " + itoa(cutoff) + " (stop at first fresh segment)"
}

func reasonKeepActive(phase string, vals ...int64) string {
	if phase == "time" {
		return "segment is the active segment and must never be removed (maxTime " +
			itoa(vals[0]) + ", cutoff " + itoa(vals[1]) + ")"
	}
	return "segment is the active segment and must never be removed (limit " + itoa(vals[1]) + ")"
}

func reasonRemoveSize(segBytes, total, limit int64) string {
	return "total " + itoa(total) + " > limit " + itoa(limit) + " and dropping " +
		itoa(segBytes) + " bytes would bring total to " + itoa(total-segBytes) + " (<= limit)"
}

func reasonKeepSize(total, limit int64) string {
	return "total " + itoa(total) + " <= limit " + itoa(limit) + " (stop at first segment that satisfies size)"
}

func reasonStopSize(segBytes, total, limit int64) string {
	return "dropping this segment (" + itoa(segBytes) + " bytes) would leave total at " +
		itoa(total-segBytes) + " > limit " + itoa(limit) + " (stop without removing)"
}

// dropOldestLocked 删除最旧的整段并前移起始位点。调用方必须持锁，
// 且保证删除后仍至少保留活动段（即 len(l.segs) > 1）。
func (l *Log) dropOldestLocked() {
	oldest := l.segs[0]
	l.segs = l.segs[1:]
	l.startOffset = oldest.endOffset
	l.totalBytes -= oldest.bytes
}

func (l *Log) segmentInfoLocked(idx int) SegmentInfo {
	s := l.segs[idx]
	return SegmentInfo{
		Index:       idx,
		StartOffset: s.startOffset,
		EndOffset:   s.endOffset,
		Bytes:       s.bytes,
		MaxTime:     s.maxTime,
		Records:     len(s.records),
		Active:      idx == len(l.segs)-1,
	}
}

// Segments 返回段列表的快照（按从旧到新顺序，最后一个为活动段）。
func (l *Log) Segments() []SegmentInfo {
	l.mu.RLock()
	defer l.mu.RUnlock()
	out := make([]SegmentInfo, len(l.segs))
	for i := range l.segs {
		out[i] = l.segmentInfoLocked(i)
	}
	return out
}

// Read 从 offset 起顺序读取到日志末尾。offset 必须是某条尚存记录的起始位点：
// 早于当前起始位点（已删除）、晚于末尾位点或落在记录内部都属于越界。
// 返回记录的数据均为拷贝，调用方修改不影响日志内容。
func (l *Log) Read(ctx context.Context, offset int64) ([]Record, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if offset < 0 {
		return nil, reject(ErrInvalidArgument, "offset must be non-negative, got %d", offset)
	}

	l.mu.RLock()
	defer l.mu.RUnlock()

	if len(l.segs) == 0 {
		if offset != 0 {
			return nil, reject(ErrOffsetOutOfRange, "offset %d in empty log (valid: 0)", offset)
		}
		return []Record{}, nil
	}
	if offset < l.startOffset {
		return nil, reject(ErrOffsetOutOfRange,
			"offset %d is before start offset %d (data already removed)", offset, l.startOffset)
	}
	if offset == l.nextOffset {
		return []Record{}, nil
	}
	if offset > l.nextOffset {
		return nil, reject(ErrOffsetOutOfRange,
			"offset %d is past end offset %d", offset, l.nextOffset)
	}

	// 定位包含 offset 的段：[startOffset, endOffset)。
	si, found := slices.BinarySearchFunc(l.segs, offset, func(s *segment, off int64) int {
		switch {
		case off < s.startOffset:
			return 1
		case off >= s.endOffset:
			return -1
		default:
			return 0
		}
	})
	if !found {
		return nil, reject(ErrOffsetOutOfRange, "offset %d falls in a removed gap", offset)
	}

	// 段内校验 offset 必须精确落在某条记录的起始位点上。
	seg := l.segs[si]
	cursor := seg.startOffset
	ri := -1
	for i, e := range seg.records {
		if cursor == offset {
			ri = i
			break
		}
		cursor += int64(len(e.data))
		if cursor > offset {
			return nil, reject(ErrOffsetOutOfRange,
				"offset %d is inside a record (next boundary %d)", offset, cursor)
		}
	}
	if ri < 0 {
		return nil, reject(ErrOffsetOutOfRange, "offset %d is not a record boundary", offset)
	}

	out := make([]Record, 0)
	for j := ri; j < len(seg.records); j++ {
		e := seg.records[j]
		data := make([]byte, len(e.data))
		copy(data, e.data)
		out = append(out, Record{Time: e.time, Data: data})
	}
	for _, next := range l.segs[si+1:] {
		for _, e := range next.records {
			data := make([]byte, len(e.data))
			copy(data, e.data)
			out = append(out, Record{Time: e.time, Data: data})
		}
	}
	return out, nil
}

func itoa(v int64) string { return strconv.FormatInt(v, 10) }

package segmentlog

import "fmt"

// Read 返回起点 start 起、累计字节不超过 maxBytes 的连续完整记录副本。
//
// start 必须等于现存日志起始位点或某条记录的边界；start 等于当前末尾时
// 返回空结果。maxBytes 必须为正。任何越界或未对齐都返回 ErrOutOfRange，
// 且读取不改变日志状态。返回的字节切片均为内部数据的副本，调用方可自由修改。
func (l *Log) Read(start, maxBytes int64) ([][]byte, error) {
	if maxBytes <= 0 {
		return nil, fmt.Errorf("%w: maxBytes must be positive, got %d", ErrOutOfRange, maxBytes)
	}

	l.mu.RLock()
	defer l.mu.RUnlock()

	s := &l.state
	end := s.startOff + s.total
	if start < s.startOff || start > end {
		return nil, fmt.Errorf("%w: start %d not within [%d,%d]", ErrOutOfRange, start, s.startOff, end)
	}
	if start == end {
		return [][]byte{}, nil
	}

	out := make([][]byte, 0)
	budget := maxBytes
	started := false
	for _, seg := range s.segments {
		for _, rec := range seg.records {
			if !started {
				if rec.start < start {
					continue
				}
				if rec.start != start {
					return nil, fmt.Errorf("%w: start %d is not on a record boundary", ErrOutOfRange, start)
				}
				started = true
			}
			if rec.bytes > budget {
				s.debugf("READ start=%d stop offset=%d budget-left=%d", start, rec.start, budget)
				return out, nil
			}
			budget -= rec.bytes
			out = append(out, append([]byte(nil), rec.data...))
		}
	}
	if !started {
		return nil, fmt.Errorf("%w: start %d is not on a record boundary", ErrOutOfRange, start)
	}
	return out, nil
}

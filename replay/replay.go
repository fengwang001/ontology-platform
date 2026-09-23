package replay

import (
	"encoding/binary"
	"os"

	"ontology/event"
	"ontology/segment"
	"ontology/sparse"
)

// Report 记录一次回放的内部计数（字段非导出，经方法暴露）。
type Report struct {
	skipped      int // 定位阶段跳过的事件数
	bytesRead    int // 事件区实际扫描的字节数（不含段头）
	anchorUsed   bool
	indexInvalid bool
}

// SkippedEvents 返回定位阶段跳过的事件数。
func (r Report) SkippedEvents() int { return r.skipped }

// BytesRead 返回事件区实际扫描的字节数。
func (r Report) BytesRead() int { return r.bytesRead }

// AnchorUsed 报告本次定位是否使用了稀疏锚点。
func (r Report) AnchorUsed() bool { return r.anchorUsed }

// IndexInvalid 报告索引是否被判定失效并已回退全段扫描。
func (r Report) IndexInvalid() bool { return r.indexInvalid }

// Replay 按序号区间回放。边界语义：
// from > to 报 ErrInvalidRange；from < 最小序号钳到段首；
// to > 最大序号放到末尾；from > 最大序号返回空。
func (l *Log) Replay(from, to uint64) ([]event.Event, Report, error) {
	if from > to {
		return nil, Report{}, ErrInvalidRange
	}
	metas := l.snapshot()
	var rep Report
	var out []event.Event
	if len(metas) == 0 {
		return nil, rep, nil
	}
	if from < metas[0].first {
		from = metas[0].first
	}
	for _, m := range metas {
		if m.count == 0 || to < m.first {
			break
		}
		segLast := m.first + m.count - 1
		if from > segLast {
			continue
		}
		localTo := to
		if localTo > segLast {
			localTo = segLast
		}
		evs, err := replaySegment(m, from, localTo, &rep)
		if err != nil {
			return nil, rep, err
		}
		out = append(out, evs...)
	}
	return out, rep, nil
}

func replaySegment(m segMeta, from, to uint64, rep *Report) ([]event.Event, error) {
	f, err := os.Open(m.path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return nil, err
	}
	size := fi.Size()
	startSeq, startOff, anchored := m.first, int64(segment.HeaderSize), false
	if idx, ierr := sparse.Load(indexPath(m.path)); ierr == nil {
		if a, ok := idx.Locate(from); ok {
			startSeq, startOff, anchored = a.Seq, int64(a.Offset), true
		}
	}
	evs, err := scanRange(f, size, startSeq, startOff, from, to, anchored, rep)
	if err != nil && anchored {
		// 锚点处解不出合法记录：索引失效，回退全段扫描。
		rep.indexInvalid = true
		rep.anchorUsed = false
		startSeq, startOff = m.first, segment.HeaderSize
		evs, err = scanRange(f, size, startSeq, startOff, from, to, false, rep)
	}
	return evs, err
}

// scanRange 从 (startSeq,startOff) 顺序扫到 to，只读取 [from,to] 内事件。
func scanRange(f *os.File, size, startSeq int64OrUint64, startOff, from, to uint64, anchored bool, rep *Report) ([]event.Event, error) {
	return nil, nil
}

func indexPath(segPath string) string {
	return segPath[:len(segPath)-len(".dat")] + ".idx"
}

var _ = binary.LittleEndian

// Package replay 按事件序号范围定位并回放段日志。
package replay

import (
	"errors"
	"io"

	"ontology/event"
	"ontology/segment"
	"ontology/sparse"
)

// ErrInvalidRange 表示 from > to 的非法区间。
var ErrInvalidRange = errors.New("replay: from > to")

// Report 是一次回放的统计与索引状态。
type Report struct {
	Skipped      int   // 定位阶段跳过的事件数（< 锚点间隔 N）
	BytesRead    int64 // 扫描读取的记录字节数
	IndexUsed    bool  // 是否用上了索引定位
	IndexInvalid bool  // 索引校验失败、已回退全段扫描
}

// Replayer 回放一个目录下的段日志。计数器非导出，经 Report 暴露。
type Replayer struct {
	dir string
	// 非导出计数器：包内测试直接断言
	skipped   int
	bytesRead int64
}

// New 创建针对 dir 的回放器。
func New(dir string) *Replayer { return &Replayer{dir: dir} }

// Replay 回放序号区间 [from, to]。
// from 小于最小序号时钳位到日志开头；to 超过最大序号时回放到末尾。
func (r *Replayer) Replay(from, to uint64) ([]event.Event, Report, error) {
	if from > to {
		return nil, Report{}, ErrInvalidRange
	}
	r.skipped, r.bytesRead = 0, 0
	rep := Report{}
	segs, err := segment.ListSegments(r.dir)
	if err != nil {
		return nil, rep, err
	}
	var out []event.Event
	for _, p := range segs {
		sc, err := segment.NewScannerStable(p, 1000)
		if err != nil {
			return nil, rep, err
		}
		h := sc.Header
		if h.Count == 0 || h.LastSeq() < from || h.FirstSeq > to {
			sc.Close()
			continue
		}
		start := max(from, h.FirstSeq)
		end := min(to, h.LastSeq())
		used, invalid := r.locate(sc, p, start)
		rep.IndexUsed = rep.IndexUsed || used
		rep.IndexInvalid = rep.IndexInvalid || invalid
		for {
			before := sc.Off()
			ev, err := sc.Next()
			if err == io.EOF {
				break
			}
			if err != nil {
				sc.Close()
				return nil, rep, err
			}
			n := sc.Off() - before
			if ev.Seq < start {
				r.skipped++
				r.bytesRead += n
				continue
			}
			if ev.Seq > end {
				break // 越界的一条不计入读取量
			}
			r.bytesRead += n
			out = append(out, ev)
		}
		sc.Close()
		if h.LastSeq() >= to {
			break
		}
	}
	rep.Skipped, rep.BytesRead = r.skipped, r.bytesRead
	return out, rep, nil
}

// locate 用稀疏索引把扫描器定位到「不大于 start 的最大锚点」；
// 锚点就地校验失败时回退全段扫描并标注索引失效。
func (r *Replayer) locate(sc *segment.Scanner, segPath string, start uint64) (used, invalid bool) {
	idx, err := sparse.ReadFile(segment.IndexPath(segPath))
	if err != nil {
		sc.SeekTo(segment.HeaderSize) // 索引缺失：全段扫描
		return false, false
	}
	anchor, hit := idx.Locate(start)
	if !hit {
		sc.SeekTo(segment.HeaderSize)
		return false, false
	}
	sc.SeekTo(anchor.Offset)
	ev, err := sc.Next()
	if err != nil || ev.Seq != anchor.Seq { // 偏移处解不出合法记录或序号不符
		sc.SeekTo(segment.HeaderSize)
		return false, true
	}
	sc.SeekTo(anchor.Offset)
	return true, false
}

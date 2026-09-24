package replay

import (
	"errors"
	"io"

	"ontology/event"
	"ontology/segment"
	"ontology/sparse"
)

// Range 回放闭区间 [from,to]。from 小于最小序号时从头开始；to 超过末尾时到末尾。
func (r *Runner) Range(from, to uint64) (Result, error) {
	if from > to {
		return Result{}, ErrBadRange
	}
	paths, err := segment.List(r.dir)
	if err != nil {
		return Result{}, err
	}
	res := Result{}
	for _, p := range paths {
		rd, err := segment.Open(p)
		if err != nil {
			return Result{}, err
		}
		h := rd.Header()
		first := h.FirstSeq
		last := h.FirstSeq + h.Count - 1
		// 段与区间不相交则跳过（count==0 的空段也跳过）。
		if h.Count == 0 || to < first || from > last {
			rd.Close()
			continue
		}
		st, evs, err := r.replaySegment(rd, from, to)
		closeErr := rd.Close()
		if err != nil {
			return Result{}, err
		}
		if closeErr != nil {
			return Result{}, closeErr
		}
		res.Stats.add(st)
		res.Events = append(res.Events, evs...)
	}
	return res, nil
}

func (r *Runner) replaySegment(rd *segment.Reader, from, to uint64) (Stats, []event.Event, error) {
	var st Stats
	idx, idxErr := sparse.LoadOrBuild(rd.Path(), r.interval)
	start := int64(segment.HeaderSize)
	used := false
	if idxErr == nil {
		if a, err := idx.Lookup(from); err == nil {
			start = a.Offset
			used = true
		}
	}
	rd.ResetCounters()
	rd.SeekRecord(start)
	var out []event.Event
	for {
		ev, err := rd.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if errors.Is(err, segment.ErrLenPrefixIncomplete) ||
			errors.Is(err, segment.ErrBodyIncomplete) ||
			errors.Is(err, segment.ErrCRC) {
			if used {
				// 索引承诺的合法记录边界出错：索引失效，回退全段。
				return r.fullScan(rd, from, to, true)
			}
			break
		}
		if err != nil {
			return r.fullScan(rd, from, to, used)
		}
		if ev.Seq < from {
			st.Skipped++
			continue
		}
		if ev.Seq > to {
			break
		}
		out = append(out, ev)
	}
	st.Bytes = rd.BytesRead()
	return st, out, nil
}

func (r *Runner) fullScan(rd *segment.Reader, from, to uint64, stale bool) (Stats, []event.Event, error) {
	rd.ResetCounters()
	rd.SeekRecord(segment.HeaderSize)
	var st Stats
	st.Fallback = stale
	st.Stale = stale
	var out []event.Event
	for {
		ev, err := rd.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if errors.Is(err, segment.ErrLenPrefixIncomplete) ||
			errors.Is(err, segment.ErrBodyIncomplete) ||
			errors.Is(err, segment.ErrCRC) {
			break
		}
		if err != nil {
			return Stats{}, nil, err
		}
		if ev.Seq < from {
			continue
		}
		if ev.Seq > to {
			break
		}
		out = append(out, ev)
	}
	st.Bytes = rd.BytesRead()
	return st, out, nil
}

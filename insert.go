package ontology

import "container/heap"

func (a *RangeAggregator) supplementOutput(key string, ts int64, val int64, row *retainedRow) Output {
	agg := a.aggregateForKey(key)
	low := ts - a.width
	high := ts
	sum := int64(0)
	cnt := int64(0)
	max := int64(0)
	hasMax := false
	a.lateWork += 2
	treapRange(agg.root, low, high, func(node *treapNode) bool {
		a.lateWork++
		for _, existing := range node.bucket.rows {
			sum += existing.val
			cnt++
			if !hasMax || existing.val > max {
				max = existing.val
				hasMax = true
			}
		}
		return true
	})
	a.addRetainedRows(key, ts, []*retainedRow{row})
	a.addSupplementRow(key, ts, row)
	sum += val
	cnt++
	if !hasMax || val > max {
		max = val
	}
	return Output{Key: []byte(key), TS: ts, Val: val, Sum: sum, Cnt: cnt, Max: max}
}

func (a *RangeAggregator) insertLocked(row Row) (Output, string, error) {
	key := string(row.Key)
	lateCutoff := a.wm - a.allowedLateness
	if row.TS <= lateCutoff {
		a.lateDropped++
		return Output{}, "late", nil
	}
	if row.TS <= a.wm {
		a.supplemented++
		lateRow := a.newRetainedRow(row.Val, -1, true)
		out := a.supplementOutput(key, row.TS, row.Val, lateRow)
		return out, "supplement", nil
	}
	if int64(a.buffer.Len()) >= a.capacity {
		return Output{}, "rejected", ErrInvalidArgument
	}
	a.seq++
	buffered := &bufferedRow{key: key, ts: row.TS, val: row.Val, seq: a.seq}
	heap.Push(a.buffer, buffered)
	return Output{}, "buffered", nil
}

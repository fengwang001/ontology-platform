package ontology

import "container/heap"

func (a *RangeAggregator) aggregateForKey(key string) *keyAggregate {
	agg := a.keys[key]
	if agg == nil {
		agg = &keyAggregate{
			buckets:           make(map[int64]*treapNode),
			supplementBuckets: make(map[int64]*treapNode),
			activeIDs:         make(map[int64]bool),
		}
		a.keys[key] = agg
	}
	return agg
}

func (a *RangeAggregator) addRetainedRows(key string, ts int64, rows []*retainedRow) {
	agg := a.aggregateForKey(key)
	node := agg.buckets[ts]
	if node == nil {
		node = &treapNode{ts: ts, priority: bucketPriority(ts), bucket: &retainedBucket{}}
		agg.root = treapInsert(agg.root, node)
		agg.buckets[ts] = node
		heap.Push(a.oldestBuckets, oldBucketEntry{key: key, ts: ts})
	}
	node.bucket.rows = append(node.bucket.rows, rows...)
	a.retainedCount += int64(len(rows))
	a.released += int64(len(rows))
}

func (a *RangeAggregator) newRetainedRow(val, seq int64, late bool) *retainedRow {
	a.nextRowID++
	return &retainedRow{id: a.nextRowID, val: val, seq: seq, late: late}
}

func (a *RangeAggregator) addSupplementRow(key string, ts int64, row *retainedRow) {
	agg := a.aggregateForKey(key)
	node := agg.supplementBuckets[ts]
	if node == nil {
		node = &treapNode{ts: ts, priority: bucketPriority(ts), bucket: &retainedBucket{}}
		agg.supplementRoot = treapInsert(agg.supplementRoot, node)
		agg.supplementBuckets[ts] = node
	}
	node.bucket.rows = append(node.bucket.rows, row)
}

func (a *RangeAggregator) pruneSupplements(key string, agg *keyAggregate, cutoff int64) {
	for agg.supplementRoot != nil {
		node := treapFirst(agg.supplementRoot)
		if node.ts > cutoff {
			return
		}
		delete(agg.supplementBuckets, node.ts)
		agg.supplementRoot = treapDelete(agg.supplementRoot, node.ts)
	}
}

func (a *RangeAggregator) pruneFrameMax(agg *keyAggregate) {
	for agg.maxHeap.Len() > 0 {
		top := agg.maxHeap[0]
		if agg.activeIDs[top.id] {
			break
		}
		heap.Pop(&agg.maxHeap)
		a.frameWork++
	}
}

func (a *RangeAggregator) enterFrame(key string, ts int64, row *retainedRow) {
	agg := a.keys[key]
	if agg.activeIDs[row.id] {
		return
	}
	heap.Push(&agg.expiring, &activeEntry{key: key, row: row, ts: ts, seq: row.seq})
	a.frameWork++
	agg.frameSum += row.val
	agg.frameCnt++
	agg.activeIDs[row.id] = true
	heap.Push(&agg.maxHeap, &frameEntry{id: row.id, key: key, ts: ts, val: row.val, seq: row.seq})
	a.frameWork++
	a.pruneFrameMax(agg)
}

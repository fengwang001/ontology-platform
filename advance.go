package ontology

import (
	"container/heap"
	"sort"
)

type releaseGroup struct {
	key   string
	ts    int64
	rows  []*retainedRow
	order []*bufferedRow
	sum   int64
	cnt   int64
	max   int64
}

func (a *RangeAggregator) releaseBuffered(watermark int64) []Output {
	var released []*bufferedRow
	for a.buffer.Len() > 0 && (*a.buffer)[0].ts <= watermark {
		released = append(released, heap.Pop(a.buffer).(*bufferedRow))
	}
	sort.Slice(released, func(i, j int) bool {
		if released[i].ts != released[j].ts {
			return released[i].ts < released[j].ts
		}
		return released[i].seq < released[j].seq
	})

	groupIndex := make(map[string]map[int64]int)
	var groups []*releaseGroup
	for _, buffered := range released {
		byTS := groupIndex[buffered.key]
		if byTS == nil {
			byTS = make(map[int64]int)
			groupIndex[buffered.key] = byTS
		}
		index, ok := byTS[buffered.ts]
		if !ok {
			index = len(groups)
			byTS[buffered.ts] = index
			groups = append(groups, &releaseGroup{key: buffered.key, ts: buffered.ts})
		}
		group := groups[index]
		group.rows = append(group.rows, a.newRetainedRow(buffered.val, buffered.seq, false))
		group.order = append(group.order, buffered)
	}
	sort.Slice(groups, func(i, j int) bool {
		if groups[i].ts != groups[j].ts {
			return groups[i].ts < groups[j].ts
		}
		return groups[i].key < groups[j].key
	})

	for start := 0; start < len(groups); {
		end := start + 1
		for end < len(groups) && groups[end].ts == groups[start].ts {
			end++
		}
		timeGroups := groups[start:end]
		for _, group := range timeGroups {
			a.addRetainedRows(group.key, group.ts, group.rows)
		}
		for _, group := range timeGroups {
			agg := a.aggregateForKey(group.key)
			a.evictActiveBefore(agg, group.ts-a.width)
			var supplementNodes []*treapNode
			treapRange(agg.supplementRoot, group.ts-a.width, group.ts, func(node *treapNode) bool {
				supplementNodes = append(supplementNodes, node)
				return true
			})
			for _, node := range supplementNodes {
				for _, row := range node.bucket.rows {
					if !agg.activeIDs[row.id] {
						a.enterFrame(group.key, node.ts, row)
					}
				}
				delete(agg.supplementBuckets, node.ts)
				agg.supplementRoot = treapDelete(agg.supplementRoot, node.ts)
			}
			for _, row := range group.rows {
				a.enterFrame(group.key, group.ts, row)
			}
			group.sum = agg.frameSum
			group.cnt = agg.frameCnt
			if agg.maxHeap.Len() > 0 {
				group.max = agg.maxHeap[0].val
			}
		}
		start = end
	}

	outputs := make([]Output, 0, len(released))
	byKeyTS := make(map[string]map[int64]*releaseGroup)
	for _, group := range groups {
		byTS := byKeyTS[group.key]
		if byTS == nil {
			byTS = make(map[int64]*releaseGroup)
			byKeyTS[group.key] = byTS
		}
		byTS[group.ts] = group
	}
	for _, buffered := range released {
		group := byKeyTS[buffered.key][buffered.ts]
		outputs = append(outputs, Output{Key: []byte(group.key), TS: group.ts, Val: buffered.val, Sum: group.sum, Cnt: group.cnt, Max: group.max})
	}
	return outputs
}

func (a *RangeAggregator) evictActiveBefore(agg *keyAggregate, cutoff int64) {
	for agg.expiring.Len() > 0 && agg.expiring[0].ts < cutoff {
		top := heap.Pop(&agg.expiring).(*activeEntry)
		if !agg.activeIDs[top.row.id] {
			continue
		}
		delete(agg.activeIDs, top.row.id)
		agg.frameSum -= top.row.val
		agg.frameCnt--
		a.frameWork++
		a.pruneFrameMax(agg)
	}
}

func (a *RangeAggregator) cleanupRetained(cutoff int64) {
	for key, agg := range a.keys {
		a.pruneSupplements(key, agg, cutoff)
	}
	for a.oldestBuckets.Len() > 0 {
		entry := (*a.oldestBuckets)[0]
		agg := a.keys[entry.key]
		node := agg.buckets[entry.ts]
		if node == nil || node.ts != entry.ts {
			heap.Pop(a.oldestBuckets)
			continue
		}
		if node.ts > cutoff {
			break
		}
		heap.Pop(a.oldestBuckets)
		for _, row := range node.bucket.rows {
			if agg.activeIDs[row.id] {
				delete(agg.activeIDs, row.id)
				agg.frameSum -= row.val
				agg.frameCnt--
				a.frameWork++
				a.pruneFrameMax(agg)
			}
			a.retainedCount--
		}
		agg.root = treapDelete(agg.root, node.ts)
		delete(agg.buckets, node.ts)
	}
}

func (a *RangeAggregator) advanceLocked(watermark int64) []Output {
	if watermark == a.wm {
		return nil
	}
	outputs := a.releaseBuffered(watermark)
	a.wm = watermark
	a.cleanupRetained(watermark - a.width - a.allowedLateness)
	return outputs
}

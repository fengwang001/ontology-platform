package ontology

import "container/heap"

type bufferedRow struct {
	key string
	ts  int64
	val int64
	seq int64
}

type bufferHeap []*bufferedRow

func (h bufferHeap) Len() int { return len(h) }

func (h bufferHeap) Less(i, j int) bool {
	if h[i].ts != h[j].ts {
		return h[i].ts < h[j].ts
	}
	return h[i].seq < h[j].seq
}

func (h bufferHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }

func (h *bufferHeap) Push(value any) {
	*h = append(*h, value.(*bufferedRow))
}

func (h *bufferHeap) Pop() any {
	old := *h
	last := len(old) - 1
	value := old[last]
	old[last] = nil
	*h = old[:last]
	return value
}

type retainedRow struct {
	id     int64
	val    int64
	seq    int64
	late   bool
	active bool
}

type retainedBucket struct {
	rows []*retainedRow
}

type treapNode struct {
	ts       int64
	priority uint64
	left     *treapNode
	right    *treapNode
	bucket   *retainedBucket
}

type activeEntry struct {
	key string
	row *retainedRow
	ts  int64
	seq int64
}

type activeExpireHeap []*activeEntry

func (h activeExpireHeap) Len() int { return len(h) }

func (h activeExpireHeap) Less(i, j int) bool {
	if h[i].ts != h[j].ts {
		return h[i].ts < h[j].ts
	}
	return h[i].seq < h[j].seq
}

func (h activeExpireHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }

func (h *activeExpireHeap) Push(value any) {
	*h = append(*h, value.(*activeEntry))
}

func (h *activeExpireHeap) Pop() any {
	old := *h
	last := len(old) - 1
	value := old[last]
	old[last] = nil
	*h = old[:last]
	return value
}

type frameEntry struct {
	id  int64
	key string
	ts  int64
	val int64
	seq int64
}

type frameMaxHeap []*frameEntry

func (h frameMaxHeap) Len() int { return len(h) }

func (h frameMaxHeap) Less(i, j int) bool {
	if h[i].val != h[j].val {
		return h[i].val > h[j].val
	}
	return h[i].seq < h[j].seq
}

func (h frameMaxHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }

func (h *frameMaxHeap) Push(value any) {
	*h = append(*h, value.(*frameEntry))
}

func (h *frameMaxHeap) Pop() any {
	old := *h
	last := len(old) - 1
	value := old[last]
	old[last] = nil
	*h = old[:last]
	return value
}

type keyAggregate struct {
	root              *treapNode
	buckets           map[int64]*treapNode
	supplementRoot    *treapNode
	supplementBuckets map[int64]*treapNode
	frameSum          int64
	frameCnt          int64
	expiring          activeExpireHeap
	maxHeap           frameMaxHeap
	activeIDs         map[int64]bool
}

type oldBucketEntry struct {
	key string
	ts  int64
}

type oldBucketHeap []oldBucketEntry

func (h oldBucketHeap) Len() int { return len(h) }

func (h oldBucketHeap) Less(i, j int) bool {
	if h[i].ts != h[j].ts {
		return h[i].ts < h[j].ts
	}
	return h[i].key < h[j].key
}

func (h oldBucketHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }

func (h *oldBucketHeap) Push(value any) {
	*h = append(*h, value.(oldBucketEntry))
}

func (h *oldBucketHeap) Pop() any {
	old := *h
	last := len(old) - 1
	value := old[last]
	*h = old[:last]
	return value
}

var _ heap.Interface = (*bufferHeap)(nil)
var _ heap.Interface = (*activeExpireHeap)(nil)
var _ heap.Interface = (*frameMaxHeap)(nil)
var _ heap.Interface = (*oldBucketHeap)(nil)

package crew

import "sort"

type bucketIndex struct {
	width int
	cells map[int]map[int]*storedDuty
}

func newBucketIndex(width int) *bucketIndex {
	if width < 1 {
		width = 1
	}
	return &bucketIndex{width: width, cells: make(map[int]map[int]*storedDuty)}
}

func (b *bucketIndex) bucket(at int) int {
	if at >= 0 {
		return at / b.width
	}
	return -((-at + b.width - 1) / b.width)
}

func (b *bucketIndex) add(duty *storedDuty) {
	key := b.bucket(duty.Start)
	cell := b.cells[key]
	if cell == nil {
		cell = make(map[int]*storedDuty)
		b.cells[key] = cell
	}
	cell[duty.Start] = duty
}

func (b *bucketIndex) remove(duty *storedDuty) {
	key := b.bucket(duty.Start)
	delete(b.cells[key], duty.Start)
	if len(b.cells[key]) == 0 {
		delete(b.cells, key)
	}
}

func (b *bucketIndex) rangeDuties(from, until int) []*storedDuty {
	first := b.bucket(from)
	last := b.bucket(until - 1)
	result := make([]*storedDuty, 0)
	for key := first; key <= last; key++ {
		starts := make([]int, 0, len(b.cells[key]))
		for start := range b.cells[key] {
			if start >= from && start < until {
				starts = append(starts, start)
			}
		}
		sort.Ints(starts)
		for _, start := range starts {
			result = append(result, b.cells[key][start])
		}
	}
	return result
}

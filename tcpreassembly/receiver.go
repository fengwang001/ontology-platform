package tcpreassembly

import (
	"errors"
	"fmt"
	"sort"
	"sync"
)

const (
	minWindow     uint32 = 1
	maxWindow     uint32 = 1 << 30
	minMaxSack    uint32 = 1
	maxMaxSack    uint32 = 4
	minMaxBlocks  uint32 = 1
	maxMaxBlocks  uint32 = 64
	minSegmentLen        = 1
	maxSegmentLen        = 65535
)

var ErrInvalidArgument = errors.New("invalid tcp reassembly argument")

type Block struct {
	Start uint32
	End   uint32
	Stamp uint64
	Data  []byte
}

type Result struct {
	AckNo         uint32
	Delivered     uint32
	DeliveredData []byte
	Sack          []Block
	Dropped       bool
}

type Receiver struct {
	mu        sync.RWMutex
	rcvNxt    uint32
	window    uint32
	maxSack   int
	maxBlocks int
	clk       uint64
	blocks    []Block
}

type interval struct {
	start int64
	end   int64
}

type relBlock struct {
	interval
	stamp uint64
	data  []byte
}

type newRange struct {
	interval
	data []byte
	base int64
}

func NewReceiver(rcvNxt uint32, window uint32, maxSack uint32, maxBlocks uint32) (*Receiver, error) {
	if window < minWindow || window > maxWindow {
		return nil, fmt.Errorf("window %d out of range [%d,%d]", window, minWindow, maxWindow)
	}
	if maxSack < minMaxSack || maxSack > maxMaxSack {
		return nil, fmt.Errorf("maxSack %d out of range [%d,%d]", maxSack, minMaxSack, maxMaxSack)
	}
	if maxBlocks < minMaxBlocks || maxBlocks > maxMaxBlocks {
		return nil, fmt.Errorf("maxBlocks %d out of range [%d,%d]", maxBlocks, minMaxBlocks, maxMaxBlocks)
	}
	return &Receiver{
		rcvNxt:    rcvNxt,
		window:    window,
		maxSack:   int(maxSack),
		maxBlocks: int(maxBlocks),
	}, nil
}

func (r *Receiver) OnSegment(seq uint32, data []byte) (Result, error) {
	length := len(data)
	if length < minSegmentLen || length > maxSegmentLen {
		return Result{AckNo: r.RcvNxt(), Sack: []Block{}}, ErrInvalidArgument
	}

	segment := append([]byte(nil), data...)

	r.mu.Lock()
	defer r.mu.Unlock()

	oldRcvNxt := r.rcvNxt
	lo := int64(int32(seq - oldRcvNxt))
	hi := lo + int64(length)
	windowEnd := int64(r.window)

	duplicateRel := make([]interval, 0, 2)
	if lo < 0 && hi > 0 {
		duplicateRel = append(duplicateRel, interval{lo, min64(hi, 0)})
	}

	candidateLo := max64(lo, 0)
	candidateHi := min64(hi, windowEnd)
	candidate := interval{candidateLo, candidateHi}

	oldBlocks := r.snapshotRelativeLocked(oldRcvNxt)
	var covered []interval
	for _, block := range oldBlocks {
		if overlap, ok := intersect(candidate, block.interval); ok {
			covered = append(covered, overlap)
			duplicateRel = append(duplicateRel, overlap)
		}
	}
	covered = mergeIntervals(covered)

	if candidate.start < candidate.end && !hasNewBytes(candidate, covered, segment, lo) {
		return r.resultLocked(oldRcvNxt, 0, nil, false, firstMerged(duplicateRel), oldRcvNxt), nil
	}

	newRanges := subtractIntervals(candidate, covered, segment, lo)
	dropped := false
	if candidate.start < candidate.end && len(newRanges) > 0 && candidate.start > 0 &&
		len(oldBlocks) >= r.maxBlocks && !touchesAnyBlock(candidate, oldBlocks) {
		dropped = true
		newRanges = nil
	}

	if len(newRanges) == 0 {
		return r.resultLocked(oldRcvNxt, 0, nil, dropped, firstMerged(duplicateRel), oldRcvNxt), nil
	}

	components := buildComponents(oldBlocks, newRanges)
	var deliveredRel *interval
	var deliveredData []byte
	for idx := range components {
		if components[idx].interval.start == 0 && components[idx].hasNew {
			deliveredRel = &components[idx].interval
			deliveredData = components[idx].data
			break
		}
	}

	var deliveredLen uint32
	if deliveredRel != nil {
		deliveredLen = uint32(deliveredRel.end - deliveredRel.start)
		r.rcvNxt = oldRcvNxt + uint32(deliveredRel.end)
		oldBlocks = removeIntersecting(oldBlocks, *deliveredRel)
		newRanges = removeIntersectingRanges(newRanges, *deliveredRel)
	}

	touchedOutOfOrder := false
	for _, nr := range newRanges {
		if nr.start < nr.end {
			touchedOutOfOrder = true
			break
		}
	}
	if touchedOutOfOrder {
		r.clk++
	}

	nextBlocks := make([]relBlock, 0, len(oldBlocks)+1)
	if len(newRanges) > 0 {
		mergedComponents := buildComponents(oldBlocks, newRanges)
		consumedBlocks := make(map[int]bool)
		for _, component := range mergedComponents {
			block := relBlock{interval: component.interval, stamp: 0}
			if component.hasNew {
				block.stamp = r.clk
			}
			for idx, old := range oldBlocks {
				if intervalsIntersect(block.interval, old.interval) {
					block.stamp = old.stamp
					if component.hasNew {
						block.stamp = r.clk
					}
					consumedBlocks[idx] = true
				}
			}
			block.data = component.data
			nextBlocks = append(nextBlocks, block)
		}
		for idx, block := range oldBlocks {
			if !consumedBlocks[idx] {
				nextBlocks = append(nextBlocks, block)
			}
		}
	} else {
		nextBlocks = append(nextBlocks, oldBlocks...)
	}

	sort.Slice(nextBlocks, func(i, j int) bool {
		return nextBlocks[i].start < nextBlocks[j].start
	})
	if r.rcvNxt != oldRcvNxt {
		advance := int64(int32(r.rcvNxt - oldRcvNxt))
		for i := range nextBlocks {
			nextBlocks[i].start -= advance
			nextBlocks[i].end -= advance
		}
	}
	r.blocks = absoluteBlocks(r.rcvNxt, nextBlocks)

	return r.resultLocked(oldRcvNxt, deliveredLen, deliveredData, dropped, firstMerged(duplicateRel), oldRcvNxt), nil
}

func (r *Receiver) RcvNxt() uint32 {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.rcvNxt
}

func (r *Receiver) Blocks() []Block {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return cloneBlocks(r.blocks)
}

func (r *Receiver) SackBlocks() []Block {
	r.mu.RLock()
	defer r.mu.RUnlock()
	blocks := cloneBlocks(r.blocks)
	sort.SliceStable(blocks, func(i, j int) bool {
		if blocks[i].Stamp != blocks[j].Stamp {
			return blocks[i].Stamp > blocks[j].Stamp
		}
		return blocks[i].Start < blocks[j].Start
	})
	return blocks
}

func (r *Receiver) snapshotRelativeLocked(base uint32) []relBlock {
	blocks := make([]relBlock, 0, len(r.blocks))
	for _, block := range r.blocks {
		start := int64(int32(block.Start - base))
		end := start + int64(len(block.Data))
		blocks = append(blocks, relBlock{
			interval: interval{start, end},
			stamp:    block.Stamp,
			data:     append([]byte(nil), block.Data...),
		})
	}
	return blocks
}

func (r *Receiver) resultLocked(oldRcvNxt uint32, delivered uint32, deliveredData []byte, dropped bool, dsack *interval, dsackBase uint32) Result {
	blocks := cloneBlocks(r.blocks)
	sort.SliceStable(blocks, func(i, j int) bool {
		if blocks[i].Stamp != blocks[j].Stamp {
			return blocks[i].Stamp > blocks[j].Stamp
		}
		return blocks[i].Start < blocks[j].Start
	})
	result := Result{
		AckNo:         r.rcvNxt,
		Delivered:     delivered,
		DeliveredData: deliveredData,
		Sack:          make([]Block, 0, r.maxSack),
		Dropped:       dropped,
	}

	remaining := r.maxSack
	if dsack != nil && dsack.start < dsack.end {
		result.Sack = append(result.Sack, Block{
			Start: dsackBase + uint32(dsack.start),
			End:   dsackBase + uint32(dsack.end),
		})
		remaining--
	}
	if remaining > 0 {
		if len(blocks) < remaining {
			remaining = len(blocks)
		}
		result.Sack = append(result.Sack, blocks[:remaining]...)
	}
	return result
}

type component struct {
	interval
	data   []byte
	hasNew bool
}

func buildComponents(blocks []relBlock, news []newRange) []component {
	bounds := make([]interval, 0, len(blocks)+len(news))
	for _, block := range blocks {
		bounds = append(bounds, block.interval)
	}
	for _, nr := range news {
		bounds = append(bounds, nr.interval)
	}
	merged := mergeIntervals(bounds)
	components := make([]component, 0, len(merged))
	for _, bound := range merged {
		data := make([]byte, bound.end-bound.start)
		for _, nr := range news {
			if overlap, ok := intersect(bound, nr.interval); ok {
				copy(data[overlap.start-bound.start:overlap.end-bound.start],
					nr.data[overlap.start-nr.base:overlap.end-nr.base])
			}
		}
		for _, block := range blocks {
			if overlap, ok := intersect(bound, block.interval); ok {
				copy(data[overlap.start-bound.start:overlap.end-bound.start],
					block.data[overlap.start-block.start:overlap.end-block.start])
			}
		}
		hasNew := false
		for _, nr := range news {
			if intervalsIntersect(bound, nr.interval) {
				hasNew = true
				break
			}
		}
		components = append(components, component{interval: bound, data: data, hasNew: hasNew})
	}
	return components
}

func absoluteBlocks(base uint32, blocks []relBlock) []Block {
	result := make([]Block, 0, len(blocks))
	for _, block := range blocks {
		result = append(result, Block{
			Start: base + uint32(block.start),
			End:   base + uint32(block.end),
			Stamp: block.stamp,
			Data:  append([]byte(nil), block.data...),
		})
	}
	return result
}

func removeIntersecting(blocks []relBlock, target interval) []relBlock {
	result := make([]relBlock, 0, len(blocks))
	for _, block := range blocks {
		if !intervalsIntersect(block.interval, target) {
			result = append(result, block)
		}
	}
	return result
}

func removeIntersectingRanges(ranges []newRange, target interval) []newRange {
	result := make([]newRange, 0, len(ranges))
	for _, nr := range ranges {
		if !intervalsIntersect(nr.interval, target) {
			result = append(result, nr)
		}
	}
	return result
}

func touchesAnyBlock(target interval, blocks []relBlock) bool {
	for _, block := range blocks {
		if target.start <= block.end && block.start <= target.end {
			return true
		}
	}
	return false
}

func hasNewBytes(candidate interval, covered []interval, data []byte, base int64) bool {
	return len(subtractIntervals(candidate, covered, data, base)) > 0
}

func subtractIntervals(target interval, covered []interval, data []byte, base int64) []newRange {
	if target.start >= target.end {
		return nil
	}
	result := []newRange{}
	cursor := target.start
	for _, part := range covered {
		if part.end <= cursor {
			continue
		}
		if part.start >= target.end {
			break
		}
		if part.start > cursor {
			result = append(result, newRange{
				interval: interval{cursor, min64(part.start, target.end)},
				data:     data,
				base:     base,
			})
		}
		cursor = max64(cursor, min64(part.end, target.end))
	}
	if cursor < target.end {
		result = append(result, newRange{
			interval: interval{cursor, target.end},
			data:     data,
			base:     base,
		})
	}
	return result
}

func mergeIntervals(input []interval) []interval {
	if len(input) == 0 {
		return nil
	}
	intervals := append([]interval(nil), input...)
	sort.Slice(intervals, func(i, j int) bool {
		if intervals[i].start != intervals[j].start {
			return intervals[i].start < intervals[j].start
		}
		return intervals[i].end < intervals[j].end
	})
	result := []interval{intervals[0]}
	for _, current := range intervals[1:] {
		last := &result[len(result)-1]
		if current.start <= last.end {
			if current.end > last.end {
				last.end = current.end
			}
		} else {
			result = append(result, current)
		}
	}
	return result
}

func firstMerged(intervals []interval) *interval {
	merged := mergeIntervals(intervals)
	if len(merged) == 0 {
		return nil
	}
	return &merged[0]
}

func intersect(a interval, b interval) (interval, bool) {
	start := max64(a.start, b.start)
	end := min64(a.end, b.end)
	if start >= end {
		return interval{}, false
	}
	return interval{start, end}, true
}

func intervalsIntersect(a interval, b interval) bool {
	_, ok := intersect(a, b)
	return ok
}

func cloneBlocks(blocks []Block) []Block {
	result := make([]Block, len(blocks))
	for i, block := range blocks {
		result[i] = Block{
			Start: block.Start,
			End:   block.End,
			Stamp: block.Stamp,
			Data:  append([]byte(nil), block.Data...),
		}
	}
	return result
}

func min64(a int64, b int64) int64 {
	if a < b {
		return a
	}
	return b
}

func max64(a int64, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

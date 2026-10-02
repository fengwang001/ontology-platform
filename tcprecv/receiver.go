package tcprecv

import (
	"errors"
	"sort"
	"sync"
)

var ErrInvalidArgument = errors.New("tcprecv: invalid argument")

type Block struct {
	Start uint32
	End   uint32
	Data  []byte
	Stamp uint64
}

type Result struct {
	AckNo         uint32
	Delivered     int
	DeliveredData []byte
	Sack          []Block
	Dropped       bool
}

type Receiver struct {
	mu        sync.RWMutex
	rcvNxt    uint32
	window    int64
	maxSack   int
	maxBlocks int
	clk       uint64
	blocks    []Block
	delivered []byte
}

type relInterval struct {
	start int64
	end   int64
}

func New(rcvNxt uint32, window, maxSack, maxBlocks int) (*Receiver, error) {
	if window < 1 || window > 1<<30 || maxSack < 1 || maxSack > 4 || maxBlocks < 1 || maxBlocks > 64 {
		return nil, ErrInvalidArgument
	}
	return &Receiver{
		rcvNxt:    rcvNxt,
		window:    int64(window),
		maxSack:   maxSack,
		maxBlocks: maxBlocks,
	}, nil
}

func (r *Receiver) OnSegment(seq uint32, data []byte) (Result, error) {
	if len(data) < 1 || len(data) > 65535 {
		return Result{}, ErrInvalidArgument
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	segment := append([]byte(nil), data...)
	base := r.rcvNxt
	lo := int64(int32(seq - base))
	hi := lo + int64(len(segment))
	candLo := maxInt64(lo, 0)
	candHi := minInt64(hi, r.window)

	intervals := make([]relInterval, 0, len(r.blocks)+1)
	if lo < 0 {
		oldEnd := minInt64(hi, 0)
		if lo < oldEnd {
			intervals = append(intervals, relInterval{lo, oldEnd})
		}
	}
	if candLo < candHi {
		for _, block := range r.blocks {
			bs := relOffset(base, block.Start)
			be := relOffset(base, block.End)
			ovStart := maxInt64(bs, candLo)
			ovEnd := minInt64(be, candHi)
			if ovStart < ovEnd {
				intervals = append(intervals, relInterval{ovStart, ovEnd})
			}
		}
	}
	duplicate, hasDuplicate := earliestMergedInterval(intervals)

	dropped := false
	deliveredCount := 0
	var deliveredBytes []byte

	if candLo < candHi {
		covered := make([]relInterval, 0, len(r.blocks))
		for _, block := range r.blocks {
			bs := relOffset(base, block.Start)
			be := relOffset(base, block.End)
			ovStart := maxInt64(bs, candLo)
			ovEnd := minInt64(be, candHi)
			if ovStart < ovEnd {
				covered = append(covered, relInterval{ovStart, ovEnd})
			}
		}
		newRanges := subtractRanges(relInterval{candLo, candHi}, covered)

		if len(newRanges) > 0 {
			if candLo == 0 {
				frontier, consumed, delivered := r.deliverInOrder(base, lo, segment, candHi)
				deliveredCount = int(frontier)
				deliveredBytes = delivered
				r.removeBlocks(consumed)
				sortBlocks(r.blocks, base+uint32(frontier))
				r.rcvNxt = base + uint32(frontier)
				r.delivered = append(r.delivered, delivered...)
			} else {
				touched := make(map[int]struct{})
				for i, block := range r.blocks {
					bs := relOffset(base, block.Start)
					be := relOffset(base, block.End)
					if bs <= candHi && be >= candLo {
						touched[i] = struct{}{}
					}
				}

				if len(touched) == 0 && len(r.blocks) >= r.maxBlocks {
					dropped = true
				} else {
					mergedStart := candLo
					mergedEnd := candHi
					for i := range touched {
						bs := relOffset(base, r.blocks[i].Start)
						be := relOffset(base, r.blocks[i].End)
						mergedStart = minInt64(mergedStart, bs)
						mergedEnd = maxInt64(mergedEnd, be)
					}

					merged := make([]byte, mergedEnd-mergedStart)
					for i := range touched {
						bs := relOffset(base, r.blocks[i].Start)
						copy(merged[bs-mergedStart:], r.blocks[i].Data)
					}
					for _, free := range newRanges {
						copySegmentRange(merged, mergedStart, segment, seq, base, free.start, free.end)
					}

					nextBlocks := make([]Block, 0, len(r.blocks)-len(touched)+1)
					for i := range r.blocks {
						if _, ok := touched[i]; !ok {
							nextBlocks = append(nextBlocks, r.blocks[i])
						}
					}
					r.clk++
					nextBlocks = append(nextBlocks, Block{
						Start: base + uint32(mergedStart),
						End:   base + uint32(mergedEnd),
						Data:  merged,
						Stamp: r.clk,
					})
					sortBlocks(nextBlocks, r.rcvNxt)
					r.blocks = nextBlocks
				}
			}
		}
	}

	return r.currentResult(base, dropped, deliveredCount, deliveredBytes, duplicate, hasDuplicate), nil
}

func (r *Receiver) deliverInOrder(base uint32, segmentStart int64, segment []byte, candEnd int64) (int64, map[int]struct{}, []byte) {
	consumed := make(map[int]struct{})
	frontier := int64(0)
	delivered := make([]byte, 0, candEnd)

	for frontier < candEnd {
		next := -1
		for i, block := range r.blocks {
			if _, used := consumed[i]; used {
				continue
			}
			bs := relOffset(base, block.Start)
			be := relOffset(base, block.End)
			if bs >= frontier && bs < candEnd && be > frontier {
				next = i
				break
			}
		}

		if next == -1 {
			delivered = appendSegmentRange(delivered, segment, segmentStart, frontier, candEnd)
			frontier = candEnd
			break
		}

		block := r.blocks[next]
		bs := relOffset(base, block.Start)
		be := relOffset(base, block.End)
		if bs > frontier {
			delivered = appendSegmentRange(delivered, segment, segmentStart, frontier, bs)
		}
		delivered = append(delivered, block.Data...)
		frontier = be
		consumed[next] = struct{}{}
	}

	for {
		next := -1
		for i, block := range r.blocks {
			if _, used := consumed[i]; used {
				continue
			}
			if relOffset(base, block.Start) == frontier {
				next = i
				break
			}
		}
		if next == -1 {
			break
		}
		delivered = append(delivered, r.blocks[next].Data...)
		frontier = relOffset(base, r.blocks[next].End)
		consumed[next] = struct{}{}
	}

	return frontier, consumed, delivered
}

func (r *Receiver) removeBlocks(remove map[int]struct{}) {
	next := make([]Block, 0, len(r.blocks)-len(remove))
	for i := range r.blocks {
		if _, ok := remove[i]; !ok {
			next = append(next, r.blocks[i])
		}
	}
	r.blocks = next
}

func (r *Receiver) currentResult(dsackBase uint32, dropped bool, deliveredCount int, delivered []byte, duplicate relInterval, hasDuplicate bool) Result {
	result := Result{
		AckNo:         r.rcvNxt,
		Delivered:     deliveredCount,
		DeliveredData: cloneBytes(delivered),
		Dropped:       dropped,
		Sack:          make([]Block, 0, r.maxSack),
	}

	if hasDuplicate {
		result.Sack = append(result.Sack, Block{
			Start: dsackBase + uint32(duplicate.start),
			End:   dsackBase + uint32(duplicate.end),
		})
	}

	limit := r.maxSack
	if hasDuplicate {
		limit--
	}
	ordered := append([]Block(nil), r.blocks...)
	sort.SliceStable(ordered, func(i, j int) bool {
		return ordered[i].Stamp > ordered[j].Stamp
	})
	for i := 0; i < len(ordered) && i < limit; i++ {
		block := ordered[i]
		block.Data = cloneBytes(block.Data)
		result.Sack = append(result.Sack, block)
	}
	return result
}

func (r *Receiver) RcvNxt() uint32 {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.rcvNxt
}

func (r *Receiver) Blocks() []Block {
	r.mu.RLock()
	defer r.mu.RUnlock()
	blocks := make([]Block, len(r.blocks))
	for i, block := range r.blocks {
		block.Data = cloneBytes(block.Data)
		blocks[i] = block
	}
	return blocks
}

func (r *Receiver) DeliveredData() []byte {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return cloneBytes(r.delivered)
}

func relOffset(base, value uint32) int64 {
	return int64(int32(value - base))
}

func earliestMergedInterval(intervals []relInterval) (relInterval, bool) {
	if len(intervals) == 0 {
		return relInterval{}, false
	}
	sort.Slice(intervals, func(i, j int) bool {
		if intervals[i].start == intervals[j].start {
			return intervals[i].end < intervals[j].end
		}
		return intervals[i].start < intervals[j].start
	})
	merged := []relInterval{intervals[0]}
	for _, in := range intervals[1:] {
		last := &merged[len(merged)-1]
		if in.start <= last.end {
			last.end = maxInt64(last.end, in.end)
		} else {
			merged = append(merged, in)
		}
	}
	return merged[0], true
}

func subtractRanges(total relInterval, covered []relInterval) []relInterval {
	if len(covered) == 0 {
		return []relInterval{total}
	}
	sort.Slice(covered, func(i, j int) bool {
		if covered[i].start == covered[j].start {
			return covered[i].end < covered[j].end
		}
		return covered[i].start < covered[j].start
	})

	result := make([]relInterval, 0, len(covered)+1)
	frontier := total.start
	for _, in := range covered {
		start := maxInt64(in.start, total.start)
		end := minInt64(in.end, total.end)
		if start >= total.end || end <= total.start {
			continue
		}
		if frontier < start {
			result = append(result, relInterval{frontier, start})
		}
		frontier = maxInt64(frontier, end)
	}
	if frontier < total.end {
		result = append(result, relInterval{frontier, total.end})
	}
	return result
}

func appendSegmentRange(dst []byte, segment []byte, segmentStart, relStart, relEnd int64) []byte {
	if relStart >= relEnd {
		return dst
	}
	start := relStart - segmentStart
	end := relEnd - segmentStart
	return append(dst, segment[start:end]...)
}

func copySegmentRange(dst []byte, dstStart int64, segment []byte, seq, base uint32, relStart, relEnd int64) {
	if relStart >= relEnd {
		return
	}
	segmentStart := relOffset(base, seq)
	copy(dst[relStart-dstStart:relEnd-dstStart], segment[relStart-segmentStart:relEnd-segmentStart])
}

func sortBlocks(blocks []Block, base uint32) {
	sort.Slice(blocks, func(i, j int) bool {
		return relOffset(base, blocks[i].Start) < relOffset(base, blocks[j].Start)
	})
}

func cloneBytes(in []byte) []byte {
	if in == nil {
		return nil
	}
	return append([]byte(nil), in...)
}

func minInt64(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}

func maxInt64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

package tcprecv

import (
	"fmt"
	"math/rand"
	"testing"
)

type naiveResult struct {
	ack       uint32
	delivered int
	sack      []Block
	dropped   bool
}

type naiveReceiver struct {
	base      uint32
	window    int64
	maxSack   int
	maxBlocks int
	clk       uint64
	content   map[int64]byte
	blocks    []simBlock
	delivered []byte
}

type simBlock struct {
	start int64
	end   int64
	stamp uint64
}

func newNaive(rcvNxt uint32, window, maxSack, maxBlocks int) *naiveReceiver {
	return &naiveReceiver{
		base:      rcvNxt,
		window:    int64(window),
		maxSack:   maxSack,
		maxBlocks: maxBlocks,
		content:   make(map[int64]byte),
	}
}

func simRel(base, value uint32) int64 {
	return int64(int32(value - base))
}

func simIntervalsFor(base, start, end uint32, blocks []simBlock) []simBlock {
	lo := simRel(base, start)
	hi := lo + int64(end-start)
	result := make([]simBlock, 0, len(blocks)+1)
	if lo < 0 {
		oldEnd := hi
		if oldEnd > 0 {
			oldEnd = 0
		}
		if lo < oldEnd {
			result = append(result, simBlock{lo, oldEnd, 0})
		}
	}
	for _, block := range blocks {
		ovStart := block.start
		if ovStart < 0 {
			ovStart = 0
		}
		ovEnd := block.end
		if ovEnd < 0 {
			ovEnd = 0
		}
		if lo > ovStart {
			ovStart = lo
		}
		if hi < ovEnd {
			ovEnd = hi
		}
		if ovStart < ovEnd {
			result = append(result, simBlock{ovStart, ovEnd, 0})
		}
	}
	return mergeSimIntervals(result)
}

func mergeSimIntervals(intervals []simBlock) []simBlock {
	if len(intervals) == 0 {
		return nil
	}
	for i := 0; i < len(intervals); i++ {
		for j := i + 1; j < len(intervals); j++ {
			if intervals[j].start < intervals[i].start {
				intervals[i], intervals[j] = intervals[j], intervals[i]
			}
		}
	}
	merged := []simBlock{intervals[0]}
	for _, in := range intervals[1:] {
		last := &merged[len(merged)-1]
		if in.start <= last.end {
			if in.end > last.end {
				last.end = in.end
			}
		} else {
			merged = append(merged, in)
		}
	}
	return merged
}

func (n *naiveReceiver) onSegment(seq uint32, data []byte) naiveResult {
	lo := simRel(n.base, seq)
	hi := lo + int64(len(data))
	candLo := lo
	if candLo < 0 {
		candLo = 0
	}
	candHi := hi
	if candHi > n.window {
		candHi = n.window
	}
	preBase := n.base
	beforeDup := mergeSimIntervals(simIntervalsFor(preBase, seq, seq+uint32(len(data)), n.blocks))

	dropped := false
	delivered := 0
	reason := "no-candidate"

	if candLo < candHi {
		hasNew := false
		for pos := candLo; pos < candHi; pos++ {
			if _, exists := n.content[pos]; !exists {
				hasNew = true
				break
			}
		}

		touches := false
		if candLo == 0 {
			touches = true
		}
		for _, block := range n.blocks {
			if block.start <= candHi && block.end >= candLo {
				touches = true
				break
			}
		}

		if hasNew && !touches && len(n.blocks) >= n.maxBlocks {
			dropped = true
			reason = "full-and-isolated"
		} else {
			newPositions := 0
			for pos := candLo; pos < candHi; pos++ {
				if _, exists := n.content[pos]; !exists {
					n.content[pos] = data[pos-lo]
					newPositions++
				}
			}

			if candLo == 0 {
				frontier := int64(0)
				for {
					if _, exists := n.content[frontier]; !exists {
						break
					}
					n.delivered = append(n.delivered, n.content[frontier])
					delete(n.content, frontier)
					frontier++
					delivered++
				}
				remaining := make([]simBlock, 0, len(n.blocks))
				for _, block := range n.blocks {
					if block.end <= frontier {
						continue
					}
					remaining = append(remaining, simBlock{block.start - frontier, block.end - frontier, block.stamp})
				}
				n.blocks = remaining
				n.base += uint32(frontier)
				shiftedContent := make(map[int64]byte, len(n.content))
				for pos, value := range n.content {
					shiftedContent[pos-frontier] = value
				}
				n.content = shiftedContent
				reason = fmt.Sprintf("in-order=%d", frontier)
			} else if newPositions > 0 {
				mergedStart := candLo
				mergedEnd := candHi
				kept := make([]simBlock, 0, len(n.blocks))
				for _, block := range n.blocks {
					if block.start <= candHi && block.end >= candLo {
						if block.start < mergedStart {
							mergedStart = block.start
						}
						if block.end > mergedEnd {
							mergedEnd = block.end
						}
					} else {
						kept = append(kept, block)
					}
				}
				n.clk++
				n.blocks = append(kept, simBlock{mergedStart, mergedEnd, n.clk})
				reason = fmt.Sprintf("merge=[%d,%d) stamp=%d", mergedStart, mergedEnd, n.clk)
			} else {
				reason = "fully-duplicate"
			}
		}
	} else if hi <= 0 {
		reason = "old-data-only"
	} else {
		reason = "window-right-trimmed-empty"
	}

	result := n.currentResult(preBase, dropped, delivered, beforeDup)
	result.dropped = dropped
	_ = reason
	return result
}

func (n *naiveReceiver) currentResult(dsackBase uint32, dropped bool, delivered int, dup []simBlock) naiveResult {
	result := naiveResult{
		ack:       n.base,
		delivered: delivered,
		dropped:   dropped,
		sack:      make([]Block, 0, n.maxSack),
	}
	if len(dup) > 0 {
		result.sack = append(result.sack, Block{
			Start: dsackBase + uint32(dup[0].start),
			End:   dsackBase + uint32(dup[0].end),
		})
	}
	limit := n.maxSack
	if len(dup) > 0 {
		limit--
	}
	ordered := append([]simBlock(nil), n.blocks...)
	for i := 0; i < len(ordered); i++ {
		for j := i + 1; j < len(ordered); j++ {
			if ordered[j].stamp > ordered[i].stamp {
				ordered[i], ordered[j] = ordered[j], ordered[i]
			}
		}
	}
	for i := 0; i < len(ordered) && i < limit; i++ {
		result.sack = append(result.sack, Block{
			Start: n.base + uint32(ordered[i].start),
			End:   n.base + uint32(ordered[i].end),
			Stamp: ordered[i].stamp,
		})
	}
	return result
}

func compareResults(t *testing.T, got Result, want naiveResult, iter int, seq uint32, data []byte) {
	t.Helper()
	if got.AckNo != want.ack || got.Delivered != want.delivered || got.Dropped != want.dropped || len(got.Sack) != len(want.sack) {
		t.Fatalf("iter %d seq=%d len=%d result=%+v want=%+v", iter, seq, len(data), got, want)
	}
	for i := range want.sack {
		if got.Sack[i].Start != want.sack[i].Start || got.Sack[i].End != want.sack[i].End || got.Sack[i].Stamp != want.sack[i].Stamp {
			t.Fatalf("iter %d Sack[%d]=%+v want=%+v all=%+v", iter, i, got.Sack[i], want.sack[i], want.sack)
		}
		if want.sack[i].Stamp != 0 && len(got.Sack[i].Data) != int(want.sack[i].End-want.sack[i].Start) {
			t.Fatalf("iter %d Sack[%d] data len=%d", iter, i, len(got.Sack[i].Data))
		}
	}
}

func TestRandomByteSetSimulation2000(t *testing.T) {
	rng := rand.New(rand.NewSource(20261002))
	for iter := 0; iter < 2000; iter++ {
		rcvNxt := uint32(rng.Int63())
		if rng.Intn(4) == 0 {
			rcvNxt = uint32(0xFFFFFFF0 + rng.Intn(64))
		}
		window := 1 + rng.Intn(80)
		maxSack := 1 + rng.Intn(4)
		maxBlocks := 1 + rng.Intn(8)
		receiver, err := New(rcvNxt, window, maxSack, maxBlocks)
		if err != nil {
			t.Fatal(err)
		}
		model := newNaive(rcvNxt, window, maxSack, maxBlocks)

		for step := 0; step < 24; step++ {
			var seq uint32
			switch rng.Intn(6) {
			case 0:
				seq = receiver.rcvNxt - uint32(rng.Intn(40)+1)
			case 1:
				seq = receiver.rcvNxt
			case 2:
				seq = receiver.rcvNxt + uint32(rng.Intn(int(window)+40))
			case 3:
				seq = receiver.rcvNxt - uint32(rng.Intn(20)) + uint32(rng.Intn(window+40))
			default:
				seq = uint32(rng.Int63())
			}
			length := 1 + rng.Intn(30)
			data := make([]byte, length)
			for i := range data {
				data[i] = byte(rng.Intn(256))
			}

			got, gotErr := receiver.OnSegment(seq, data)
			want := model.onSegment(seq, data)
			if gotErr != nil {
				t.Fatalf("iter %d step %d unexpected error: %v", iter, step, gotErr)
			}

			t.Logf("iter=%d params(base=%d W=%d maxSack=%d maxBlocks=%d) step=%d input=(seq=%d len=%d, first=%d) output=(ack=%d delivered=%d dropped=%v sack=%v) judgement=(candidate/dup/merge/drop compared with byte-set model)",
				iter, rcvNxt, window, maxSack, maxBlocks, step, seq, length, data[0], got.AckNo, got.Delivered, got.Dropped, got.Sack)
			compareResults(t, got, want, iter, seq, data)

			if receiver.RcvNxt() != model.base {
				t.Fatalf("iter %d base got=%d want=%d", iter, receiver.RcvNxt(), model.base)
			}
			gotBlocks := receiver.Blocks()
			if len(gotBlocks) != len(model.blocks) {
				t.Fatalf("iter %d blocks got=%+v want=%+v", iter, gotBlocks, model.blocks)
			}
			wantByStart := make(map[uint32]simBlock, len(model.blocks))
			for _, wantBlock := range model.blocks {
				wantByStart[model.base+uint32(wantBlock.start)] = wantBlock
			}
			for i, gotBlock := range gotBlocks {
				wantBlock, ok := wantByStart[gotBlock.Start]
				if !ok || gotBlock.End != model.base+uint32(wantBlock.end) || gotBlock.Stamp != wantBlock.stamp {
					t.Fatalf("iter %d block %d got=%+v want=%+v", iter, i, gotBlock, model.blocks)
				}
				for idx, pos := 0, wantBlock.start; pos < wantBlock.end; idx, pos = idx+1, pos+1 {
					wantByte := model.content[pos]
					gotByte := gotBlock.Data[idx]
					if gotByte != wantByte {
						t.Fatalf("iter %d byte at rel %d got=%d want=%d", iter, pos, gotByte, wantByte)
					}
				}
			}
			gotDelivered := receiver.DeliveredData()
			if len(gotDelivered) != len(model.delivered) {
				t.Fatalf("iter %d delivered len got=%d want=%d", iter, len(gotDelivered), len(model.delivered))
			}
			for i := range model.delivered {
				if gotDelivered[i] != model.delivered[i] {
					t.Fatalf("iter %d delivered byte %d got=%d want=%d gotTail=%v wantTail=%v blocks=%+v", iter, i, gotDelivered[i], model.delivered[i], gotDelivered[i-10:], model.delivered[i-10:], gotBlocks)
				}
			}
		}
	}
}

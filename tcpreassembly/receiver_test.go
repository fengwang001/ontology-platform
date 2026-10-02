package tcpreassembly

import (
	"bytes"
	"fmt"
	"math/rand"
	"sort"
	"sync"
	"testing"
)

func TestSpecifiedSequence(t *testing.T) {
	r := mustNewReceiver(t, 1000, 10000, 3, 8)

	res := mustSegment(t, r, 2000, 100)
	assertResult(t, res, 1000, 0, false, [][2]uint32{{2000, 2100}})
	assertBlockRange(t, r.Blocks()[0], 2000, 2100, 1)

	res = mustSegment(t, r, 3000, 100)
	assertResult(t, res, 1000, 0, false, [][2]uint32{{3000, 3100}, {2000, 2100}})

	res = mustSegment(t, r, 2100, 100)
	assertResult(t, res, 1000, 0, false, [][2]uint32{{2000, 2200}, {3000, 3100}})
	assertBlockRange(t, r.Blocks()[0], 2000, 2200, 3)

	res = mustSegment(t, r, 2000, 100)
	assertResult(t, res, 1000, 0, false, [][2]uint32{{2000, 2100}, {2000, 2200}, {3000, 3100}})

	res = mustSegment(t, r, 900, 200)
	assertResult(t, res, 1100, 100, false, [][2]uint32{{900, 1000}, {2000, 2200}, {3000, 3100}})
	if !bytes.Equal(res.DeliveredData, payload(900, 200)[100:]) {
		t.Fatalf("delivered data = %x, want prefix bytes", res.DeliveredData)
	}

	mustSegment(t, r, 4000, 100)
	res = mustSegment(t, r, 2000, 100)
	assertResult(t, res, 1100, 0, false, [][2]uint32{{2000, 2100}, {4000, 4100}, {2000, 2200}})
}

func TestMergeKeepsFirstArrival(t *testing.T) {
	r := mustNewReceiver(t, 1000, 10000, 3, 8)
	first := []byte("0123456789")
	second := []byte("abcdefghijklmnopqrstuvwxyz")

	if res, err := r.OnSegment(1010, first); err != nil {
		t.Fatal(err)
	} else {
		assertResult(t, res, 1000, 0, false, [][2]uint32{{1010, 1020}})
	}
	res, err := r.OnSegment(1005, second)
	if err != nil {
		t.Fatal(err)
	}
	assertResult(t, res, 1000, 0, false, [][2]uint32{{1010, 1020}, {1005, 1031}})
	block := r.Blocks()[0]
	want := append([]byte("abcde"), first...)
	want = append(want, []byte("pqrstuvwxyz")...)
	if !bytes.Equal(block.Data, want) {
		t.Fatalf("merged data = %q, want %q", block.Data, want)
	}
}

func TestWraparoundDelivery(t *testing.T) {
	base := uint32(0xfffffff0)
	r := mustNewReceiver(t, base, 10000, 3, 8)

	res := mustSegment(t, r, 0xfffffff8, 16)
	assertResult(t, res, base, 0, false, [][2]uint32{{0xfffffff8, 0x08}})
	block := r.Blocks()[0]
	if block.Start != 0xfffffff8 || block.End != 0x08 || len(block.Data) != 16 {
		t.Fatalf("wrapped block = [%#x,%#x) len=%d", block.Start, block.End, len(block.Data))
	}

	res = mustSegment(t, r, base, 8)
	assertResult(t, res, 0x08, 24, false, nil)
	if len(r.Blocks()) != 0 {
		t.Fatalf("blocks after delivery = %#v", r.Blocks())
	}
	wantDelivered := append(payload(base, 8), payload(0xfffffff8, 16)...)
	if !bytes.Equal(res.DeliveredData, wantDelivered) {
		t.Fatalf("wrapped delivered data mismatch: %x", res.DeliveredData)
	}
}

func TestFillingHoleDeliversConnectedBlocks(t *testing.T) {
	r := mustNewReceiver(t, 1000, 10000, 3, 8)
	mustSegment(t, r, 1002, 3)
	mustSegment(t, r, 1005, 5)
	if blocks := r.Blocks(); len(blocks) != 2 {
		if len(blocks) != 1 || blocks[0].Start != 1002 || blocks[0].End != 1010 {
			t.Fatalf("adjacent blocks before hole should merge = %#v", blocks)
		}
	}

	res := mustSegment(t, r, 1000, 2)
	assertResult(t, res, 1010, 10, false, nil)
	if len(r.Blocks()) != 0 {
		t.Fatalf("connected blocks were not delivered: %#v", r.Blocks())
	}
}

func TestDSackOnlyWhenMaxSackIsOne(t *testing.T) {
	r := mustNewReceiver(t, 1000, 10000, 1, 8)
	mustSegment(t, r, 2000, 100)
	res := mustSegment(t, r, 2000, 100)
	assertResult(t, res, 1000, 0, false, [][2]uint32{{2000, 2100}})
}

func TestConcurrentQueriesAndSegments(t *testing.T) {
	r := mustNewReceiver(t, 1000, 10000, 3, 8)
	var wg sync.WaitGroup
	for worker := 0; worker < 10; worker++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			for step := 0; step < 100; step++ {
				_, _ = r.OnSegment(1000+uint32((worker*97+step*7)%500), payload(1000+uint32((worker*97+step*7)%500), 8))
			}
		}(worker)
	}
	for worker := 0; worker < 8; worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for step := 0; step < 200; step++ {
				_ = r.RcvNxt()
				_ = r.Blocks()
				_ = r.SackBlocks()
			}
		}()
	}
	wg.Wait()
	assertReceiverInvariant(t, r, 10000)
}

func TestDroppingWindowAndFullBlocks(t *testing.T) {
	r := mustNewReceiver(t, 1000, 10000, 3, 2)
	mustSegment(t, r, 2000, 100)
	mustSegment(t, r, 3000, 100)

	res := mustSegment(t, r, 4000, 100)
	if !res.Dropped || res.AckNo != 1000 || len(res.Sack) != 2 {
		t.Fatalf("isolated full-table result = %+v", res)
	}
	if blocks := r.Blocks(); len(blocks) != 2 {
		t.Fatalf("dropped segment changed blocks: %#v", blocks)
	}

	res = mustSegment(t, r, 2100, 100)
	if res.Dropped || len(r.Blocks()) != 2 {
		t.Fatalf("touching segment should merge: result=%+v blocks=%#v", res, r.Blocks())
	}
	assertResult(t, res, 1000, 0, false, [][2]uint32{{2000, 2200}, {3000, 3100}})

	before := r.RcvNxt()
	res = mustSegment(t, r, 500, 100)
	if res.Dropped || len(res.Sack) != 2 || res.AckNo != before {
		t.Fatalf("segment wholly left of window = %+v", res)
	}
	res = mustSegment(t, r, 11000, 100)
	if res.Dropped || len(res.Sack) != 2 || res.AckNo != before {
		t.Fatalf("segment wholly right of window = %+v", res)
	}
	res = mustSegment(t, r, 10990, 20)
	if !res.Dropped || res.AckNo != before {
		t.Fatalf("clipped isolated edge segment = %+v", res)
	}
	res = mustSegment(t, r, 10950, 100)
	if !res.Dropped || res.AckNo != before {
		t.Fatalf("cross-edge isolated segment = %+v", res)
	}

	if _, err := r.OnSegment(1000, nil); err != ErrInvalidArgument {
		t.Fatalf("nil segment error = %v", err)
	}
	if _, err := r.OnSegment(1000, make([]byte, 65536)); err != ErrInvalidArgument {
		t.Fatalf("oversize segment error = %v", err)
	}
	ack := r.RcvNxt()
	blocks := r.Blocks()
	if ack != before || len(blocks) != 2 {
		t.Fatalf("invalid call changed state ack=%#x before=%#x blocks=%s", ack, before, formatBlocks(blocks))
	}
	if _, err := NewReceiver(0, 0, 3, 8); err == nil {
		t.Fatal("invalid constructor did not fail")
	}
}

func mustNewReceiver(t *testing.T, rcvNxt uint32, window uint32, maxSack uint32, maxBlocks uint32) *Receiver {
	t.Helper()
	r, err := NewReceiver(rcvNxt, window, maxSack, maxBlocks)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func mustSegment(t *testing.T, r *Receiver, seq uint32, length int) Result {
	t.Helper()
	result, err := r.OnSegment(seq, payload(seq, length))
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func payload(seq uint32, length int) []byte {
	data := make([]byte, length)
	for i := range data {
		data[i] = byte(uint32(i) + seq*31)
	}
	return data
}

func assertResult(t *testing.T, result Result, ack uint32, delivered uint32, dropped bool, sack [][2]uint32) {
	t.Helper()
	if result.AckNo != ack || result.Delivered != delivered || result.Dropped != dropped || len(result.Sack) != len(sack) {
		t.Fatalf("result=%+v, want ack=%#x delivered=%d dropped=%v sack=%v", result, ack, delivered, dropped, sack)
	}
	for i, expected := range sack {
		if result.Sack[i].Start != expected[0] || result.Sack[i].End != expected[1] {
			t.Fatalf("sack[%d]=[%#x,%#x), want [%#x,%#x)", i, result.Sack[i].Start, result.Sack[i].End, expected[0], expected[1])
		}
	}
}

func assertBlockRange(t *testing.T, block Block, start uint32, end uint32, stamp uint64) {
	t.Helper()
	if block.Start != start || block.End != end || block.Stamp != stamp {
		t.Fatalf("block=%#v, want [%#x,%#x) stamp=%d", block, start, end, stamp)
	}
}

func assertReceiverInvariant(t *testing.T, r *Receiver, window uint32) {
	t.Helper()
	blocks := r.Blocks()
	base := r.RcvNxt()
	for i, block := range blocks {
		lo := int32(block.Start - base)
		hi := int64(lo) + int64(len(block.Data))
		if lo <= 0 || hi > int64(window) || int64(len(block.Data)) != int64(block.End-block.Start) {
			t.Fatalf("block %#v outside [1,%d] relative to ack %#x", block, window, base)
		}
		if i > 0 {
			if block.Start <= blocks[i-1].End {
				t.Fatalf("blocks overlap or touch: %#v %#v", blocks[i-1], block)
			}
		}
	}
}

func TestRandomNaiveByteModel2000(t *testing.T) {
	rng := rand.New(rand.NewSource(20261002))
	for sequence := 0; sequence < 2000; sequence++ {
		base := rng.Uint32()
		model := newNaiveReceiver(base, 10000, 3, 8)
		actual, err := NewReceiver(base, 10000, 3, 8)
		if err != nil {
			t.Fatal(err)
		}
		steps := 30 + rng.Intn(31)
		var log bytes.Buffer
		fmt.Fprintf(&log, "sequence=%d base=%#x window=10000 maxSack=3 maxBlocks=8\n", sequence, base)

		for step := 0; step < steps; step++ {
			length := 1 + rng.Intn(140)
			var seq uint32
			switch rng.Intn(10) {
			case 0:
				seq = model.rcvNxt32() - uint32(rng.Intn(300))
			case 1, 2:
				seq = model.rcvNxt32() + uint32(rng.Intn(10000))
			case 3:
				seq = model.rcvNxt32() + uint32(9900+rng.Intn(200))
			case 4:
				seq = model.rcvNxt32() - uint32(20+rng.Intn(100))
			default:
				seq = model.rcvNxt32() + uint32(rng.Intn(3000))
			}
			data := generatedPayload(seq, length)
			want := model.onSegment(seq, data)
			got, callErr := actual.OnSegment(seq, data)
			if callErr != nil {
				t.Fatalf("%sstep=%d unexpected error: %v", log.String(), step, callErr)
			}
			fmt.Fprintf(&log, "step=%d input=Seg(%#x,%d) -> output={ack:%#x delivered:%d dropped:%v sack:%s}; model={ack:%#x delivered:%d dropped:%v sack:%s}; actualBlocks=%s; basis=window-clip/duplicate-before-state/new-first/merge-touching/stamp-order\n",
				step, seq, length, got.AckNo, got.Delivered, got.Dropped, formatSack(got.Sack),
				want.AckNo, want.Delivered, want.Dropped, formatSack(want.Sack), formatBlocks(actual.Blocks()))
			assertModelState(t, &log, step, actual, model, got, want)
		}
	}
}

type naiveInterval struct {
	start int64
	end   int64
	stamp uint64
}

type naiveReceiver struct {
	rcvNxt    int64
	window    int64
	maxSack   int
	maxBlocks int
	clk       uint64
	blocks    []naiveInterval
	delivered []byte
}

func newNaiveReceiver(rcvNxt uint32, window uint32, maxSack uint32, maxBlocks uint32) *naiveReceiver {
	return &naiveReceiver{
		rcvNxt:    int64(rcvNxt),
		window:    int64(window),
		maxSack:   int(maxSack),
		maxBlocks: int(maxBlocks),
	}
}

func (n *naiveReceiver) rcvNxt32() uint32 {
	return uint32(n.rcvNxt)
}

func (n *naiveReceiver) onSegment(seq uint32, input []byte) Result {
	base := n.rcvNxt
	lo := int64(int32(seq-uint32(base))) + base
	hi := lo + int64(len(input))
	var duplicates []naiveInterval

	addDuplicate := func(start int64, end int64) {
		if start >= end {
			return
		}
		duplicates = append(duplicates, naiveInterval{start: start, end: end})
	}

	if lo < base && hi > base {
		addDuplicate(lo, minInt(hi, base))
	}

	candidateLo := maxInt(lo, base)
	candidateHi := minInt(hi, base+n.window)
	var covered []naiveInterval
	for _, block := range n.blocks {
		start := maxInt(candidateLo, block.start)
		end := minInt(candidateHi, block.end)
		if start < end {
			covered = append(covered, naiveInterval{start: start, end: end})
			addDuplicate(start, end)
		}
	}
	covered = mergeNaive(covered)

	news := subtractNaive(candidateLo, candidateHi, covered)
	dropped := false
	if candidateLo < candidateHi && len(news) > 0 && candidateLo > base &&
		len(n.blocks) >= n.maxBlocks && !n.touchesAny(candidateLo, candidateHi) {
		dropped = true
		news = nil
	}
	if len(news) == 0 {
		return n.result(0, nil, dropped, duplicates)
	}

	components := mergeNaive(append(append([]naiveInterval{}, n.blocks...), news...))
	var deliveredEnd int64
	var deliveredData []byte
	deliveredLen := uint32(0)
	if components[0].start == base {
		deliveredEnd = components[0].end
		deliveredData = make([]byte, deliveredEnd-base)
		for pos := base; pos < deliveredEnd; pos++ {
			deliveredData[pos-base] = generatedByte(uint32(pos))
		}
		remaining := make([]naiveInterval, 0, len(n.blocks))
		for _, block := range n.blocks {
			if block.end <= base || block.start >= deliveredEnd {
				remaining = append(remaining, block)
			}
		}
		n.blocks = remaining
		news = nil
		deliveredLen = uint32(deliveredEnd - base)
		n.rcvNxt = deliveredEnd
	}

	if len(news) > 0 {
		n.clk++
	}
	touched := make(map[int]bool)
	var added []naiveInterval
	for _, component := range mergeNaive(append(append([]naiveInterval{}, n.blocks...), news...)) {
		containsNew := false
		for _, nr := range news {
			if nr.start < component.end && component.start < nr.end {
				containsNew = true
				break
			}
		}
		if !containsNew {
			continue
		}
		component.stamp = n.clk
		for idx, block := range n.blocks {
			if block.start < component.end && component.start < block.end {
				touched[idx] = true
			}
		}
		added = append(added, component)
	}
	next := append([]naiveInterval{}, added...)
	for idx, block := range n.blocks {
		if !touched[idx] {
			next = append(next, block)
		}
	}
	sortNaive(next)
	n.blocks = next
	n.delivered = append(n.delivered, deliveredData...)

	return n.result(deliveredLen, deliveredData, dropped, duplicates)
}

func (n *naiveReceiver) result(delivered uint32, deliveredData []byte, dropped bool, duplicates []naiveInterval) Result {
	blocks := append([]naiveInterval{}, n.blocks...)
	sort.SliceStable(blocks, func(i, j int) bool {
		if blocks[i].stamp != blocks[j].stamp {
			return blocks[i].stamp > blocks[j].stamp
		}
		return uint32(blocks[i].start) < uint32(blocks[j].start)
	})
	result := Result{
		AckNo:         uint32(n.rcvNxt),
		Delivered:     delivered,
		DeliveredData: deliveredData,
		Sack:          []Block{},
		Dropped:       dropped,
	}
	remaining := n.maxSack
	if merged := mergeNaive(duplicates); len(merged) > 0 {
		result.Sack = append(result.Sack, Block{Start: uint32(merged[0].start), End: uint32(merged[0].end)})
		remaining--
	}
	for _, block := range blocks {
		if remaining == 0 {
			break
		}
		result.Sack = append(result.Sack, Block{Start: uint32(block.start), End: uint32(block.end), Stamp: block.stamp})
		remaining--
	}
	return result
}

func (n *naiveReceiver) touchesAny(start int64, end int64) bool {
	for _, block := range n.blocks {
		if start <= block.end && block.start <= end {
			return true
		}
	}
	return false
}

func assertModelState(t *testing.T, log *bytes.Buffer, step int, actual *Receiver, model *naiveReceiver, got Result, want Result) {
	t.Helper()
	if got.AckNo != want.AckNo || got.Delivered != want.Delivered || got.Dropped != want.Dropped {
		t.Fatalf("%sstep=%d result mismatch got=%+v want=%+v", log.String(), step, got, want)
	}
	if !bytes.Equal(got.DeliveredData, want.DeliveredData) {
		t.Fatalf("%sstep=%d delivered data mismatch", log.String(), step)
	}
	if len(got.Sack) != len(want.Sack) {
		t.Fatalf("%sstep=%d sack len mismatch got=%s want=%s", log.String(), step, formatSack(got.Sack), formatSack(want.Sack))
	}
	for i := range got.Sack {
		if got.Sack[i].Start != want.Sack[i].Start || got.Sack[i].End != want.Sack[i].End || got.Sack[i].Stamp != want.Sack[i].Stamp {
			t.Fatalf("%sstep=%d sack[%d] mismatch got=%s want=%s", log.String(), step, i, formatSack(got.Sack), formatSack(want.Sack))
		}
	}
	actualBlocks := actual.Blocks()
	if len(actualBlocks) != len(model.blocks) {
		t.Fatalf("%sstep=%d block count mismatch got=%s", log.String(), step, formatBlocks(actualBlocks))
	}
	for i, block := range model.blocks {
		if actualBlocks[i].Start != uint32(block.start) || actualBlocks[i].End != uint32(block.end) || actualBlocks[i].Stamp != block.stamp {
			t.Fatalf("%sstep=%d block[%d] mismatch got=%s want=%v", log.String(), step, i, formatBlocks(actualBlocks), model.blocks)
		}
		for pos := block.start; pos < block.end; pos++ {
			if actualBlocks[i].Data[pos-block.start] != generatedByte(uint32(pos)) {
				t.Fatalf("%sstep=%d block data mismatch at %#x", log.String(), step, uint32(pos))
			}
		}
	}
}

func generatedPayload(seq uint32, length int) []byte {
	data := make([]byte, length)
	for i := range data {
		data[i] = generatedByte(seq + uint32(i))
	}
	return data
}

func generatedByte(seq uint32) byte {
	return byte(seq*31 + 7)
}

func mergeNaive(input []naiveInterval) []naiveInterval {
	if len(input) == 0 {
		return nil
	}
	intervals := append([]naiveInterval{}, input...)
	sort.Slice(intervals, func(i, j int) bool {
		if intervals[i].start != intervals[j].start {
			return intervals[i].start < intervals[j].start
		}
		return intervals[i].end < intervals[j].end
	})
	result := []naiveInterval{intervals[0]}
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

func subtractNaive(lo int64, hi int64, covered []naiveInterval) []naiveInterval {
	if lo >= hi {
		return nil
	}
	var result []naiveInterval
	cursor := lo
	for _, part := range covered {
		if part.end <= cursor {
			continue
		}
		if part.start >= hi {
			break
		}
		if part.start > cursor {
			result = append(result, naiveInterval{start: cursor, end: minInt(part.start, hi)})
		}
		cursor = maxInt(cursor, minInt(part.end, hi))
	}
	if cursor < hi {
		result = append(result, naiveInterval{start: cursor, end: hi})
	}
	return result
}

func filterOutside(input []naiveInterval, removeLo int64, removeHi int64, keepEnd int64) []naiveInterval {
	var result []naiveInterval
	for _, item := range input {
		if item.end <= removeLo || item.start >= removeHi {
			if item.start < keepEnd {
				result = append(result, item)
			}
		}
	}
	return result
}

func sortNaive(blocks []naiveInterval) {
	sort.Slice(blocks, func(i, j int) bool {
		return blocks[i].start < blocks[j].start
	})
}

func minInt(a int64, b int64) int64 {
	if a < b {
		return a
	}
	return b
}

func maxInt(a int64, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

func formatSack(blocks []Block) string {
	parts := make([][3]any, 0, len(blocks))
	for _, block := range blocks {
		parts = append(parts, [3]any{block.Start, block.End, block.Stamp})
	}
	return fmt.Sprintf("%v", parts)
}

func formatBlocks(blocks []Block) string {
	return formatSack(blocks)
}

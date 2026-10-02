package tcprecv

import (
	"bytes"
	"testing"
)

func payload(start uint32, n int) []byte {
	data := make([]byte, n)
	for i := range data {
		data[i] = byte(uint64(start) + uint64(i))
	}
	return data
}

func fixedPayload(n int, value byte) []byte {
	return bytes.Repeat([]byte{value}, n)
}

func assertBlock(t *testing.T, got Block, start, end uint32, stamp uint64) {
	t.Helper()
	if got.Start != start || got.End != end || got.Stamp != stamp {
		t.Fatalf("block = [%d,%d) stamp=%d, want [%d,%d) stamp=%d", got.Start, got.End, got.Stamp, start, end, stamp)
	}
	if stamp != 0 && int(got.End-got.Start) != len(got.Data) {
		t.Fatalf("block data length = %d, want %d", len(got.Data), got.End-got.Start)
	}
}

func TestSpecifiedReassemblyAndDSackOrder(t *testing.T) {
	r, err := New(1000, 10000, 3, 8)
	if err != nil {
		t.Fatal(err)
	}

	res, err := r.OnSegment(2000, payload(2000, 100))
	if err != nil || res.Delivered != 0 || res.AckNo != 1000 || res.Dropped {
		t.Fatalf("unexpected result: %+v, err=%v", res, err)
	}
	if len(res.Sack) != 1 {
		t.Fatalf("Sack len = %d, want 1", len(res.Sack))
	}
	assertBlock(t, res.Sack[0], 2000, 2100, 1)

	res, _ = r.OnSegment(3000, payload(3000, 100))
	assertBlock(t, res.Sack[0], 3000, 3100, 2)
	assertBlock(t, res.Sack[1], 2000, 2100, 1)

	res, _ = r.OnSegment(2100, payload(2100, 100))
	assertBlock(t, res.Sack[0], 2000, 2200, 3)
	assertBlock(t, res.Sack[1], 3000, 3100, 2)

	res, _ = r.OnSegment(2000, payload(2000, 100))
	if len(res.Sack) != 3 {
		t.Fatalf("Sack len = %d, want 3", len(res.Sack))
	}
	assertBlock(t, res.Sack[0], 2000, 2100, 0)
	assertBlock(t, res.Sack[1], 2000, 2200, 3)
	assertBlock(t, res.Sack[2], 3000, 3100, 2)

	res, _ = r.OnSegment(900, payload(900, 200))
	if res.AckNo != 1100 || res.Delivered != 100 {
		t.Fatalf("delivery result = %+v", res)
	}
	if !bytes.Equal(res.DeliveredData, payload(1000, 100)) {
		t.Fatalf("delivered data mismatch")
	}
	if len(res.Sack) != 3 {
		t.Fatalf("Sack len = %d, want 3", len(res.Sack))
	}
	assertBlock(t, res.Sack[0], 900, 1000, 0)
	assertBlock(t, res.Sack[1], 2000, 2200, 3)
	assertBlock(t, res.Sack[2], 3000, 3100, 2)

	_, _ = r.OnSegment(4000, payload(4000, 100))
	res, _ = r.OnSegment(3000, payload(3000, 100))
	if len(res.Sack) != 3 {
		t.Fatalf("Sack len = %d, want 3", len(res.Sack))
	}
	assertBlock(t, res.Sack[0], 3000, 3100, 0)
	assertBlock(t, res.Sack[1], 4000, 4100, 4)
	assertBlock(t, res.Sack[2], 2000, 2200, 3)
}

func TestOverlapKeepsFirstWinnerAndMergesSides(t *testing.T) {
	r, _ := New(0, 1000, 3, 8)
	if _, err := r.OnSegment(20, fixedPayload(10, 0xAA)); err != nil {
		t.Fatal(err)
	}
	res, err := r.OnSegment(10, fixedPayload(40, 0xBB))
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Sack) != 2 {
		t.Fatalf("Sack len=%d, want 2", len(res.Sack))
	}
	assertBlock(t, res.Sack[0], 20, 30, 0)
	block := res.Sack[1]
	assertBlock(t, block, 10, 50, 2)
	want := append(append(fixedPayload(10, 0xBB), fixedPayload(10, 0xAA)...), fixedPayload(20, 0xBB)...)
	if !bytes.Equal(block.Data, want) {
		t.Fatalf("merged data = % X, want % X", block.Data, want)
	}
}

func TestWraparoundBlockAndDeliveryChain(t *testing.T) {
	r, _ := New(0xFFFFFFF0, 64, 3, 8)
	res, err := r.OnSegment(0xFFFFFFF8, payload(0xFFFFFFF8, 16))
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Sack) != 1 {
		t.Fatalf("Sack len=%d", len(res.Sack))
	}
	assertBlock(t, res.Sack[0], 0xFFFFFFF8, 8, 1)
	if len(res.Sack[0].Data) != 16 {
		t.Fatalf("wrapping block data len=%d", len(res.Sack[0].Data))
	}

	res, _ = r.OnSegment(0xFFFFFFF0, payload(0xFFFFFFF0, 8))
	if res.AckNo != 8 || res.Delivered != 24 {
		t.Fatalf("wraparound delivery = %+v", res)
	}
	want := payload(0xFFFFFFF0, 24)
	if !bytes.Equal(res.DeliveredData, want) {
		t.Fatalf("wraparound delivered data mismatch")
	}
	if len(r.Blocks()) != 0 {
		t.Fatalf("blocks remain: %+v", r.Blocks())
	}
}

func TestFullBlocksDropsIsolatedButAcceptsAdjacent(t *testing.T) {
	r, _ := New(0, 1000, 3, 1)
	if _, err := r.OnSegment(10, payload(10, 10)); err != nil {
		t.Fatal(err)
	}
	res, _ := r.OnSegment(30, payload(30, 10))
	if !res.Dropped || len(r.Blocks()) != 1 {
		t.Fatalf("isolated segment should be dropped: %+v blocks=%+v", res, r.Blocks())
	}
	res, _ = r.OnSegment(20, payload(20, 10))
	if res.Dropped {
		t.Fatalf("adjacent segment should merge: %+v", res)
	}
	assertBlock(t, res.Sack[0], 10, 30, 2)
}

func TestWindowTrimsRightEdge(t *testing.T) {
	r, _ := New(1000, 10000, 3, 8)
	res, err := r.OnSegment(10990, payload(10990, 20))
	if err != nil {
		t.Fatal(err)
	}
	if res.Delivered != 0 || len(res.Sack) != 1 {
		t.Fatalf("trimmed edge result=%+v", res)
	}
	assertBlock(t, res.Sack[0], 10990, 11000, 1)
	if len(res.Sack[0].Data) != 10 {
		t.Fatalf("trimmed data len=%d", len(res.Sack[0].Data))
	}

	res, err = r.OnSegment(11000, payload(11000, 10))
	if err != nil || res.Dropped || res.Delivered != 0 || len(res.Sack) != 1 {
		t.Fatalf("fully outside result=%+v err=%v", res, err)
	}
}

func TestInvalidArgumentsDoNotMutate(t *testing.T) {
	r, _ := New(1000, 10000, 3, 8)
	if _, err := New(0, 0, 3, 8); err != ErrInvalidArgument {
		t.Fatalf("window zero err=%v", err)
	}
	if _, err := New(0, 1, 5, 8); err != ErrInvalidArgument {
		t.Fatalf("maxSack err=%v", err)
	}
	if _, err := r.OnSegment(1, nil); err != ErrInvalidArgument {
		t.Fatalf("empty segment err=%v", err)
	}
	if _, err := r.OnSegment(1, make([]byte, 65536)); err != ErrInvalidArgument {
		t.Fatalf("long segment err=%v", err)
	}
	if r.RcvNxt() != 1000 || len(r.Blocks()) != 0 {
		t.Fatalf("state changed after invalid call: nxt=%d blocks=%+v", r.RcvNxt(), r.Blocks())
	}
}

func TestConcurrentSegmentsAndQueries(t *testing.T) {
	r, _ := New(1000, 10000, 4, 64)
	done := make(chan struct{})
	for worker := 0; worker < 8; worker++ {
		go func(worker int) {
			for i := 0; i < 100; i++ {
				seq := 1000 + ((worker*97 + i*3) % 5000)
				_, _ = r.OnSegment(uint32(seq), payload(uint32(seq), 1+(worker+i)%40))
				_ = r.RcvNxt()
				_ = r.Blocks()
				_ = r.DeliveredData()
			}
			done <- struct{}{}
		}(worker)
	}
	for worker := 0; worker < 8; worker++ {
		<-done
	}
}

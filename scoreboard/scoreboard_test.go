package scoreboard

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
)

func recomputeInflight(states []SegmentState) int {
	total := 0
	for _, st := range states {
		if !st.Sacked && (!st.Lost || st.Retransmitted) {
			total += st.End - st.Start
		}
	}
	return total
}

func checkInvariant(t *testing.T, sb *Scoreboard, ctx string) {
	t.Helper()
	got := sb.Inflight()
	want := recomputeInflight(sb.Snapshot())
	t.Logf("%s: inflight=%d recomputed=%d snapshot=%+v", ctx, got, want, sb.Snapshot())
	if got != want {
		t.Fatalf("%s: inflight %d != recomputed %d", ctx, got, want)
	}
}

func mustNew(t *testing.T, m, d, w int) *Scoreboard {
	t.Helper()
	sb, err := New(m, d, w)
	if err != nil {
		t.Fatalf("New(%d,%d,%d): %v", m, d, w, err)
	}
	return sb
}

func mustSend(t *testing.T, sb *Scoreboard, length int) int {
	t.Helper()
	start, err := sb.Send(length)
	if err != nil {
		t.Fatalf("Send(%d): %v", length, err)
	}
	t.Logf("Send(%d) -> start=%d", length, start)
	return start
}

func mustAck(t *testing.T, sb *Scoreboard, cum int, blocks ...Block) {
	t.Helper()
	if err := sb.Ack(cum, blocks); err != nil {
		t.Fatalf("Ack(%d, %v): %v", cum, blocks, err)
	}
	t.Logf("Ack(%d, %v) -> ok, inflight=%d", cum, blocks, sb.Inflight())
}

func sendAll(t *testing.T, sb *Scoreboard, lengths ...int) {
	t.Helper()
	for _, l := range lengths {
		mustSend(t, sb, l)
	}
}

// Exactly D disjoint SACK fragments above a segment make it lost; D-1 do not.
// The byte total stays below (D-1)*M, so the fragment condition acts alone.
func TestLossByExactlyDFragments(t *testing.T) {
	const M, D = 10, 3
	sb := mustNew(t, M, D, 1000)
	sendAll(t, sb, 4, 2, 2, 4, 2, 2, 2) // [0,4) [4,6) [6,8) [8,12) [12,14) [14,16) [16,18)
	checkInvariant(t, sb, "after sends")

	mustAck(t, sb, 0, Block{4, 8}, Block{12, 14})
	snap := sb.Snapshot()
	t.Logf("after 2 fragments: seg[0,4) lost=%v (fragments=2 < D=3, bytes=6 < (D-1)*M=20)", snap[0].Lost)
	if snap[0].Lost {
		t.Fatalf("seg [0,4) must not be lost with only 2 fragments above")
	}
	checkInvariant(t, sb, "after 2 fragments")
	if got := sb.Inflight(); got != 12 {
		t.Fatalf("inflight = %d, want 12 (18 sent - 6 sacked)", got)
	}

	mustAck(t, sb, 0, Block{16, 18})
	snap = sb.Snapshot()
	t.Logf("after 3 fragments: seg[0,4) lost=%v (fragments=3 >= D=3, bytes=6 < 20: fragment condition alone)", snap[0].Lost)
	if !snap[0].Lost {
		t.Fatalf("seg [0,4) must be lost with exactly D=3 fragments above")
	}
	if got := sb.Inflight(); got != 6 {
		t.Fatalf("inflight = %d, want 6 ([8,12)+[14,16) still in flight)", got)
	}
	checkInvariant(t, sb, "after 3 fragments")
}

// Exactly (D-1)*M SACK bytes above a segment make it lost; one fewer byte
// does not. The fragment count stays below D, so the byte condition acts alone.
func TestLossByExactlyThresholdBytes(t *testing.T) {
	const M, D = 10, 3 // threshold = (D-1)*M = 20 bytes

	t.Run("exactly threshold bytes", func(t *testing.T) {
		sb := mustNew(t, M, D, 1000)
		sendAll(t, sb, 10, 10, 10) // [0,10) [10,20) [20,30)
		mustAck(t, sb, 0, Block{10, 30})
		snap := sb.Snapshot()
		t.Logf("seg[0,10) lost=%v (fragments=1 < D=3, bytes=20 >= (D-1)*M=20: byte condition alone)", snap[0].Lost)
		if !snap[0].Lost {
			t.Fatalf("seg [0,10) must be lost with exactly (D-1)*M=20 bytes above")
		}
		if got := sb.Inflight(); got != 0 {
			t.Fatalf("inflight = %d, want 0", got)
		}
		checkInvariant(t, sb, "threshold bytes")
	})

	t.Run("one byte below threshold", func(t *testing.T) {
		sb := mustNew(t, M, D, 1000)
		sendAll(t, sb, 10, 9, 10) // [0,10) [10,19) [19,29)
		mustAck(t, sb, 0, Block{10, 29})
		snap := sb.Snapshot()
		t.Logf("seg[0,10) lost=%v (fragments=1 < 3, bytes=19 < 20: neither condition)", snap[0].Lost)
		if snap[0].Lost {
			t.Fatalf("seg [0,10) must not be lost with only 19 bytes above")
		}
		if got := sb.Inflight(); got != 10 {
			t.Fatalf("inflight = %d, want 10", got)
		}
		checkInvariant(t, sb, "below threshold")
	})
}

// Retransmission returns the lost segment with the smallest start, restores
// its in-flight bytes, and advances to the next lost segment.
func TestRetransmitInflightChange(t *testing.T) {
	sb := mustNew(t, 10, 2, 100) // D=2: lost at 2 fragments or >= 10 bytes above
	sendAll(t, sb, 10, 10, 10, 10, 10)
	mustAck(t, sb, 0, Block{10, 20}, Block{30, 40})
	snap := sb.Snapshot()
	t.Logf("after ack: snapshot=%+v", snap)
	if !snap[0].Lost || !snap[2].Lost {
		t.Fatalf("segs [0,10) and [20,30) must be lost: %+v", snap)
	}
	if got := sb.Inflight(); got != 10 {
		t.Fatalf("inflight = %d, want 10 (only [40,50) in flight)", got)
	}

	start, err := sb.Retransmit()
	t.Logf("Retransmit() -> start=%d err=%v (smallest lost start)", start, err)
	if err != nil || start != 0 {
		t.Fatalf("Retransmit() = %d, %v; want 0, nil", start, err)
	}
	if got := sb.Inflight(); got != 20 {
		t.Fatalf("inflight after retransmit = %d, want 20", got)
	}
	checkInvariant(t, sb, "after first retransmit")

	start, err = sb.Retransmit()
	t.Logf("Retransmit() -> start=%d err=%v (next smallest lost start)", start, err)
	if err != nil || start != 20 {
		t.Fatalf("Retransmit() = %d, %v; want 20, nil", start, err)
	}
	if got := sb.Inflight(); got != 30 {
		t.Fatalf("inflight after second retransmit = %d, want 30", got)
	}

	start, err = sb.Retransmit()
	t.Logf("Retransmit() -> start=%d err=%v (no lost segment left)", start, err)
	if !errors.Is(err, ErrNoRetransmittable) {
		t.Fatalf("Retransmit() err = %v, want ErrNoRetransmittable", err)
	}
	checkInvariant(t, sb, "after draining retransmits")
}

// Advancing the cumulative point clears the segments and SACK records it
// passes over, and the in-flight count is recomputed from what remains.
func TestCumulativeSwallowsSack(t *testing.T) {
	sb := mustNew(t, 10, 4, 100)
	sendAll(t, sb, 10, 10, 10, 10, 10)
	mustAck(t, sb, 0, Block{10, 20}, Block{30, 40})
	if got := sb.Inflight(); got != 30 {
		t.Fatalf("inflight = %d, want 30 (50 sent - 20 sacked)", got)
	}

	mustAck(t, sb, 40)
	snap := sb.Snapshot()
	t.Logf("after cum=40: snapshot=%+v cumAck=%d (segments and SACK blocks below 40 cleared)", snap, sb.CumAck())
	if len(snap) != 1 || snap[0].Start != 40 || snap[0].Sacked || snap[0].Lost {
		t.Fatalf("unexpected snapshot after cum=40: %+v", snap)
	}
	if got := sb.Inflight(); got != 10 {
		t.Fatalf("inflight = %d, want 10", got)
	}
	if got := sb.CumAck(); got != 40 {
		t.Fatalf("cumAck = %d, want 40", got)
	}
	checkInvariant(t, sb, "after cumulative swallow")

	mustAck(t, sb, 50) // cumulative point may advance to the sent end
	if got := sb.Inflight(); got != 0 {
		t.Fatalf("inflight = %d, want 0 after full ack", got)
	}
	if len(sb.Snapshot()) != 0 {
		t.Fatalf("snapshot must be empty after full ack: %+v", sb.Snapshot())
	}
}

// A retransmission that makes the in-flight bytes exactly W is allowed; one
// byte over W is rejected with ErrInflightFull, and a board with no lost
// segment reports ErrNoRetransmittable first.
func TestRetransmitWindowLimit(t *testing.T) {
	t.Run("exact fit", func(t *testing.T) {
		sb := mustNew(t, 10, 2, 20) // W=20
		sendAll(t, sb, 10, 10, 10)
		mustAck(t, sb, 0, Block{10, 20}) // [0,10) lost: 10 bytes >= (D-1)*M=10
		if got := sb.Inflight(); got != 10 {
			t.Fatalf("inflight = %d, want 10", got)
		}
		start, err := sb.Retransmit()
		t.Logf("Retransmit() -> start=%d err=%v (inflight 10 + 10 == W 20: exact fit allowed)", start, err)
		if err != nil || start != 0 {
			t.Fatalf("Retransmit() = %d, %v; want 0, nil", start, err)
		}
		if got := sb.Inflight(); got != 20 {
			t.Fatalf("inflight = %d, want exactly W=20", got)
		}
	})

	t.Run("window full", func(t *testing.T) {
		sb := mustNew(t, 10, 2, 20) // W=20
		sendAll(t, sb, 10, 10, 10, 10)
		mustAck(t, sb, 0, Block{10, 20}) // [0,10) lost
		if got := sb.Inflight(); got != 20 {
			t.Fatalf("inflight = %d, want 20", got)
		}
		start, err := sb.Retransmit()
		t.Logf("Retransmit() -> start=%d err=%v (inflight 20 + 10 > W 20: full)", start, err)
		if !errors.Is(err, ErrInflightFull) {
			t.Fatalf("Retransmit() err = %v, want ErrInflightFull", err)
		}
		checkInvariant(t, sb, "after rejected retransmit")
	})

	t.Run("no retransmittable takes priority", func(t *testing.T) {
		sb := mustNew(t, 10, 2, 20) // W=20, inflight already at W, nothing lost
		sendAll(t, sb, 10, 10)
		if got := sb.Inflight(); got != 20 {
			t.Fatalf("inflight = %d, want 20", got)
		}
		_, err := sb.Retransmit()
		t.Logf("Retransmit() -> err=%v (no lost segment: reported before window-full)", err)
		if !errors.Is(err, ErrNoRetransmittable) {
			t.Fatalf("Retransmit() err = %v, want ErrNoRetransmittable", err)
		}
	})
}

// Constructor and Send reject invalid parameters without changing state.
func TestInvalidConstructionAndSend(t *testing.T) {
	for _, tc := range []struct {
		m, d, w int
		want    error
	}{
		{0, 3, 100, ErrNonPositiveM},
		{-1, 3, 100, ErrNonPositiveM},
		{10, 1, 100, ErrThresholdTooSmall},
		{10, 0, 100, ErrThresholdTooSmall},
		{10, 3, 9, ErrWindowTooSmall},
	} {
		_, err := New(tc.m, tc.d, tc.w)
		t.Logf("New(%d,%d,%d) -> err=%v", tc.m, tc.d, tc.w, err)
		if !errors.Is(err, tc.want) {
			t.Fatalf("New(%d,%d,%d) err = %v, want %v", tc.m, tc.d, tc.w, err, tc.want)
		}
	}

	sb := mustNew(t, 10, 3, 100)
	for _, length := range []int{0, -5, 11} {
		_, err := sb.Send(length)
		t.Logf("Send(%d) -> err=%v", length, err)
		if !errors.Is(err, ErrBadSegmentLength) {
			t.Fatalf("Send(%d) err = %v, want ErrBadSegmentLength", length, err)
		}
	}
	if got := sb.SentEnd(); got != 0 {
		t.Fatalf("sentEnd = %d after rejected sends, want 0", got)
	}
	checkInvariant(t, sb, "after rejected sends")
}

// Ack rejections are reported in the specified order, the first reason wins,
// and a rejected Ack leaves all state untouched (atomicity).
func TestInvalidAck(t *testing.T) {
	// Board: segments [0,10) [10,20) [20,30), cumAck advanced to 10, so the
	// tracked boundaries are 10, 20, 30 (segment starts) and 30 (sent end).
	newBoard := func(t *testing.T) *Scoreboard {
		sb := mustNew(t, 10, 3, 100)
		sendAll(t, sb, 10, 10, 10)
		mustAck(t, sb, 10)
		return sb
	}

	cases := []struct {
		name   string
		cum    int
		blocks []Block
		want   error
		why    string
	}{
		{"cum behind", 5, nil, ErrCumBehind, "5 < highest received cumulative point 10"},
		{"cum behind wins over not-boundary", 5, []Block{{15, 20}}, ErrCumBehind, "cumulative checks run before block checks"},
		{"cum beyond sent end", 40, nil, ErrCumBeyondEnd, "40 > sent end 30"},
		{"cum not on boundary", 15, nil, ErrCumNotBoundary, "15 is inside segment [10,20)"},
		{"block empty", 10, []Block{{20, 20}}, ErrBlockEmpty, "start == end"},
		{"block inverted", 10, []Block{{25, 20}}, ErrBlockEmpty, "start > end"},
		{"block empty wins over not-above", 10, []Block{{10, 10}}, ErrBlockEmpty, "empty/inverted checked before start <= cum"},
		{"block start at cum", 10, []Block{{10, 20}}, ErrBlockNotAbove, "start 10 <= cumulative point 10"},
		{"block start below cum", 10, []Block{{0, 20}}, ErrBlockNotAbove, "start 0 <= cumulative point 10"},
		{"block not-above wins over beyond-end", 10, []Block{{5, 100}}, ErrBlockNotAbove, "start <= cum checked before end > sent end"},
		{"block end beyond sent end", 10, []Block{{20, 40}}, ErrBlockBeyondEnd, "end 40 > sent end 30"},
		{"block beyond-end wins over not-boundary", 10, []Block{{20, 35}}, ErrBlockBeyondEnd, "end > sent end checked before boundary"},
		{"block start not on boundary", 10, []Block{{15, 20}}, ErrBlockNotOnBound, "15 is not a segment boundary"},
		{"block end not on boundary", 10, []Block{{20, 25}}, ErrBlockNotOnBound, "25 is not a segment boundary"},
		{"first bad block reported", 10, []Block{{15, 20}, {20, 25}}, ErrBlockNotOnBound, "blocks checked in given order, first failure wins"},
		{"second block bad after good one", 10, []Block{{20, 30}, {25, 30}}, ErrBlockNotOnBound, "second block end 25 not a boundary"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sb := newBoard(t)
			before := fmt.Sprintf("%+v", sb.Snapshot())
			beforeInflight := sb.Inflight()
			err := sb.Ack(tc.cum, tc.blocks)
			t.Logf("Ack(%d, %v) -> err=%v (%s)", tc.cum, tc.blocks, err, tc.why)
			if !errors.Is(err, tc.want) {
				t.Fatalf("Ack(%d, %v) err = %v, want %v", tc.cum, tc.blocks, err, tc.want)
			}
			if after := fmt.Sprintf("%+v", sb.Snapshot()); after != before {
				t.Fatalf("rejected ack changed state: before %s after %s", before, after)
			}
			if got := sb.Inflight(); got != beforeInflight {
				t.Fatalf("rejected ack changed inflight: %d -> %d", beforeInflight, got)
			}
			if got := sb.CumAck(); got != 10 {
				t.Fatalf("rejected ack changed cumAck: %d, want 10", got)
			}
		})
	}

	t.Run("rejected multi-block ack records nothing", func(t *testing.T) {
		sb := mustNew(t, 10, 3, 100)
		sendAll(t, sb, 10, 10, 10)
		err := sb.Ack(0, []Block{{10, 20}, {25, 30}}) // first block valid, second not on boundary
		t.Logf("Ack(0, [{10,20} {25,30}]) -> err=%v (atomic: valid first block must not be recorded)", err)
		if !errors.Is(err, ErrBlockNotOnBound) {
			t.Fatalf("err = %v, want ErrBlockNotOnBound", err)
		}
		for _, st := range sb.Snapshot() {
			if st.Sacked {
				t.Fatalf("rejected ack left sacked segment: %+v", st)
			}
		}
		if got := sb.Inflight(); got != 30 {
			t.Fatalf("inflight = %d, want 30 (nothing recorded)", got)
		}
	})
}

// Replaying the same call sequence on a fresh board produces identical
// results.
func TestDeterministicReplay(t *testing.T) {
	run := func(t *testing.T) string {
		sb := mustNew(t, 8, 3, 64)
		var trace strings.Builder
		log := func(format string, args ...any) {
			line := fmt.Sprintf(format, args...)
			trace.WriteString(line)
			trace.WriteByte('\n')
			t.Log(line)
		}
		for _, l := range []int{8, 4, 8, 2, 8, 8, 6} {
			start, err := sb.Send(l)
			log("Send(%d) -> %d, %v", l, start, err)
		}
		acks := []struct {
			cum    int
			blocks []Block
		}{
			{0, []Block{{8, 12}, {20, 28}}},
			{0, []Block{{36, 44}}},
			{4, []Block{{12, 20}}},
			{44, nil},
		}
		for _, a := range acks {
			err := sb.Ack(a.cum, a.blocks)
			log("Ack(%d, %v) -> %v inflight=%d", a.cum, a.blocks, err, sb.Inflight())
		}
		for i := 0; i < 4; i++ {
			start, err := sb.Retransmit()
			log("Retransmit() -> %d, %v inflight=%d", start, err, sb.Inflight())
		}
		log("Snapshot() -> %+v", sb.Snapshot())
		return trace.String()
	}
	first := run(t)
	second := run(t)
	if first != second {
		t.Fatalf("replay diverged:\nfirst:\n%s\nsecond:\n%s", first, second)
	}
}

// Concurrent Send/Ack/Retransmit/Inflight/Snapshot calls are safe (run with
// -race) and the in-flight invariant holds once the dust settles.
func TestConcurrentAccess(t *testing.T) {
	sb := mustNew(t, 16, 3, 1<<20)
	var wg sync.WaitGroup

	for g := 0; g < 4; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 50; i++ {
				_, _ = sb.Send(1 + (g+i)%16)
			}
		}(g)
	}
	for g := 0; g < 2; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 100; i++ {
				cum := sb.CumAck()
				var blocks []Block
				for j, st := range sb.Snapshot() {
					if j%3 == 1 && st.Start > cum {
						blocks = append(blocks, Block{st.Start, st.End})
					}
				}
				_ = sb.Ack(cum, blocks)
			}
		}()
	}
	for g := 0; g < 2; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 100; i++ {
				_, _ = sb.Retransmit()
			}
		}()
	}
	for g := 0; g < 2; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				_ = sb.Inflight()
				_ = sb.Snapshot()
			}
		}()
	}
	wg.Wait()

	checkInvariant(t, sb, "after concurrent phase")
	t.Logf("final: sentEnd=%d cumAck=%d inflight=%d segments=%d",
		sb.SentEnd(), sb.CumAck(), sb.Inflight(), len(sb.Snapshot()))
}

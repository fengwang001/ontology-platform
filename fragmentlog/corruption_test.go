package fragmentlog

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
)

// corruptionBaseStream builds a stream exercising padding, an empty
// record, an exact block fill and a multi-block record.
func corruptionBaseStream(t *testing.T, B int) []byte {
	t.Helper()
	var stream []byte
	stream, _ = naiveAppend(B, stream, []byte("abc"))                         // full, leaves padding
	stream, _ = naiveAppend(B, stream, nil)                                   // empty record
	stream, _ = naiveAppend(B, stream, make([]byte, B-7))                     // fills a block exactly
	stream, _ = naiveAppend(B, stream, bytes.Repeat([]byte{0x42}, 2*(B-7)+3)) // 3 blocks
	stream, _ = naiveAppend(B, stream, []byte("tail"))
	return stream
}

func summarize(events []event) string {
	var sb strings.Builder
	for i, ev := range events {
		if i > 0 {
			sb.WriteString(" | ")
		}
		sb.WriteString(ev.String())
	}
	return sb.String()
}

func compareWithNaive(t *testing.T, B int, data []byte, label string) {
	t.Helper()
	want := naiveReadAll(B, data)
	rd := mustReader(t, data, B)
	got := realReadAll(t, rd)
	if len(got) != len(want) {
		t.Fatalf("%s: got %d events, want %d\n got: %s\nwant: %s",
			label, len(got), len(want), summarize(got), summarize(want))
	}
	for i := range got {
		if !eventsEqual(got[i], want[i]) {
			t.Fatalf("%s: event %d differs\n got: %s\nwant: %s",
				label, i, summarize(got), summarize(want))
		}
	}
	t.Logf("%s -> %s", label, summarize(got))
}

// TestFlipEveryByte flips every byte of the output (two masks) and checks
// that the real reader never panics and produces exactly the event stream
// (error classes, offsets, recovery positions) predicted by the naive
// reference implementation of the rules.
func TestFlipEveryByte(t *testing.T) {
	const B = 32
	base := corruptionBaseStream(t, B)
	t.Logf("基准流: %d 字节, 干净读取事件: %s", len(base), summarize(naiveReadAll(B, base)))
	for i := 0; i < len(base); i++ {
		for _, mask := range []byte{0xFF, 0x01} {
			data := append([]byte(nil), base...)
			data[i] ^= mask
			compareWithNaive(t, B, data,
				fmt.Sprintf("翻转字节 %d (掩码 %#02x, 原值 %#02x)", i, mask, base[i]))
		}
	}
}

// TestTruncateEveryPosition truncates the output at every byte position
// and cross-checks error classes and offsets with the naive reader.
func TestTruncateEveryPosition(t *testing.T) {
	const B = 32
	base := corruptionBaseStream(t, B)
	for cut := 0; cut <= len(base); cut++ {
		compareWithNaive(t, B, base[:cut],
			fmt.Sprintf("截断到 %d/%d 字节", cut, len(base)))
	}
}

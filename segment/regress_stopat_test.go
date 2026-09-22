package segment

import (
	"testing"

	"ontology/codec"
)

// 回归：钉住契约「fn 返回 false 提前结束时，StopAt 是已完整消费的
// 字节位置（刚回放那条记录之后、下一条待读的位置）」，与正常扫完一致。
// 下游按 StopAt 做增量游标，停在记录起点会导致从同一条记录重扫。
func TestRegressEarlyStopAtConsumedPosition(t *testing.T) {
	dev := NewDevice()
	seg := New(dev)
	seg.Append([]byte("first"))
	second := seg.Append([]byte("second"))
	seg.Append([]byte("third"))

	var seen []string
	rep := seg.Scan(0, dev.Len(), func(_ int64, p []byte) bool {
		seen = append(seen, string(p))
		return false // 第一条就提前结束
	})
	if rep.Reason != StopEOF {
		t.Fatalf("reason = %v, want StopEOF", rep.Reason)
	}
	if len(seen) != 1 || seen[0] != "first" {
		t.Fatalf("replayed %v, want only [first]", seen)
	}
	if rep.StopAt != second {
		t.Fatalf("stop at %d, want consumed position %d (start of next record)",
			rep.StopAt, second)
	}

	// 从 StopAt 继续扫，必须恰好拿到剩下的记录，不重扫第一条。
	var rest []string
	rep2 := seg.Scan(rep.StopAt, dev.Len(), func(_ int64, p []byte) bool {
		rest = append(rest, string(p))
		return true
	})
	if rep2.Reason != StopEOF || rep2.StopAt != dev.Len() {
		t.Fatalf("resume report = %+v, want EOF at %d", rep2, dev.Len())
	}
	if len(rest) != 2 || rest[0] != "second" || rest[1] != "third" {
		t.Fatalf("resumed %v, want [second third]", rest)
	}

	// 零长度负载记录提前结束时，StopAt 也要前进 HeaderLen。
	dev2 := NewDevice()
	seg2 := New(dev2)
	seg2.Append([]byte{})
	rep3 := seg2.Scan(0, dev2.Len(), func(_ int64, _ []byte) bool { return false })
	if rep3.StopAt != int64(codec.HeaderLen) {
		t.Fatalf("empty payload: stop at %d, want %d", rep3.StopAt, codec.HeaderLen)
	}
}

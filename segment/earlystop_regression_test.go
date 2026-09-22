package segment

import (
	"testing"

	"ontology/codec"
)

// 回归测试：钉住契约「fn 返回 false 提前结束时，StopAt 必须是
// 已完整消费的字节位置（刚回放那条记录之后、下一条待读的位置），
// 与正常扫完的 StopAt 语义一致」。
// 修复前 StopAt 指在提前结束那条记录的起点，按 StopAt 做增量
// 游标的下游会从同一条记录重扫，造成死循环。
func TestScanEarlyStopReportsConsumedPosition(t *testing.T) {
	dev := NewDevice()
	seg := New(dev)
	first := seg.Append([]byte("first"))
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
		t.Fatalf("replayed %q, want only [first]", seen)
	}
	// 停点必须是第一条记录之后的位置，也就是第二条的起点。
	if rep.StopAt != second {
		t.Fatalf("stop at %d, want consumed position %d (record start was %d)",
			rep.StopAt, second, first)
	}

	// 从 StopAt 继续扫，必须恰好接上后续记录，不重复、不遗漏。
	var rest []string
	rep2 := seg.Scan(rep.StopAt, dev.Len(), func(_ int64, p []byte) bool {
		rest = append(rest, string(p))
		return true
	})
	if rep2.Reason != StopEOF || rep2.StopAt != dev.Len() {
		t.Fatalf("resume report = %+v, want EOF at %d", rep2, dev.Len())
	}
	if len(rest) != 2 || rest[0] != "second" || rest[1] != "third" {
		t.Fatalf("resumed %q, want [second third]", rest)
	}
}

// 回归测试：提前结束发生在非首条记录时，StopAt 同样要是
// 已消费位置，且等于手工算出的编码边界。
func TestScanEarlyStopAfterSeveralRecords(t *testing.T) {
	dev := NewDevice()
	seg := New(dev)
	payloads := []string{"aa", "bbb", "c"}
	for _, p := range payloads {
		seg.Append([]byte(p))
	}

	count := 0
	rep := seg.Scan(0, dev.Len(), func(_ int64, _ []byte) bool {
		count++
		return count < 2 // 回放两条后停下
	})
	want := int64(codec.EncodedLen(len(payloads[0])) + codec.EncodedLen(len(payloads[1])))
	if rep.StopAt != want {
		t.Fatalf("stop at %d, want consumed position %d", rep.StopAt, want)
	}
}

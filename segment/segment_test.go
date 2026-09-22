package segment

import (
	"bytes"
	"testing"

	"ontology/codec"
)

func appendOrDie(t *testing.T, s *Segment, payload []byte) int {
	t.Helper()
	off, err := s.Append(payload)
	if err != nil {
		t.Fatalf("Append: %v", err)
	}
	return off
}

func collect(payloads *[][]byte) func(int, []byte) bool {
	return func(_ int, p []byte) bool {
		*payloads = append(*payloads, p)
		return true
	}
}

// 顺序扫描能完整回放所有记录，且偏移单调递增。
func TestScanReplaysAllInOrder(t *testing.T) {
	buf := NewMemBuffer()
	s := New(buf)
	want := [][]byte{[]byte("a"), []byte("bb"), {}, []byte("ccc")}
	var offsets []int
	for _, p := range want {
		offsets = append(offsets, appendOrDie(t, s, p))
	}
	var got [][]byte
	var gotOffsets []int
	n, stop := s.Scan(buf.Len(), func(off int, p []byte) bool {
		got = append(got, p)
		gotOffsets = append(gotOffsets, off)
		return true
	})
	if n != len(want) || stop.Reason != codec.Complete {
		t.Fatalf("n=%d stop=%+v", n, stop)
	}
	for i := range want {
		if !bytes.Equal(got[i], want[i]) || gotOffsets[i] != offsets[i] {
			t.Fatalf("record %d: payload=%q off=%d, want %q off=%d",
				i, got[i], gotOffsets[i], want[i], offsets[i])
		}
	}
}

// 语义 2：扫描遇到损坏就停，停止点之后的完整记录不捡回来。
func TestScanStopsAtFirstDamage(t *testing.T) {
	buf := NewMemBuffer()
	s := New(buf)
	appendOrDie(t, s, []byte("first"))
	damageAt := s.Len()
	appendOrDie(t, s, []byte("second"))
	appendOrDie(t, s, []byte("third"))
	raw := buf.Bytes()
	raw[damageAt+codec.LenSize] ^= 0xFF // 翻坏第二条记录的负载
	var got [][]byte
	n, stop := s.Scan(buf.Len(), collect(&got))
	if n != 1 || len(got) != 1 || !bytes.Equal(got[0], []byte("first")) {
		t.Fatalf("replayed %d records %q, want only first", n, got)
	}
	if stop.Offset != damageAt || stop.Reason != codec.Corrupt {
		t.Fatalf("stop=%+v, want offset=%d reason=Corrupt", stop, damageAt)
	}
}

// 语义 3：从任意字节处截断，回放恰好是截断点之前的完整记录。
func TestScanAfterTruncation(t *testing.T) {
	payloads := [][]byte{[]byte("r1"), []byte("r22"), []byte("r333")}
	var boundaries []int
	// 每个用例都用全新缓冲，因为截断不可逆。
	fresh := func() (*MemBuffer, *Segment) {
		buf := NewMemBuffer()
		s := New(buf)
		boundaries = boundaries[:0]
		for _, p := range payloads {
			appendOrDie(t, s, p)
			boundaries = append(boundaries, s.Len())
		}
		return buf, s
	}
	// 边界：恰好截在第二条记录的最后一个字节上。
	buf, s := fresh()
	buf.Truncate(boundaries[1])
	var got [][]byte
	n, stop := s.Scan(buf.Len(), collect(&got))
	if n != 2 || stop.Reason != codec.Complete || stop.Offset != boundaries[1] {
		t.Fatalf("exact boundary: n=%d stop=%+v, want n=2 Complete@%d",
			n, stop, boundaries[1])
	}
	if !bytes.Equal(got[0], payloads[0]) || !bytes.Equal(got[1], payloads[1]) {
		t.Fatalf("exact boundary: got %q", got)
	}
	// 截在第三条记录中间：只回放前两条，停在第二条末尾。
	buf, s = fresh()
	buf.Truncate(boundaries[1] + 3)
	got = nil
	n, stop = s.Scan(buf.Len(), collect(&got))
	if n != 2 || stop.Reason != codec.Truncated || stop.Offset != boundaries[1] {
		t.Fatalf("mid truncation: n=%d stop=%+v, want n=2 Truncated@%d",
			n, stop, boundaries[1])
	}
	// 截在第一条记录中间：零条记录，停在偏移 0。
	buf, s = fresh()
	buf.Truncate(boundaries[0] - 1)
	got = nil
	n, stop = s.Scan(buf.Len(), collect(&got))
	if n != 0 || stop.Reason != codec.Truncated || stop.Offset != 0 {
		t.Fatalf("head truncation: n=%d stop=%+v", n, stop)
	}
}

// 逐字节截断性质：截断点之前的完整记录不多不少全部被回放。
func TestScanEveryTruncationPoint(t *testing.T) {
	payloads := [][]byte{[]byte("a"), []byte("bb"), {}, []byte("ccc")}
	buf := NewMemBuffer()
	s := New(buf)
	var boundaries []int
	for _, p := range payloads {
		appendOrDie(t, s, p)
		boundaries = append(boundaries, s.Len())
	}
	full := buf.Snapshot()
	for cut := 0; cut <= len(full); cut++ {
		b := NewMemBuffer()
		b.Append(full[:cut])
		sc := New(b)
		want := 0
		for _, boundary := range boundaries {
			if boundary <= cut {
				want++
			}
		}
		n, _ := sc.Scan(cut, collect(&[][]byte{}))
		if n != want {
			t.Fatalf("cut=%d: replayed %d records, want %d", cut, n, want)
		}
	}
}

// limit 小于缓冲长度时，只扫描 limit 之前的字节。
func TestScanRespectsLimit(t *testing.T) {
	buf := NewMemBuffer()
	s := New(buf)
	appendOrDie(t, s, []byte("one"))
	mark := s.Len()
	appendOrDie(t, s, []byte("two"))
	var got [][]byte
	n, stop := s.Scan(mark, collect(&got))
	if n != 1 || stop.Offset != mark || stop.Reason != codec.Complete {
		t.Fatalf("n=%d stop=%+v, want n=1 Complete@%d", n, stop, mark)
	}
	if !bytes.Equal(got[0], []byte("one")) {
		t.Fatalf("got %q", got)
	}
}

// 追加只增长缓冲，不改写已有字节（MemBuffer 层面）。
func TestAppendNeverMutatesHistory(t *testing.T) {
	buf := NewMemBuffer()
	s := New(buf)
	appendOrDie(t, s, []byte("stable-prefix"))
	before := buf.Snapshot()
	for i := 0; i < 10; i++ {
		appendOrDie(t, s, []byte{byte(i)})
	}
	after := buf.Bytes()
	if !bytes.Equal(after[:len(before)], before) {
		t.Fatal("append mutated previously written bytes")
	}
}

// 空缓冲扫描：零条记录，Complete 停在 0。
func TestScanEmptyBuffer(t *testing.T) {
	s := New(NewMemBuffer())
	n, stop := s.Scan(0, func(int, []byte) bool {
		t.Fatal("fn must not be called on empty buffer")
		return true
	})
	if n != 0 || stop.Offset != 0 || stop.Reason != codec.Complete {
		t.Fatalf("n=%d stop=%+v", n, stop)
	}
}

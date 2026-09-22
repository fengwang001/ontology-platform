package wal

import (
	"bytes"
	"fmt"
	"sync"
	"testing"

	"ontology/codec"
	"ontology/segment"
)

func openWith(t *testing.T, payloads ...[]byte) (*WAL, *segment.MemBuffer) {
	t.Helper()
	buf := segment.NewMemBuffer()
	w := Open(buf)
	for _, p := range payloads {
		if _, err := w.Append(p); err != nil {
			t.Fatalf("Append: %v", err)
		}
	}
	return w, buf
}

func payloadsOf(recs []Record) [][]byte {
	out := make([][]byte, len(recs))
	for i, r := range recs {
		out[i] = r.Payload
	}
	return out
}

// 语义 5：空缓冲恢复成功且记录数为零。
func TestRecoverEmptyLog(t *testing.T) {
	w := Open(segment.NewMemBuffer())
	recs, stop := w.Recover()
	if len(recs) != 0 || stop.Reason != codec.Complete || stop.Offset != 0 {
		t.Fatalf("recs=%d stop=%+v", len(recs), stop)
	}
}

// 语义 6：空负载记录被原样回放为"负载为空"的记录。
func TestRecoverEmptyPayloadRecord(t *testing.T) {
	w, _ := openWith(t, []byte("x"), []byte{}, []byte("y"))
	w.Sync()
	recs, stop := w.Recover()
	if len(recs) != 3 || stop.Reason != codec.Complete {
		t.Fatalf("recs=%d stop=%+v", len(recs), stop)
	}
	if recs[1].Payload == nil || len(recs[1].Payload) != 0 {
		t.Fatalf("record 1 payload=%v, want non-nil empty", recs[1].Payload)
	}
}

// 语义 7：写入点与同步点可读出；恢复只回放到同步点为止。
func TestSyncPointLimitsRecovery(t *testing.T) {
	w, _ := openWith(t, []byte("a"), []byte("b"))
	if w.Written() == 0 || w.Synced() != 0 {
		t.Fatalf("written=%d synced=%d", w.Written(), w.Synced())
	}
	w.Sync()
	synced := w.Synced()
	if synced != w.Written() {
		t.Fatalf("after Sync: synced=%d written=%d", synced, w.Written())
	}
	// 同步点之后再写一条完整记录，不回放。
	if _, err := w.Append([]byte("c")); err != nil {
		t.Fatalf("Append: %v", err)
	}
	if w.Written() <= synced || w.Synced() != synced {
		t.Fatalf("written=%d synced=%d, want written>%d synced=%d",
			w.Written(), w.Synced(), synced, synced)
	}
	recs, stop := w.Recover()
	if len(recs) != 2 || stop.Offset != synced {
		t.Fatalf("recs=%d stop=%+v, want 2 records stopping at %d",
			len(recs), stop, synced)
	}
	if !bytes.Equal(recs[0].Payload, []byte("a")) ||
		!bytes.Equal(recs[1].Payload, []byte("b")) {
		t.Fatalf("payloads=%q", payloadsOf(recs))
	}
}

// 语义 2/4：单字节翻转后，恢复停在该记录并报校验不符。
func TestRecoverStopsAtCorruptRecord(t *testing.T) {
	w, buf := openWith(t, []byte("good-1"), []byte("good-2"), []byte("good-3"))
	w.Sync()
	recs, _ := w.Recover()
	damageAt := recs[1].Offset
	buf.Bytes()[damageAt+codec.LenSize] ^= 0x80
	recs, stop := w.Recover()
	if len(recs) != 1 || !bytes.Equal(recs[0].Payload, []byte("good-1")) {
		t.Fatalf("recs=%q", payloadsOf(recs))
	}
	if stop.Offset != damageAt || stop.Reason != codec.Corrupt {
		t.Fatalf("stop=%+v, want Corrupt@%d", stop, damageAt)
	}
}

// 语义 3：截断后恢复恰好回放到截断点之前的完整记录。
func TestRecoverAfterTruncation(t *testing.T) {
	w, buf := openWith(t, []byte("one"), []byte("two"), []byte("three"))
	w.Sync()
	recs, _ := w.Recover()
	buf.Truncate(recs[1].Offset + 2) // 截在第二条记录中间
	recs, stop := w.Recover()
	if len(recs) != 1 || !bytes.Equal(recs[0].Payload, []byte("one")) {
		t.Fatalf("recs=%q", payloadsOf(recs))
	}
	if stop.Reason != codec.Truncated {
		t.Fatalf("stop=%+v, want Truncated", stop)
	}
}

// 语义 8：多次追加后，历史前缀逐字节不变。
func TestAppendKeepsHistoryPrefix(t *testing.T) {
	w, _ := openWith(t, []byte("stable"))
	prev := w.Snapshot()
	for i := 0; i < 20; i++ {
		if _, err := w.Append([]byte{byte(i)}); err != nil {
			t.Fatalf("Append: %v", err)
		}
		now := w.Snapshot()
		if !bytes.Equal(now[:len(prev)], prev) {
			t.Fatalf("append %d mutated history prefix", i)
		}
		prev = now
	}
}

// 语义 9：并发追加不交错，恢复全部回放且偏移与返回值一致。
func TestConcurrentAppend(t *testing.T) {
	w := Open(segment.NewMemBuffer())
	const writers = 8
	const perWriter = 50
	var mu sync.Mutex
	sent := make(map[int][]byte, writers*perWriter)
	var wg sync.WaitGroup
	for g := 0; g < writers; g++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for i := 0; i < perWriter; i++ {
				p := []byte(fmt.Sprintf("w%02d-%04d", id, i))
				off, err := w.Append(p)
				if err != nil {
					t.Errorf("Append: %v", err)
					return
				}
				mu.Lock()
				sent[off] = p
				mu.Unlock()
			}
		}(g)
	}
	wg.Wait()
	w.Sync()
	recs, stop := w.Recover()
	if stop.Reason != codec.Complete {
		t.Fatalf("stop=%+v, want Complete", stop)
	}
	if len(recs) != writers*perWriter {
		t.Fatalf("recovered %d records, want %d", len(recs), writers*perWriter)
	}
	last := -1
	for _, r := range recs {
		want, ok := sent[r.Offset]
		if !ok {
			t.Fatalf("record at unknown offset %d", r.Offset)
		}
		if !bytes.Equal(r.Payload, want) {
			t.Fatalf("offset %d: payload=%q, want %q", r.Offset, r.Payload, want)
		}
		if r.Offset <= last {
			t.Fatalf("offsets not increasing: %d after %d", r.Offset, last)
		}
		last = r.Offset
	}
}

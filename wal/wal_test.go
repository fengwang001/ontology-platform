package wal

import (
	"bytes"
	"strconv"
	"sync"
	"testing"

	"ontology/segment"
)

func TestWriteAndSyncPositions(t *testing.T) {
	dev := segment.NewDevice()
	l := Open(dev)
	if l.WritePosition() != 0 || l.SyncedPosition() != 0 {
		t.Fatal("fresh log must start at 0/0")
	}
	l.Append([]byte("abc"))
	if l.WritePosition() == 0 {
		t.Fatal("write position must advance after append")
	}
	if l.SyncedPosition() != 0 {
		t.Fatal("sync position must not move before Sync")
	}
	l.Sync()
	if l.SyncedPosition() != l.WritePosition() {
		t.Fatalf("synced = %d, want %d", l.SyncedPosition(), l.WritePosition())
	}
}

func TestRecoverStopsAtSyncPoint(t *testing.T) {
	dev := segment.NewDevice()
	l := Open(dev)
	l.Append([]byte("synced-1"))
	l.Append([]byte("synced-2"))
	l.Sync()
	l.Append([]byte("unsynced-3")) // 完整但未同步，恢复时不许回放
	rec := l.Recover()
	got := payloadsOf(rec)
	if len(got) != 2 || got[0] != "synced-1" || got[1] != "synced-2" {
		t.Fatalf("replayed %q, want [synced-1 synced-2]", got)
	}
	if rec.Report.Reason != segment.StopEOF {
		t.Fatalf("reason = %v, want StopEOF", rec.Report.Reason)
	}
	if rec.Report.StopAt != l.SyncedPosition() {
		t.Fatalf("stop at %d, want sync point %d", rec.Report.StopAt, l.SyncedPosition())
	}
}

func TestAppendDoesNotModifyHistory(t *testing.T) {
	dev := segment.NewDevice()
	l := Open(dev)
	var snapshots [][]byte
	for i := 0; i < 5; i++ {
		l.Append([]byte("record-" + strconv.Itoa(i)))
		snapshots = append(snapshots, dev.Snapshot())
	}
	final := dev.Snapshot()
	for i, snap := range snapshots {
		if len(final) < len(snap) || !bytes.Equal(final[:len(snap)], snap) {
			t.Fatalf("snapshot %d is not a byte-identical prefix of final buffer", i)
		}
	}
}

func TestConcurrentAppendAndRecover(t *testing.T) {
	dev := segment.NewDevice()
	l := Open(dev)
	const writers = 8
	const perWriter = 50
	var wg sync.WaitGroup
	var mu sync.Mutex
	returned := make(map[int64]string)
	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for i := 0; i < perWriter; i++ {
				tag := "w" + strconv.Itoa(id) + "-" + strconv.Itoa(i)
				off := l.Append([]byte(tag))
				mu.Lock()
				returned[off] = tag
				mu.Unlock()
			}
		}(w)
	}
	wg.Wait()
	l.Sync()
	rec := l.Recover()
	if rec.Report.Reason != segment.StopEOF {
		t.Fatalf("reason = %v, want StopEOF", rec.Report.Reason)
	}
	if len(rec.Entries) != writers*perWriter {
		t.Fatalf("entries = %d, want %d", len(rec.Entries), writers*perWriter)
	}
	// 回放顺序按偏移递增，且每条记录的偏移与追加时返回的一致。
	prev := int64(-1)
	for _, e := range rec.Entries {
		if e.Offset <= prev {
			t.Fatalf("offsets not increasing: %d after %d", e.Offset, prev)
		}
		prev = e.Offset
		tag, ok := returned[e.Offset]
		if !ok {
			t.Fatalf("offset %d was never returned by Append", e.Offset)
		}
		if string(e.Payload) != tag {
			t.Fatalf("payload at %d = %q, want %q", e.Offset, e.Payload, tag)
		}
	}
}

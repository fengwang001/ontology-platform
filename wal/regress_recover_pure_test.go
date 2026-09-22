package wal

import (
	"testing"

	"ontology/codec"
	"ontology/segment"
)

// 回归：钉住契约「Recover 是纯查询：绝不得改变同步点、写入点或
// 缓冲里的任何字节」。损坏点在同步点之前时，Recover 停在坏记录、
// 同步点保持不动；修好坏记录后再 Recover，已同步的记录必须全部回来。
func TestRegressRecoverDoesNotMoveSyncPoint(t *testing.T) {
	dev := segment.NewDevice()
	l := Open(dev)
	l.Append([]byte("A"))
	offB := l.Append([]byte("B"))
	l.Sync()
	l.Append([]byte("C")) // 未同步，不在回放范围

	synced := l.SyncedPosition()
	before := dev.Snapshot()

	// 损坏 B 负载的一个字节（损坏点在同步点之前）。
	dev.Flip(offB+codec.PrefixLen, 0xFF)
	rec := l.Recover()
	if rec.Report.Reason != segment.StopCorrupt || rec.Report.StopAt != offB {
		t.Fatalf("report = %+v, want Corrupt at %d", rec.Report, offB)
	}
	if got := payloadsOf(rec); len(got) != 1 || got[0] != "A" {
		t.Fatalf("replayed %q, want only [A]", got)
	}
	if l.SyncedPosition() != synced {
		t.Fatalf("synced moved to %d, want unchanged %d", l.SyncedPosition(), synced)
	}
	if l.WritePosition() != int64(len(before)) {
		t.Fatalf("write position moved to %d, want %d", l.WritePosition(), len(before))
	}

	// 修好 B（把翻转的字节翻回来），同步点之内的 A、B 都必须回放到。
	dev.Flip(offB+codec.PrefixLen, 0xFF)
	rec2 := l.Recover()
	if got := payloadsOf(rec2); len(got) != 2 || got[0] != "A" || got[1] != "B" {
		t.Fatalf("after repair replayed %q, want [A B]", got)
	}
	if l.SyncedPosition() != synced {
		t.Fatalf("synced moved to %d, want unchanged %d", l.SyncedPosition(), synced)
	}

	// 未同步的 C 只有显式 Sync 之后才进入回放范围。
	l.Sync()
	rec3 := l.Recover()
	if got := payloadsOf(rec3); len(got) != 3 || got[2] != "C" {
		t.Fatalf("after sync replayed %q, want [A B C]", got)
	}
}

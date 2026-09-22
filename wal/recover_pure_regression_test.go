package wal

import (
	"testing"

	"ontology/codec"
	"ontology/segment"
)

// 回归测试：钉住契约「Recover 是纯查询：绝不得改变同步点、
// 写入点或缓冲里的任何字节；未同步的记录只有显式 Sync 之后
// 才进入回放范围」。
// 修复前 Recover 会把同步点挪到停点：损坏点在同步点之前时，
// 一次 Recover 之后同步点回退，修好数据再 Recover 也捡不回
// 原本已同步的记录。
func TestRecoverDoesNotMoveSyncPoint(t *testing.T) {
	dev := segment.NewDevice()
	l := Open(dev)
	l.Append([]byte("A"))
	offB := l.Append([]byte("B"))
	l.Sync()
	l.Append([]byte("C")) // 未同步

	synced := l.SyncedPosition()
	writePos := l.WritePosition()
	snap := dev.Snapshot()

	// 损坏点在同步点之前：Recover 停在 B，只回放 [A]。
	dev.Flip(offB+codec.PrefixLen, 0xFF)
	rec := l.Recover()
	if rec.Report.Reason != segment.StopCorrupt || rec.Report.StopAt != offB {
		t.Fatalf("report = %+v, want Corrupt at %d", rec.Report, offB)
	}
	if got := payloadsOf(rec); len(got) != 1 || got[0] != "A" {
		t.Fatalf("replayed %q, want [A]", got)
	}
	if l.SyncedPosition() != synced {
		t.Fatalf("sync point moved: %d -> %d, want unchanged", synced, l.SyncedPosition())
	}
	if l.WritePosition() != writePos {
		t.Fatalf("write position moved: %d -> %d, want unchanged", writePos, l.WritePosition())
	}

	// 修好 B 再 Recover：同步点之内的 B 必须回放出来。
	dev.Flip(offB+codec.PrefixLen, 0xFF)
	rec = l.Recover()
	if got := payloadsOf(rec); len(got) != 2 || got[0] != "A" || got[1] != "B" {
		t.Fatalf("replayed %q after repair, want [A B]", got)
	}
	if l.SyncedPosition() != synced {
		t.Fatalf("sync point moved after second recover: %d, want %d",
			l.SyncedPosition(), synced)
	}

	// Recover 不得改动缓冲里的任何字节（与损坏前快照逐字节一致）。
	for i, b := range dev.Snapshot() {
		if b != snap[i] {
			t.Fatalf("buffer byte %d changed by Recover: %#x -> %#x", i, snap[i], b)
		}
	}

	// 未同步的 C 只有显式 Sync 之后才进入回放范围。
	if got := payloadsOf(l.Recover()); len(got) != 2 {
		t.Fatalf("replayed %q before sync, want [A B] (C unsynced)", got)
	}
	l.Sync()
	if got := payloadsOf(l.Recover()); len(got) != 3 || got[2] != "C" {
		t.Fatalf("replayed %q after sync, want [A B C]", got)
	}
}

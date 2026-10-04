package translog

import (
	"reflect"
	"testing"
)

func TestAppendSyncCrashReplay(t *testing.T) {
	l := New(0)
	if l.Generation() != 1 || l.Synced() != 0 || l.Len() != 0 {
		t.Fatalf("初始状态错误: gen=%d synced=%d len=%d", l.Generation(), l.Synced(), l.Len())
	}
	l.Append(1, IndexOp, "a", []byte("v1"))
	l.Append(2, DeleteOp, "a", nil)
	l.Append(3, IndexOp, "b", []byte("v2"))
	if l.Len() != 3 {
		t.Fatalf("追加后条数=%d, 期望 3", l.Len())
	}

	// Sync 只能前移
	l.Sync(2)
	l.Sync(1)
	if l.Synced() != 2 {
		t.Fatalf("Sync 后水位=%d, 期望 2", l.Synced())
	}
	// Replay(committed=1) 只含 (1,2]：seq 2
	got := l.Replay(1)
	want := []Entry{{Seq: 2, Op: DeleteOp, ID: "a", Body: nil}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Replay=%v, 期望 %v", got, want)
	}

	// Crash 丢弃 seq>synced 的尾部
	l.Crash()
	if l.Len() != 2 || l.Synced() != 2 || l.Generation() != 1 {
		t.Fatalf("Crash 后 len=%d synced=%d gen=%d", l.Len(), l.Synced(), l.Generation())
	}
	// Replay 返回副本，改副本不影响日志
	got = l.Replay(0)
	got[0].ID = "xxx"
	again := l.Replay(0)
	if again[0].ID != "a" {
		t.Fatalf("Replay 未返回副本，日志被外部修改: %v", again)
	}

	// Rotate 换代、清记录、保水位
	l.Rotate()
	if l.Generation() != 2 || l.Len() != 0 || l.Synced() != 2 {
		t.Fatalf("Rotate 后 gen=%d len=%d synced=%d", l.Generation(), l.Len(), l.Synced())
	}
	if rs := l.Replay(0); len(rs) != 0 {
		t.Fatalf("换代后重放=%v, 期望空", rs)
	}
}

func TestCrashTruncatesOnlyTail(t *testing.T) {
	// synced 落后两档：Crash 后再 Append 并 Sync，再次 Crash 验证新尾部被截、旧记录保留
	l := New(0)
	for seq := int64(1); seq <= 5; seq++ {
		l.Append(seq, IndexOp, "id", []byte("x"))
	}
	l.Sync(3)
	l.Crash()
	if l.Len() != 3 {
		t.Fatalf("第一次 Crash 后 len=%d, 期望 3", l.Len())
	}
	l.Append(4, IndexOp, "id", []byte("y"))
	l.Sync(4)
	l.Crash()
	if l.Len() != 4 {
		t.Fatalf("第二次 Crash 后 len=%d, 期望 4", l.Len())
	}
}

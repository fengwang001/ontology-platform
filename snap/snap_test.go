package snap_test

import (
	"testing"

	"ontology/snap"
)

// TestConsiderGeneratesOnMultiples 仅在 P 的倍数帧生成快照，窗口和惰性求值。
func TestConsiderGeneratesOnMultiples(t *testing.T) {
	tr := snap.NewTracker(10)
	calls := 0
	windowSum := func() int64 { calls++; return 42 }
	for cur := int64(1); cur <= 25; cur++ {
		s, ok := tr.Consider(cur, windowSum)
		if want := cur%10 == 0; ok != want {
			t.Fatalf("cur=%d Consider ok=%v，期望 %v", cur, ok, want)
		}
		if ok && (s.Tick != cur || s.Size != 42) {
			t.Fatalf("cur=%d 快照 %+v 不符", cur, s)
		}
	}
	if calls != 2 {
		t.Fatalf("windowSum 求值 %d 次，期望 2（仅快照帧）", calls)
	}
	s, ok := tr.Latest()
	if !ok || s.Tick != 20 || s.Size != 42 {
		t.Fatalf("Latest()=%+v,%v，期望 {20 42},true", s, ok)
	}
}

// TestLatestEmpty 尚无快照时 Latest 返回 false，cur=0 不生成。
func TestLatestEmpty(t *testing.T) {
	tr := snap.NewTracker(5)
	if _, ok := tr.Latest(); ok {
		t.Fatal("尚无快照时 Latest 应返回 false")
	}
	if _, ok := tr.Consider(0, func() int64 { return 1 }); ok {
		t.Fatal("cur=0 不应生成快照")
	}
}

// TestKeepsOnlyLatest 只保留最新一份快照。
func TestKeepsOnlyLatest(t *testing.T) {
	tr := snap.NewTracker(3)
	tr.Consider(3, func() int64 { return 100 })
	tr.Consider(6, func() int64 { return 200 })
	s, ok := tr.Latest()
	if !ok || s.Tick != 6 || s.Size != 200 {
		t.Fatalf("Latest()=%+v,%v，期望只保留 {6 200}", s, ok)
	}
}

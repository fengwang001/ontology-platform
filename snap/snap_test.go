package snap

import "testing"

func TestStoreOnlyLatest(t *testing.T) {
	s := New(10)
	if s.Tick() != 0 || s.Size() != 0 {
		t.Fatalf("空存储 tick=%d size=%d", s.Tick(), s.Size())
	}
	s.Observe(9, 100)
	if s.Tick() != 0 {
		t.Fatalf("非周期帧不应生成快照 tick=%d", s.Tick())
	}
	s.Observe(10, 30)
	s.Observe(20, 35)
	if s.Tick() != 20 || s.Size() != 35 {
		t.Fatalf("只保留最新快照 tick=%d size=%d（期望 20/35）", s.Tick(), s.Size())
	}
}

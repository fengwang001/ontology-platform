package pit

import (
	"errors"
	"fmt"
	"testing"

	"ontology/segstore"
)

func addSeg(t *testing.T, s *segstore.Store, now int64, prefix string, n int, valBase int64) int64 {
	t.Helper()
	docs := make([]segstore.Doc, n)
	for i := range docs {
		docs[i] = segstore.Doc{ID: fmt.Sprintf("%s%05d", prefix, i), SortVal: valBase + int64(i)}
	}
	num, err := s.AddSegment(now, docs)
	if err != nil {
		t.Fatalf("add segment: %v", err)
	}
	return num
}

// touched 证明 Open/Close 的代价只与段数有关，与文档总数无关。
func TestTouchedIndependentOfDocCount(t *testing.T) {
	run := func(docsPerSeg int) int64 {
		s := segstore.NewStore()
		m := NewManager(s, 10)
		for i := 0; i < 3; i++ {
			addSeg(t, s, int64(i), fmt.Sprintf("s%d_", i), docsPerSeg, 0)
		}
		before := m.touched
		pid, err := m.Open(10, 100)
		if err != nil {
			t.Fatalf("open: %v", err)
		}
		if err := m.Close(11, pid); err != nil {
			t.Fatalf("close: %v", err)
		}
		return m.touched - before
	}
	small := run(10)
	large := run(10000)
	if small != large {
		t.Fatalf("touched depends on doc count: 10/seg -> %d, 10000/seg -> %d", small, large)
	}
	// 上界：当前视图段数 + PIT 所持段数 = 3 + 3。
	if small != 6 {
		t.Fatalf("touched = %d, want 6 (|view| + |pit segs|)", small)
	}
}

func TestOpenParamValidation(t *testing.T) {
	cases := []struct {
		name    string
		now, ka int64
	}{
		{"ka zero", 0, 0},
		{"ka too large", 0, MaxKA + 1},
		{"now negative", -1, 1},
		{"now too large", segstore.MaxNow + 1, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := NewManager(segstore.NewStore(), 1)
			if _, err := m.Open(tc.now, tc.ka); !errors.Is(err, segstore.ErrInvalidParam) {
				t.Fatalf("err = %v, want ErrInvalidParam", err)
			}
		})
	}
}

func TestNewManagerPanicsOnBadPmax(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("want panic for Pmax out of [1,1000]")
		}
	}()
	NewManager(segstore.NewStore(), 0)
}

func TestOpenCloseReleaseTiming(t *testing.T) {
	s := segstore.NewStore()
	m := NewManager(s, 2)
	addSeg(t, s, 0, "a_", 2, 5) // 段 1
	addSeg(t, s, 0, "b_", 2, 5) // 段 2
	pid, err := m.Open(10, 20)
	if err != nil || pid != 1 {
		t.Fatalf("pid = %d, err = %v", pid, err)
	}
	if _, err := s.Merge(12, []int64{1, 2}); err != nil { // 段 3
		t.Fatalf("merge: %v", err)
	}
	if got := s.Released(); len(got) != 0 {
		t.Fatalf("released too early: %v", got)
	}
	if err := m.Close(20, pid); err != nil {
		t.Fatalf("close: %v", err)
	}
	if got := fmt.Sprint(s.Released()); got != "[1 2]" {
		t.Fatalf("released = %s, want [1 2]", got)
	}
	if err := m.Close(21, pid); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestExpiryLandedByRejectedOp(t *testing.T) {
	s := segstore.NewStore()
	m := NewManager(s, 2)
	addSeg(t, s, 0, "a_", 2, 5) // 段 1
	addSeg(t, s, 0, "b_", 2, 5) // 段 2
	pid, err := m.Open(10, 20)  // exp = 30
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Merge(12, []int64{1, 2}); err != nil {
		t.Fatalf("merge: %v", err)
	}
	// 被拒的 Delete（文档不存在）仍先落地过期 PIT 并推进时钟。
	if err := s.Delete(35, "ghost"); !errors.Is(err, segstore.ErrDocNotFound) {
		t.Fatalf("err = %v, want ErrDocNotFound", err)
	}
	if got := fmt.Sprint(s.Released()); got != "[1 2]" {
		t.Fatalf("released = %s, want [1 2]", got)
	}
	if err := m.Close(36, pid); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
	// 时钟已推进到 35。
	if err := s.Delete(34, "ghost"); !errors.Is(err, segstore.ErrClock) {
		t.Fatalf("err = %v, want ErrClock", err)
	}
}

func TestPITLimit(t *testing.T) {
	s := segstore.NewStore()
	m := NewManager(s, 2)
	addSeg(t, s, 0, "a_", 1, 1)
	if _, err := m.Open(1, 100); err != nil { // PIT 1, exp=101
		t.Fatal(err)
	}
	if _, err := m.Open(2, 100); err != nil { // PIT 2, exp=102
		t.Fatal(err)
	}
	if _, err := m.Open(3, 100); !errors.Is(err, ErrLimit) {
		t.Fatalf("err = %v, want ErrLimit", err)
	}
	// 超限被拒不占 PIT 编号；t=200 落地两个过期 PIT 后可再开，编号为 3。
	if _, err := s.AddSegment(200, []segstore.Doc{{ID: "z", SortVal: 1}}); err != nil {
		t.Fatal(err)
	}
	pid, err := m.Open(201, 100)
	if err != nil {
		t.Fatalf("open after landing: %v", err)
	}
	if pid != 3 {
		t.Fatalf("pid = %d, want 3 (rejected open must not consume a number)", pid)
	}
}

func TestSharedSegmentReleasedAfterLastLanding(t *testing.T) {
	s := segstore.NewStore()
	m := NewManager(s, 5)
	addSeg(t, s, 0, "a_", 2, 5) // 段 1
	addSeg(t, s, 0, "b_", 2, 7) // 段 2
	p1, err := m.Open(1, 10)    // exp=11
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.Open(2, 100); err != nil { // PIT 2, exp=102
		t.Fatal(err)
	}
	if _, err := s.Merge(3, []int64{1, 2}); err != nil { // 段 3
		t.Fatalf("merge: %v", err)
	}
	// 关闭 p1：段 1、2 仍被 p2 持有，不释放。
	if err := m.Close(4, p1); err != nil {
		t.Fatal(err)
	}
	if got := s.Released(); len(got) != 0 {
		t.Fatalf("released = %v, want empty", got)
	}
	// p2 过期落地后，最后一个引用消失才释放。
	if _, err := s.AddSegment(200, []segstore.Doc{{ID: "z", SortVal: 1}}); err != nil {
		t.Fatal(err)
	}
	if got := fmt.Sprint(s.Released()); got != "[1 2]" {
		t.Fatalf("released = %s, want [1 2]", got)
	}
}

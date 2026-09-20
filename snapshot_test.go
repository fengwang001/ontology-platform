package ontology

import (
	"errors"
	"slices"
	"testing"
)

// 回收以引用为准：有未归还快照指向的版本一律不回收，
// 归还后可以被回收，且返回实际回收的版本号；当前版本永不回收。
func TestCollectRespectsReferences(t *testing.T) {
	m := newTestManager(t)
	s := m.Acquire() // 指向 v1
	mustUpdate(t, m, map[string]any{"host": "b"})

	if gone := m.Collect(); len(gone) != 0 {
		t.Fatalf("Collect with live reference = %v, want empty", gone)
	}
	s.Release()
	gone := m.Collect()
	if !slices.Equal(gone, []uint64{1}) {
		t.Fatalf("Collect after release = %v, want [1]", gone)
	}
	// 当前版本（v2）即使无引用也永远不回收。
	if gone := m.Collect(); len(gone) != 0 {
		t.Fatalf("Collect again = %v, want empty", gone)
	}
}

// 过期快照：指向已回收版本的快照读取返回 ErrVersionReclaimed。
func TestStaleSnapshotReclaimedError(t *testing.T) {
	m := newTestManager(t)
	s := m.Acquire() // 指向 v1
	mustUpdate(t, m, map[string]any{"host": "b"})
	s.Release()
	if gone := m.Collect(); !slices.Equal(gone, []uint64{1}) {
		t.Fatalf("Collect = %v, want [1]", gone)
	}
	if _, err := s.Get("host"); !errors.Is(err, ErrVersionReclaimed) {
		t.Fatalf("stale Get err = %v, want ErrVersionReclaimed", err)
	}
	if _, err := s.SourceVersion("host"); !errors.Is(err, ErrVersionReclaimed) {
		t.Fatalf("stale SourceVersion err = %v, want ErrVersionReclaimed", err)
	}
	// 与"已归还"错误可区分。
	if _, err := s.Get("host"); errors.Is(err, ErrSnapshotReleased) {
		t.Fatal("stale Get must not report ErrSnapshotReleased")
	}
}

// 已归还快照：版本仍在（当前版本）时读取返回 ErrSnapshotReleased。
func TestReleasedSnapshotError(t *testing.T) {
	m := newTestManager(t)
	s := m.Acquire()
	s.Release()
	if _, err := s.Get("host"); !errors.Is(err, ErrSnapshotReleased) {
		t.Fatalf("released Get err = %v, want ErrSnapshotReleased", err)
	}
	if _, err := s.SourceVersion("host"); !errors.Is(err, ErrSnapshotReleased) {
		t.Fatalf("released SourceVersion err = %v, want ErrSnapshotReleased", err)
	}
	if _, err := s.Get("host"); errors.Is(err, ErrVersionReclaimed) {
		t.Fatal("released Get must not report ErrVersionReclaimed")
	}
}

// 归还幂等：重复归还不 panic，引用计数不会减成负数。
func TestDoubleReleaseIdempotent(t *testing.T) {
	m := newTestManager(t)
	s := m.Acquire()
	s.Release()
	s.Release()
	s.Release()
	rep := m.Leaks()
	if rep.Outstanding != 0 {
		t.Fatalf("outstanding = %d, want 0", rep.Outstanding)
	}
	if len(rep.ByVersion) != 0 {
		t.Fatalf("by-version = %v, want empty", rep.ByVersion)
	}
	// 再取新快照，计数从正确的基线继续。
	s2 := m.Acquire()
	if rep := m.Leaks(); rep.Outstanding != 1 {
		t.Fatalf("outstanding after new acquire = %d, want 1", rep.Outstanding)
	}
	s2.Release()
}

// 泄漏报告：准确反映未归还快照的数量与各自指向的版本。
func TestLeakReport(t *testing.T) {
	m := newTestManager(t)
	s1 := m.Acquire()
	s2 := m.Acquire()
	mustUpdate(t, m, map[string]any{"host": "b"})
	s3 := m.Acquire()

	rep := m.Leaks()
	if rep.Outstanding != 3 {
		t.Fatalf("outstanding = %d, want 3", rep.Outstanding)
	}
	want := map[uint64]int{1: 2, 2: 1}
	if len(rep.ByVersion) != len(want) {
		t.Fatalf("by-version = %v, want %v", rep.ByVersion, want)
	}
	for v, n := range want {
		if rep.ByVersion[v] != n {
			t.Fatalf("by-version[%d] = %d, want %d", v, rep.ByVersion[v], n)
		}
	}
	s1.Release()
	s2.Release()
	s3.Release()
	if rep := m.Leaks(); rep.Outstanding != 0 {
		t.Fatalf("outstanding after releases = %d, want 0", rep.Outstanding)
	}
}

// 读取未声明字段返回可判定的 ErrUnknownField。
func TestSnapshotUnknownField(t *testing.T) {
	m := newTestManager(t)
	s := m.Acquire()
	defer s.Release()
	if _, err := s.Get("ghost"); !errors.Is(err, ErrUnknownField) {
		t.Fatalf("Get(ghost) err = %v, want ErrUnknownField", err)
	}
}

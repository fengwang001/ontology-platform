package snapshot

import (
	"errors"
	"fmt"
	"math"
	"reflect"
	"testing"
)

func mustCommit(t *testing.T, tb *Table, ts int64, added, removed []string) Snapshot {
	t.Helper()
	s, err := tb.Commit(ts, added, removed)
	if err != nil {
		t.Fatalf("Commit(ts=%d, added=%v, removed=%v) failed: %v", ts, added, removed, err)
	}
	t.Logf("commit ts=%d added=%v removed=%v -> snapshot id=%d files=%v",
		ts, added, removed, s.ID, s.Files)
	return s
}

func mustExpire(t *testing.T, tb *Table, keepLast int, threshold int64) []string {
	t.Helper()
	deleted, err := tb.Expire(keepLast, threshold)
	if err != nil {
		t.Fatalf("Expire(keepLast=%d, threshold=%d) failed: %v", keepLast, threshold, err)
	}
	var retained []string
	for _, s := range tb.Snapshots() {
		retained = append(retained, fmt.Sprintf("id=%d(ts=%d)", s.ID, s.Timestamp))
	}
	t.Logf("expire keepLast=%d threshold=%d -> retained=%v deleted=%v store=%v",
		keepLast, threshold, retained, deleted, tb.Files())
	return deleted
}

func snapshotIDs(snaps []Snapshot) []int64 {
	ids := make([]int64, len(snaps))
	for i, s := range snaps {
		ids[i] = s.ID
	}
	return ids
}

func checkState(t *testing.T, tb *Table, wantIDs []int64, wantFiles []string) {
	t.Helper()
	if got := snapshotIDs(tb.Snapshots()); !reflect.DeepEqual(got, wantIDs) {
		t.Fatalf("snapshot ids = %v, want %v", got, wantIDs)
	}
	if got := tb.Files(); !reflect.DeepEqual(got, wantFiles) {
		t.Fatalf("store files = %v, want %v", got, wantFiles)
	}
	if err := tb.CheckConsistency(); err != nil {
		t.Fatalf("consistency check failed: %v", err)
	}
}

// TestCommitFileSetEvolution 验证新快照文件集 = 当前文件集 - removed + added。
func TestCommitFileSetEvolution(t *testing.T) {
	tb := New()
	mustCommit(t, tb, 10, []string{"a", "b", "c"}, nil)
	s := mustCommit(t, tb, 20, []string{"d"}, []string{"a"})
	if want := []string{"b", "c", "d"}; !reflect.DeepEqual(s.Files, want) {
		t.Fatalf("files = %v, want %v", s.Files, want)
	}
	// 文件存储 = 全部现存快照文件集之并：{a,b,c} ∪ {b,c,d}。
	checkState(t, tb, []int64{1, 2}, []string{"a", "b", "c", "d"})
}

// TestExpireRetentionUnion 验证三条保留条件取并：
// 最新 keepLast 个、时间戳严格大于阈值、当前快照。
func TestExpireRetentionUnion(t *testing.T) {
	tb := New()
	mustCommit(t, tb, 100, []string{"f1"}, nil)            // id=1 {f1}
	mustCommit(t, tb, 200, []string{"f2"}, []string{"f1"}) // id=2 {f2}
	mustCommit(t, tb, 300, []string{"f3"}, []string{"f2"}) // id=3 {f3}
	mustCommit(t, tb, 400, []string{"f4"}, []string{"f3"}) // id=4 {f4}
	mustCommit(t, tb, 500, []string{"f5"}, []string{"f4"}) // id=5 {f5}（当前快照）

	// keepLast=1 只保留 id=5；阈值 250 额外保留 id=3、id=4（时间条件）。
	deleted := mustExpire(t, tb, 1, 250)
	if want := []string{"f1", "f2"}; !reflect.DeepEqual(deleted, want) {
		t.Fatalf("deleted = %v, want %v", deleted, want)
	}
	checkState(t, tb, []int64{3, 4, 5}, []string{"f3", "f4", "f5"})

	// keepLast=2 保留 id=4、id=5；阈值 450 只保留 id=5。
	// id=4 仅由“最新 keepLast 个”保留，验证取并后 id=3 被过期。
	deleted = mustExpire(t, tb, 2, 450)
	if want := []string{"f3"}; !reflect.DeepEqual(deleted, want) {
		t.Fatalf("deleted = %v, want %v", deleted, want)
	}
	checkState(t, tb, []int64{4, 5}, []string{"f4", "f5"})
}

// TestCurrentSnapshotNeverExpires 当前快照永不过期，
// 即使 keepLast=0 且阈值大于一切时间戳。
func TestCurrentSnapshotNeverExpires(t *testing.T) {
	tb := New()
	mustCommit(t, tb, 1, []string{"a"}, nil)
	mustCommit(t, tb, 2, []string{"b"}, []string{"a"})

	deleted := mustExpire(t, tb, 0, math.MaxInt64)
	if want := []string{"a"}; !reflect.DeepEqual(deleted, want) {
		t.Fatalf("deleted = %v, want %v", deleted, want)
	}
	checkState(t, tb, []int64{2}, []string{"b"})

	// 只剩当前快照时，任何参数都不会过期它。
	deleted = mustExpire(t, tb, 0, 0)
	if len(deleted) != 0 {
		t.Fatalf("deleted = %v, want empty", deleted)
	}
	checkState(t, tb, []int64{2}, []string{"b"})
}

// TestThresholdStrictlyGreater 时间条件为严格大于：等于阈值的快照不被保留。
func TestThresholdStrictlyGreater(t *testing.T) {
	tb := New()
	mustCommit(t, tb, 100, []string{"a"}, nil)
	mustCommit(t, tb, 200, []string{"b"}, []string{"a"})
	mustCommit(t, tb, 300, []string{"c"}, []string{"b"})

	deleted := mustExpire(t, tb, 0, 200)
	if want := []string{"a", "b"}; !reflect.DeepEqual(deleted, want) {
		t.Fatalf("deleted = %v, want %v", deleted, want)
	}
	checkState(t, tb, []int64{3}, []string{"c"})
}

// TestRefCountDeletion 文件被多个快照引用时，
// 直到最后一个引用它的快照被过期才删除。
func TestRefCountDeletion(t *testing.T) {
	tb := New()
	mustCommit(t, tb, 1, []string{"a"}, nil) // id=1 {a}
	mustCommit(t, tb, 2, nil, nil)           // id=2 {a}
	mustCommit(t, tb, 3, nil, nil)           // id=3 {a}（当前）

	// id=1、id=2 过期，但 a 仍被保留的 id=3 引用，不删除。
	deleted := mustExpire(t, tb, 1, math.MaxInt64)
	if len(deleted) != 0 {
		t.Fatalf("deleted = %v, want empty", deleted)
	}
	checkState(t, tb, []int64{3}, []string{"a"})

	// 当前快照移除 a 后，id=3 成为唯一引用 a 的现存快照。
	mustCommit(t, tb, 4, []string{"b"}, []string{"a"}) // id=4 {b}
	deleted = mustExpire(t, tb, 1, math.MaxInt64)
	if want := []string{"a"}; !reflect.DeepEqual(deleted, want) {
		t.Fatalf("deleted = %v, want %v", deleted, want)
	}
	checkState(t, tb, []int64{4}, []string{"b"})
}

// TestErrorsDistinct 四类错误互不相同、可用 errors.Is 区分。
func TestErrorsDistinct(t *testing.T) {
	all := []error{ErrInvalidArgument, ErrTimestampNotIncreasing, ErrFileNameConflict, ErrRemoveNotInSnapshot}
	for i, a := range all {
		for j, b := range all {
			if i != j && errors.Is(a, b) {
				t.Fatalf("errors %d and %d should be distinct: %v vs %v", i, j, a, b)
			}
		}
	}
}

// newRejectedTestTable 构造测试用表：
// id=1 ts=10 {a,b}；id=2 ts=20 移除 a、新增 c -> {b,c}（当前）。
// 此后文件名 a 已被历史使用但不在当前快照中。
func newRejectedTestTable(t *testing.T) *Table {
	t.Helper()
	tb := New()
	mustCommit(t, tb, 10, []string{"a", "b"}, nil)
	mustCommit(t, tb, 20, []string{"c"}, []string{"a"})
	return tb
}

// TestInvalidCommitRejected 各类非法提交被整体拒绝，且拒绝后状态不变。
func TestInvalidCommitRejected(t *testing.T) {
	cases := []struct {
		name    string
		ts      int64
		added   []string
		removed []string
		wantErr error
	}{
		{"empty added name", 30, []string{""}, nil, ErrInvalidArgument},
		{"empty removed name", 30, nil, []string{""}, ErrInvalidArgument},
		{"duplicate in added", 30, []string{"x", "x"}, nil, ErrInvalidArgument},
		{"duplicate in removed", 30, nil, []string{"b", "b"}, ErrInvalidArgument},
		{"added removed overlap", 30, []string{"b"}, []string{"b"}, ErrInvalidArgument},
		{"timestamp equal", 20, []string{"x"}, nil, ErrTimestampNotIncreasing},
		{"timestamp backwards", 5, []string{"x"}, nil, ErrTimestampNotIncreasing},
		{"reuse historically used name", 30, []string{"a"}, nil, ErrFileNameConflict},
		{"reuse live name", 30, []string{"b"}, nil, ErrFileNameConflict},
		{"remove file not in snapshot", 30, nil, []string{"zzz"}, ErrRemoveNotInSnapshot},
		{"remove historically removed file", 30, nil, []string{"a"}, ErrRemoveNotInSnapshot},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tb := newRejectedTestTable(t)
			beforeSnaps := tb.Snapshots()
			beforeFiles := tb.Files()

			_, err := tb.Commit(tc.ts, tc.added, tc.removed)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("err = %v, want category %v", err, tc.wantErr)
			}
			t.Logf("rejected commit ts=%d added=%v removed=%v -> %v (state unchanged)",
				tc.ts, tc.added, tc.removed, err)

			if got := tb.Snapshots(); !reflect.DeepEqual(got, beforeSnaps) {
				t.Fatalf("snapshots changed after rejection: %v -> %v", beforeSnaps, got)
			}
			if got := tb.Files(); !reflect.DeepEqual(got, beforeFiles) {
				t.Fatalf("store changed after rejection: %v -> %v", beforeFiles, got)
			}
			if err := tb.CheckConsistency(); err != nil {
				t.Fatalf("consistency check failed after rejection: %v", err)
			}
		})
	}
}

// TestExpireInvalidKeepLast 非法保留数量被拒绝且状态不变。
func TestExpireInvalidKeepLast(t *testing.T) {
	tb := newRejectedTestTable(t)
	beforeSnaps := tb.Snapshots()
	beforeFiles := tb.Files()

	if _, err := tb.Expire(-1, 0); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("err = %v, want %v", err, ErrInvalidArgument)
	}
	t.Logf("rejected expire keepLast=-1 (state unchanged)")

	if got := tb.Snapshots(); !reflect.DeepEqual(got, beforeSnaps) {
		t.Fatalf("snapshots changed after rejection")
	}
	if got := tb.Files(); !reflect.DeepEqual(got, beforeFiles) {
		t.Fatalf("store changed after rejection")
	}
}

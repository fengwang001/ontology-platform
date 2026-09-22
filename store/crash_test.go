package store

import (
	"errors"
	"testing"
)

// allCrashPoints 先空跑一次提交，枚举流水线会经过的全部崩溃点。
func allCrashPoints(t *testing.T, keys []string) []CrashPoint {
	t.Helper()
	s := newStore(t, Config{})
	var points []CrashPoint
	s.SetCrashHook(func(p CrashPoint) bool {
		points = append(points, p)
		return false
	})
	tx, _ := s.BeginTx()
	for _, k := range keys {
		if err := tx.Write(k, []byte("val-"+k)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if len(points) == 0 {
		t.Fatalf("未枚举到任何崩溃点")
	}
	return points
}

// 第 8 条：遍历所有崩溃点。每个点崩溃并恢复后，
// 事务要么完全不可见，要么完全可见，不允许半可见状态。
func TestCrashPointEnumeration(t *testing.T) {
	keys := []string{"k1", "k2"}
	points := allCrashPoints(t, keys)
	t.Logf("共枚举 %d 个崩溃点", len(points))
	for _, p := range points {
		t.Run(p.StageString(), func(t *testing.T) {
			s := newStore(t, Config{})
			s.SetCrashHook(func(cp CrashPoint) bool { return cp == p })
			tx, _ := s.BeginTx()
			for _, k := range keys {
				if err := tx.Write(k, []byte("val-"+k)); err != nil {
					t.Fatal(err)
				}
			}
			err := tx.Commit()
			committed := p.Stage == StageAfterMark
			if !committed && !errors.Is(err, ErrCrashed) {
				t.Fatalf("点 %v 崩溃应返回 ErrCrashed，得到 %v", p, err)
			}
			s.Recover() // 模拟重启
			if err := s.checkConsistency(); err != nil {
				t.Fatalf("点 %v 恢复后出现半可见状态: %v", p, err)
			}
			snap, _ := s.Begin()
			defer s.Release(snap)
			for _, k := range keys {
				got, lk := s.ReadAt(snap, k)
				if committed {
					if lk != LookupFound || string(got) != "val-"+k {
						t.Fatalf("点 %v 已提交，键 %s 应完全可见，得到 %q/%v", p, k, got, lk)
					}
				} else if lk != LookupNever {
					t.Fatalf("点 %v 未提交，键 %s 应完全不可见，得到 %v", p, k, lk)
				}
			}
			if !committed {
				if st := s.Stats(); st.TotalVersions != 0 {
					t.Fatalf("点 %v 恢复后不得有版本残留: %+v", p, st)
				}
			}
		})
	}
}

// StageString 便于测试输出定位。
func (p CrashPoint) StageString() string {
	names := []string{"BeforeAppend", "AfterAppend", "AfterIndex", "BeforeMark", "AfterMark"}
	return names[int(p.Stage)] + ":" + p.Key
}

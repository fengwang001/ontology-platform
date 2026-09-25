package state

import (
	"errors"
	"sync"
	"testing"
)

// TestStateTransitions 表驱动核验状态机判定：共享/互斥、写者优先闸门、
// 升级路径，以及每一类拒绝都返回对应哨兵错误且零写入。
func TestStateTransitions(t *testing.T) {
	cases := []struct {
		name    string
		prep    func(s *State)
		act     func(s *State) (bool, error)
		granted bool
		wantErr error
		after   func(s *State) bool
	}{
		{"空闲获读", nil, func(s *State) (bool, error) { return s.TryRead(1) }, true, nil,
			func(s *State) bool { return len(s.readers) == 1 && s.writer == -1 }},
		{"读者共享", func(s *State) { s.readers[1] = struct{}{} },
			func(s *State) (bool, error) { return s.TryRead(2) }, true, nil,
			func(s *State) bool { return len(s.readers) == 2 }},
		{"有写者不获读", func(s *State) { s.writer = 7 },
			func(s *State) (bool, error) { return s.TryRead(1) }, false, nil,
			func(s *State) bool { return len(s.readers) == 0 }},
		{"等待写者闸门", func(s *State) { s.waitW = 1 },
			func(s *State) (bool, error) { return s.TryRead(1) }, false, nil,
			func(s *State) bool { return len(s.readers) == 0 }},
		{"空闲获写", nil, func(s *State) (bool, error) { return s.TryWrite(1) }, true, nil,
			func(s *State) bool { return s.writer == 1 }},
		{"有读者不获写", func(s *State) { s.readers[1] = struct{}{} },
			func(s *State) (bool, error) { return s.TryWrite(2) }, false, nil,
			func(s *State) bool { return s.writer == -1 }},
		{"有写者不获写", func(s *State) { s.writer = 7 },
			func(s *State) (bool, error) { return s.TryWrite(2) }, false, nil, nil},
		{"读重入", func(s *State) { s.readers[1] = struct{}{} },
			func(s *State) (bool, error) { return s.TryRead(1) }, false, ErrReaderReentry,
			func(s *State) bool { return len(s.readers) == 1 }},
		{"写重入", func(s *State) { s.writer = 1 },
			func(s *State) (bool, error) { return s.TryWrite(1) }, false, ErrWriterReentry, nil},
		{"未持读释放", nil, func(s *State) (bool, error) { return false, s.ReleaseRead(1) },
			false, ErrNotReader, nil},
		{"未持写释放", nil, func(s *State) (bool, error) { return false, s.ReleaseWrite(1) },
			false, ErrNotWriter, nil},
		{"唯一读者升级", func(s *State) { s.readers[1] = struct{}{} },
			func(s *State) (bool, error) { return true, s.Upgrade(1) }, true, nil,
			func(s *State) bool { return s.writer == 1 && len(s.readers) == 0 }},
		{"未持读升级", nil, func(s *State) (bool, error) { err := s.Upgrade(1); return err == nil, err },
			false, ErrUpgradeNotReader, nil},
		{"升级冲突", func(s *State) { s.readers[1] = struct{}{}; s.readers[2] = struct{}{} },
			func(s *State) (bool, error) { err := s.Upgrade(1); return err == nil, err }, false, ErrUpgradeConflict,
			func(s *State) bool { return s.writer == -1 && len(s.readers) == 2 }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := New()
			if c.prep != nil {
				c.prep(s)
			}
			before := s.Snapshot()
			ok, err := c.act(s)
			if ok != c.granted || !errors.Is(err, c.wantErr) {
				t.Fatalf("got granted=%v err=%v, want %v / %v", ok, err, c.granted, c.wantErr)
			}
			if err != nil && !equalSnap(before, s.Snapshot()) {
				t.Fatalf("被拒操作改变了状态")
			}
			if c.after != nil && !c.after(s) {
				t.Fatalf("操作后状态不符: %+v", s.Snapshot())
			}
		})
	}
}

// TestProbeBoundConstant 多档 m：m 个 owner 稳定持读时，获写的每次自旋判定
// 访问的状态字段数恒为不超过 3 的小常数，与 m 无关（O(1)，非整表扫描）。
func TestProbeBoundConstant(t *testing.T) {
	for _, m := range []int{100, 1000, 10000} {
		s := New()
		var wg sync.WaitGroup
		for o := 0; o < m; o++ {
			wg.Add(1)
			go func(o int) {
				defer wg.Done()
				if ok, err := s.TryRead(o); !ok || err != nil {
					t.Error()
				}
			}(o)
		}
		wg.Wait() // 全部稳定持读
		if len(s.Snapshot().Readers) != m {
			t.Fatalf("m=%d 读者数不符", m)
		}
		for i := 0; i < 1000; i++ {
			if ok, _ := s.TryWrite(m); ok || s.probe > 3 {
				t.Fatalf("m=%d 第 %d 次自旋判定 granted=%v probe=%d", m, i, ok, s.probe)
			}
		}
	}
}

func equalSnap(a, b Snapshot) bool {
	if a.Writer != b.Writer || a.WaitingWriters != b.WaitingWriters ||
		len(a.Readers) != len(b.Readers) || len(a.WaitingReaders) != len(b.WaitingReaders) {
		return false
	}
	for i := range a.Readers {
		if a.Readers[i] != b.Readers[i] {
			return false
		}
	}
	return true
}

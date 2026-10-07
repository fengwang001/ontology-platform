package lsm

import (
	"bytes"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
)

// TestConcurrentOps 所有操作并发调用时结果等价于某个串行顺序：
// 互斥锁串行化保证线性一致；本测试在 -race 下验证无数据竞争、
// 无死锁，且结束后各层不变量仍然成立。
func TestConcurrentOps(t *testing.T) {
	s := newTestService(t, Config{NumLevels: 4, L0Trigger: 2, BaseLevelBytes: 500, LevelMultiplier: 10})
	var nextID uint64 = 1
	var wg sync.WaitGroup
	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < 50; i++ {
				id := atomic.AddUint64(&nextID, 1) - 1
				lo := byte((w*50 + i) * 3 % 200)
				f := FileMeta{ID: id, Level: 0, Smallest: k(lo), Largest: k(lo + 20), Size: 10}
				if err := s.AddFile(f); err != nil {
					t.Errorf("AddFile failed: %v", err)
					return
				}
			}
		}(w)
	}
	for w := 0; w < 2; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < 100; i++ {
				p, err := s.Pick()
				if err != nil {
					t.Errorf("Pick failed: %v", err)
					return
				}
				if p == nil {
					continue
				}
				if (i+w)%3 == 2 {
					if err := s.Cancel(p.ID); err != nil {
						t.Errorf("Cancel failed: %v", err)
						return
					}
					continue
				}
				// 输出取全部输入的合并区间；并发下目标层可能已新增
				// 重叠文件导致安装失败，此时取消即可。
				in := inputInterval(p.allInputs())
				out := FileMeta{
					ID:       atomic.AddUint64(&nextID, 1) - 1,
					Level:    p.TargetLevel,
					Smallest: in.lo,
					Largest:  in.hi,
					Size:     10,
				}
				if err := s.Install(p.ID, []FileMeta{out}); err != nil {
					if errors.Is(err, ErrInvariant) || errors.Is(err, ErrFilePinned) {
						_ = s.Cancel(p.ID)
						continue
					}
					t.Errorf("Install failed unexpectedly: %v", err)
					return
				}
			}
		}(w)
	}
	wg.Wait()
	// 结束后逐层校验不变量：非零层相邻文件 max <= 下一个 min。
	for l := 1; l < 4; l++ {
		files, err := s.Files(l)
		if err != nil {
			t.Fatalf("Files(%d) failed: %v", l, err)
		}
		for i := 1; i < len(files); i++ {
			if bytes.Compare(files[i-1].Largest, files[i].Smallest) > 0 {
				t.Fatalf("level %d invariant broken between file %d and %d", l, files[i-1].ID, files[i].ID)
			}
		}
	}
}

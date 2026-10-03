package scan

import (
	"sort"
	"sync"
	"testing"
)

// 测试专用：通过包内访问读取引擎内部 T/a/epoch/Suspect/maxNow。

func (e *Engine) testGet(k int64) (int64, bool) {
	s := e.shards[int(k%int64(e.partCount))]
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.tab.Get(k)
}

func (e *Engine) testAbsent(k int64) int {
	s := e.shards[int(k%int64(e.partCount))]
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.tab.AbsentCount(k)
}

func (e *Engine) testEpoch(part int) int {
	s := e.shards[part]
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.epoch
}

func (e *Engine) testSuspect(part int) bool {
	s := e.shards[part]
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cb.Suspected()
}

func (e *Engine) testLive(part int) int {
	s := e.shards[part]
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.tab.LiveCount()
}

func (e *Engine) testMaxNow() int64 {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.maxNow
}

// seedScan 初始化：对每个分区开一轮完整扫描报告 keys 并结束。
func seedScan(t *testing.T, e *Engine, keys map[int64]int64, startNow int64) int64 {
	t.Helper()
	byPart := map[int][]Row{}
	for k, v := range keys {
		p := int(k % int64(e.partCount))
		byPart[p] = append(byPart[p], Row{K: k, V: v})
	}
	parts := make([]int, 0, len(byPart))
	for p := range byPart {
		parts = append(parts, p)
	}
	sort.Ints(parts)
	now := startNow
	for _, p := range parts {
		ep, err := e.Begin(p, now)
		if err != nil {
			t.Fatalf("seed Begin(%d): %v", p, err)
		}
		if _, err := e.Report(p, ep, byPart[p], now); err != nil {
			t.Fatalf("seed Report(%d): %v", p, err)
		}
		if r, err := e.End(p, ep, true, now); err != nil || r.Deleted != 0 || r.Tripped {
			t.Fatalf("seed End(%d): %+v, %v", p, r, err)
		}
		now++
	}
	return now
}

var _ = sync.Mutex{}

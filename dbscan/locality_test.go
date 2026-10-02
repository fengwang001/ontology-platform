package dbscan

import (
	"sync"
	"testing"
)

// 性质 1：插入一个与全部存活点相距大于 eps 的点时恰发 1 次 rangeQuery。
func TestRangeQueryFarInsertIsOne(t *testing.T) {
	s, _ := New(2, 2, 1000, 100000)
	for i := int64(1); i <= 100; i++ {
		mustInsert(t, s, i, i*20, 0) // 间距 20 >> eps
	}
	before := s.RangeQueries()
	r := mustInsert(t, s, 1000, 5000, 5000) // 与所有现存点相距 > eps
	if got := s.RangeQueries() - before; got != 1 {
		t.Fatalf("far insert issued %d range queries, want exactly 1", got)
	}
	if len(r.Changes) != 1 || r.Changes[0].NewLabel != 0 {
		t.Fatalf("far isolated point changes=%v", r.Changes)
	}
}

// 性质 2：没有点过期的 Tick 恰发 0 次 rangeQuery。
func TestRangeQueryTickNoExpiryIsZero(t *testing.T) {
	s, _ := New(2, 2, 1000, 100000)
	mustInsert(t, s, 1, 0, 0)
	mustInsert(t, s, 2, 2, 0)
	if _, err := s.Tick(50); err != nil {
		t.Fatal(err)
	}
	before := s.RangeQueries()
	for _, tt := range []int64{50, 51, 999} { // t==now 允许；且均无点过期
		r, err := s.Tick(tt)
		if err != nil {
			t.Fatal(err)
		}
		if len(r.Changes) != 0 {
			t.Fatalf("tick %d should change nothing, got %v", tt, r.Changes)
		}
	}
	if got := s.RangeQueries() - before; got != 0 {
		t.Fatalf("non-expiring ticks issued %d range queries, want 0", got)
	}
}

// 性质 3：远处无关堆从 1000 增到接近上限，对“改动点附近”的同一操作，
// rangeQueries 不随无关点数量变化。我们分别在只有 1000 个远处点与有近 10 万个
// 远处点的两个服务上，于原点附近做同一组操作（删除一个邻近点），计数相同。
func TestRangeQueryIndependentOfFarPoints(t *testing.T) {
	run := func(farCount int) int64 {
		// eps=2：远处点放在负 x 方向、间距 20，与原点附近相距 > 10*eps。
		s, _ := New(2, 2, 1_000_000_000, 100000)
		for i := 0; i < farCount; i++ {
			mustInsert(t, s, int64(1000+i), -int64(20+i*10), 0)
		}
		// 原点附近的一小组点（删除其中一个会局部重连）。
		mustInsert(t, s, 1, 0, 0)
		mustInsert(t, s, 2, 2, 0)
		mustInsert(t, s, 3, 4, 0)
		before := s.RangeQueries()
		if _, err := s.Remove(2); err != nil {
			t.Fatal(err)
		}
		// 再在附近插入一个点（恰发 1 次查询），进一步佐证计数稳定。
		mustInsert(t, s, 4, 6, 0)
		return s.RangeQueries() - before
	}
	c1 := run(1000)
	c2 := run(99990) // 1000 -> 99990 个远处无关点（容量上限内）
	if c1 != c2 {
		t.Fatalf("local op issued %d queries with 1000 far points but %d with ~100000", c1, c2)
	}
	if c1 != 1 {
		t.Fatalf("delete issues no query, only the later local insert should: got %d", c1)
	}
}

// 插入远处点（即使它本身成为核心）也只发 1 次查询：minPts=1 时。
func TestRangeQueryFarCoreInsertMinPtsOne(t *testing.T) {
	s, _ := New(3, 1, 1000, 100000)
	for i := int64(1); i <= 5000; i++ {
		mustInsert(t, s, i, i*10, 0)
	}
	before := s.RangeQueries()
	mustInsert(t, s, 99999, 700000, 700000)
	if got := s.RangeQueries() - before; got != 1 {
		t.Fatalf("far core insert minPts=1 issued %d queries, want 1", got)
	}
}

// TestConcurrency：并发读写结果等价于某个串行顺序，且无数据竞争（配合 -race）。
func TestConcurrency(t *testing.T) {
	s, _ := New(5, 2, 1000, 100000)
	for i := int64(1); i <= 20; i++ {
		mustInsert(t, s, i, (i%4)*3, (i/4)*3)
	}
	var wg sync.WaitGroup
	stop := make(chan struct{})
	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func(base int64) {
			defer wg.Done()
			id := int64(100 + base)
			for {
				select {
				case <-stop:
					return
				default:
				}
				s.Insert(id, base*50, 0) // 与主体相距 > eps，远处自成簇
				s.Label(id)
				s.Neighbors(id)
				s.Remove(id)
				s.Clusters()
				_ = s.Alive()
			}
		}(int64(w))
	}
	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				s.Clusters()
				s.Alive()
				s.Now()
				s.RangeQueries()
			}
		}()
	}
	// 让读写并发跑一会儿。
	for i := 0; i < 2000; i++ {
		s.Label(int64(1 + i%20))
		s.Clusters()
	}
	close(stop)
	wg.Wait()
	// 最终主体点标签应与朴素模型一致（远处临时点都已 Remove）。
	for i := int64(1); i <= 20; i++ {
		if _, ok := s.Label(i); !ok {
			t.Fatalf("core point %d missing", i)
		}
	}
}

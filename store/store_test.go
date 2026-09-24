package store

import (
	"math/bits"
	"math/rand"
	"testing"
)

// runRandom 以固定种子执行 n 步随机 Insert/Delete/Compact（只发合法操作），
// 每步之后回调 step 做断言；返回最终 Store 与存活键集合。
func runRandom(t *testing.T, seed int64, n int, step func(s *Store, live map[int64]bool)) (*Store, map[int64]bool) {
	t.Helper()
	s, err := New(0, 1024, 4, 2)
	if err != nil {
		t.Fatal(err)
	}
	rng := rand.New(rand.NewSource(seed))
	live := map[int64]bool{}
	for i := 0; i < n; i++ {
		k := int64(rng.Intn(1024))
		switch rng.Intn(4) {
		case 0, 1:
			if !live[k] {
				if err := s.Insert(k); err != nil {
					t.Fatalf("Insert(%d): %v", k, err)
				}
				live[k] = true
			}
		case 2:
			if live[k] {
				if err := s.Delete(k); err != nil {
					t.Fatalf("Delete(%d): %v", k, err)
				}
				delete(live, k)
			}
		default:
			s.Compact()
		}
		step(s, live)
	}
	return s, live
}

// 不变量1：键不丢不重——各分区 load 之和恒等于存活键数，且每个存活键
// 恰好落在二分唯一定位的那个分区里。
func TestNoLossNoDup(t *testing.T) {
	for _, seed := range []int64{1, 2, 3} {
		s, live := runRandom(t, seed, 1500, func(s *Store, live map[int64]bool) {
			total := 0
			for _, p := range s.parts {
				total += p.Load()
			}
			if total != len(live) {
				t.Fatalf("seed=%d: load 之和=%d, 存活键=%d", seed, total, len(live))
			}
		})
		for k := range live {
			i, _ := s.search(k)
			if !s.parts[i].Has(k) {
				t.Fatalf("seed=%d: 键 %d 未落在其唯一定位的分区", seed, k)
			}
		}
	}
}

// 不变量2：分区按 lo 升序、连续、互不重叠，恰好覆盖 [0,1024)，且 lo<hi。
func TestRangeInvariant(t *testing.T) {
	for _, seed := range []int64{1, 2, 3} {
		runRandom(t, seed, 1500, func(s *Store, live map[int64]bool) {
			ps := s.parts
			if ps[0].Lo() != 0 || ps[len(ps)-1].Hi() != 1024 {
				t.Fatalf("seed=%d: 未恰好覆盖 [0,1024)", seed)
			}
			for i, p := range ps {
				if p.Lo() >= p.Hi() {
					t.Fatalf("seed=%d: 空区间 [%d,%d)", seed, p.Lo(), p.Hi())
				}
				if i > 0 && ps[i-1].Hi() != p.Lo() {
					t.Fatalf("seed=%d: [%d,%d) 与 [%d,%d) 不连续或重叠",
						seed, ps[i-1].Lo(), ps[i-1].Hi(), p.Lo(), p.Hi())
				}
			}
		})
	}
}

// 不变量3：每个分区的 load 恒等于朴素重算（扫描全部存活键数落在区间内的个数）。
func TestLoadMatchesNaive(t *testing.T) {
	for _, seed := range []int64{1, 2} {
		runRandom(t, seed, 400, func(s *Store, live map[int64]bool) {
			for _, p := range s.parts {
				naive := 0
				for k := range live {
					if p.Lo() <= k && k < p.Hi() {
						naive++
					}
				}
				if naive != p.Load() {
					t.Fatalf("seed=%d: [%d,%d) load=%d, 朴素重算=%d",
						seed, p.Lo(), p.Hi(), p.Load(), naive)
				}
			}
		})
	}
}

// 复杂度：P 取 100~10000 若干档（用大量不同键铺出 P 个分区），一次 Locate
// 的分区比较数不超过 ceil(log2(P))+1（白盒读取非导出计数器，不经公开接口）。
func TestLocateBinaryBound(t *testing.T) {
	for _, target := range []int{100, 1000, 10000} {
		s, err := New(0, 1<<62, 2, 1)
		if err != nil {
			t.Fatal(err)
		}
		for k := int64(0); s.numParts() < target; k++ {
			if err := s.Insert(k*2654435761 + 12345); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := s.Locate(12345); err != nil {
			t.Fatal(err)
		}
		bound := bits.Len(uint(s.numParts()-1)) + 1
		if p := s.numParts(); s.lastCompares.Load() > int64(bound) {
			t.Errorf("P=%d: 比较数=%d, 上界=%d", p, s.lastCompares.Load(), bound)
		}
	}
}

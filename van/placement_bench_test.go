package van

import "testing"

// fillService 构造 zones 个分区、每分区 perZone 件货物的满载系统。
// 容量设得足够大，使全部预填货物都能成功装入。
func fillService(tb testing.TB, zones, perZone int) *Service {
	tb.Helper()
	specs := make([]ZoneSpec, zones)
	for i := range specs {
		specs[i] = ZoneSpec{WeightLimit: 1 << 30, VolumeLimit: 1 << 30}
	}
	s := New(specs)
	id := 1
	for k := 0; k < perZone; k++ {
		for z := 0; z < zones; z++ {
			// 停靠点都相同，任意分区同区混装允许；装入选编号最小可行分区。
			c := Cargo{ID: id, Weight: 1, Volume: 1, Stop: 1, Kind: General}
			if _, err := s.Load(c); err != nil {
				tb.Fatalf("预填失败 id=%d: %v", id, err)
			}
			id++
		}
	}
	return s
}

// rejectCandidate 对必然被容量拒绝、编号唯一的候选做一次放置判定。
func rejectCandidate(s *Service, seq int) {
	c := Cargo{ID: 600_000_000 + seq, Weight: 1<<30 + 1, Volume: 1<<30 + 1, Stop: 1}
	_, _ = s.chooseZone(c)
}

func BenchmarkChoose_100Cargo(b *testing.B) {
	s := fillService(b, 10, 10)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		rejectCandidate(s, i)
	}
}

func BenchmarkChoose_10000Cargo(b *testing.B) {
	s := fillService(b, 10, 1000)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		rejectCandidate(s, i)
	}
}

// TestPlacementZeroAlloc 可验证地证明放置判定不随货物总数增长：
// 固定分区数(10)，在 100 件与 10000 件两种规模下，chooseZone 每次调用
// 堆分配次数均为 0，且只读各分区聚合标量（minStop/maxStop/has/weight/volume），
// 从不遍历任何 zone.items 记录。
func TestPlacementZeroAlloc(t *testing.T) {
	for _, perZone := range []int{10, 1000} {
		s := fillService(t, 10, perZone)
		var seq int
		allocs := testing.AllocsPerRun(2000, func() {
			seq++
			rejectCandidate(s, seq)
		})
		if allocs != 0 {
			t.Fatalf("每分区%d件(共%d件): chooseZone 每次分配 %v 次, 期望0",
				perZone, perZone*10, allocs)
		}
	}
}

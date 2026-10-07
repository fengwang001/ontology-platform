package mirror

import (
	"fmt"
	"testing"
)

// 复杂度可验证性：通过逐块操作计数器 BlockOps 直接断言——
// 一次写入的逐块开销等于目标成员数，与总块数无关。
func TestWriteCostIndependentOfBlocks(t *testing.T) {
	for _, blocks := range []int{1_000, 1_000_000} {
		v := mustNewVolume(t, 4, blocks, 8)
		before := v.BlockOps()
		mustWrite(t, v, blocks/2, 1, nil)
		if delta := v.BlockOps() - before; delta != 4 {
			t.Fatalf("块数=%d 时一次写入逐块开销应为 4（成员数），实际 %d", blocks, delta)
		}
	}
}

// 部分重同步的选块与复制开销只随脏区内块数增长，与总块数无关。
func TestPartialResyncCostIndependentOfBlocks(t *testing.T) {
	for _, blocks := range []int{1_000, 1_000_000} {
		v := mustNewVolume(t, 2, blocks, 100)
		mustFault(t, v, 1)
		for i := 0; i < 5; i++ {
			mustWrite(t, v, i*7%blocks, uint64(i+1), nil)
		}
		mustRejoin(t, v, 1, 1)
		before := v.BlockOps()
		copied := drain(t, v, 1, 1000)
		if len(copied) != 5 {
			t.Fatalf("块数=%d 时应只复制 5 个脏块，实际 %d", blocks, len(copied))
		}
		if delta := v.BlockOps() - before; delta != 5 {
			t.Fatalf("块数=%d 时部分重同步逐块开销应为 5（脏区块数），实际 %d", blocks, delta)
		}
	}
}

// 基准：写入开销不随总块数增长（go test -bench=Write -benchtime=10000x 验证）。
func BenchmarkWriteBlocksScaling(b *testing.B) {
	for _, blocks := range []int{1_000, 100_000, 1_000_000} {
		b.Run(fmt.Sprintf("blocks=%d", blocks), func(b *testing.B) {
			v, err := NewVolume(4, blocks, 8)
			if err != nil {
				b.Fatal(err)
			}
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if err := v.Write(i%blocks, uint64(i), nil); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// 基准：部分重同步（重新加入选块 + 批量推进）开销随脏区块数增长，
// 在巨大总块数下依然只与脏区大小相关。
func BenchmarkPartialResyncDirtyScaling(b *testing.B) {
	const blocks = 1_000_000
	for _, dirty := range []int{10, 100, 1_000} {
		b.Run(fmt.Sprintf("dirty=%d", dirty), func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				b.StopTimer()
				v, err := NewVolume(2, blocks, dirty+1)
				if err != nil {
					b.Fatal(err)
				}
				if err := v.ReportFault(1); err != nil {
					b.Fatal(err)
				}
				for j := 0; j < dirty; j++ {
					if err := v.Write(j*997%blocks, uint64(j), nil); err != nil {
						b.Fatal(err)
					}
				}
				b.StartTimer()
				if err := v.Rejoin(1, 1); err != nil {
					b.Fatal(err)
				}
				for v.Snapshot().Members[1].State == MemberResyncing {
					if _, err := v.AdvanceResync(1, 256); err != nil {
						b.Fatal(err)
					}
				}
			}
		})
	}
}

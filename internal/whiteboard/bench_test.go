package whiteboard

import (
	"fmt"
	"testing"
)

// BenchmarkScales 在两档相差两个数量级的规模下对照复杂度性质：
//
//	N=1_000 与 N=100_000
//
// Rank / 单元素 Reorder 期望 ~O(log N)；
// Lock / Unlock 期望 ~O(1)（不随规模、锁数、修订历史增长）。
func benchBoard(b *testing.B, n int) *Board {
	board := New()
	for i := 0; i < n; i++ {
		if err := board.Add("bench", fmt.Sprintf("e%07d", i), int64(i)); err != nil {
			b.Fatal(err)
		}
	}
	return board
}

func BenchmarkRank(b *testing.B) {
	for _, n := range []int{1_000, 100_000} {
		b.Run(fmt.Sprintf("N=%d", n), func(b *testing.B) {
			board := benchBoard(b, n)
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				id := fmt.Sprintf("e%07d", i%n)
				rank, err := board.Rank(id)
				if err != nil || rank < 1 || rank > n {
					b.Fatalf("rank=%d err=%v", rank, err)
				}
			}
		})
	}
}

func BenchmarkSingleReorder(b *testing.B) {
	for _, n := range []int{1_000, 100_000} {
		b.Run(fmt.Sprintf("N=%d", n), func(b *testing.B) {
			board := benchBoard(b, n)
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				// 相邻交换：把第 (i%n) 个元素移到其前一个元素上方。
				src := fmt.Sprintf("e%07d", i%n)
				anchor := fmt.Sprintf("e%07d", (i+1)%n)
				if err := board.Reorder("bench", src, anchor, Above, board.Rev(), int64(n+i)); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkLockUnlock(b *testing.B) {
	for _, n := range []int{1_000, 100_000} {
		b.Run(fmt.Sprintf("N=%d", n), func(b *testing.B) {
			board := benchBoard(b, n)
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				id := fmt.Sprintf("e%07d", i%n)
				now := int64(n + i)
				if err := board.Lock("bench", id, 10, now); err != nil {
					b.Fatal(err)
				}
				if err := board.Unlock("bench", id, now); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// BenchmarkLockUnlockManyLocks 在预先持有大量锁（约 N/2）的情况下测 Lock/Unlock，
// 证明其开销不随锁总数增长。
func BenchmarkLockUnlockManyLocks(b *testing.B) {
	for _, n := range []int{1_000, 100_000} {
		b.Run(fmt.Sprintf("N=%d", n), func(b *testing.B) {
			board := benchBoard(b, n)
			for i := 0; i < n/2; i++ {
				if err := board.Lock("other", fmt.Sprintf("e%07d", i), 3600, int64(n)); err != nil {
					b.Fatal(err)
				}
			}
			target := fmt.Sprintf("e%07d", n-1)
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				now := int64(n + i + 1)
				if err := board.Lock("bench", target, 10, now); err != nil {
					b.Fatal(err)
				}
				if err := board.Unlock("bench", target, now); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

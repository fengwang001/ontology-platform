package kanban

import (
	"bytes"
	"sync"
	"sync/atomic"
	"testing"
)

// TestConcurrentDrains：多 goroutine 并发拖拽同一批卡片，
// 结果须等价于某串行顺序（无竞态、计数自洽、无超收）。
func TestConcurrentDrains(t *testing.T) {
	var log bytes.Buffer
	svc, err := NewService(Config{
		Columns:    []string{"todo", "dev", "qa", "done"},
		Limits:     []int{0, 4, 4, 0},
		OwnerLimit: 8,
	}, &log)
	if err != nil {
		t.Fatal(err)
	}
	const n = 40
	for i := 0; i < n; i++ {
		if _, err := svc.AddCard(idOf(i), "u", 0); err != nil {
			t.Fatal(err)
		}
	}

	// 每个 worker 抢一张卡，按版本自旋推动它向右直到完成。
	var wg sync.WaitGroup
	var clock int64
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			id := idOf(i)
			for col := 1; col <= 3; col++ {
				for attempt := 0; attempt < 200; attempt++ {
					c, err := svc.GetCard(id)
					if err != nil {
						return
					}
					out, merr := svc.Move("w", id, col, c.Version, false, atomic.AddInt64(&clock, 1))
					if merr == nil {
						_ = out
						break
					}
					// 任何拒绝都重读最新版本后重试；列/负责人上限会因其他卡片前移而释放。
				}
				latest, err := svc.GetCard(id)
				if err != nil || latest.Column != col {
					t.Errorf("card %s stuck before col %d: %v", id, col, err)
					return
				}
			}
		}(i)
	}
	wg.Wait()

	b := svc.Board()
	if err := b.CheckInvariants(); err != nil {
		t.Fatal(err)
	}
	doneCol := b.NumColumns() - 1
	if b.ColumnCount(doneCol) != n {
		t.Fatalf("done=%d want %d", b.ColumnCount(doneCol), n)
	}
	if b.ExpeditedCard() != "" {
		t.Fatal("unexpected expedited card")
	}
	if len(svc.Logs()) == 0 {
		t.Fatal("no operation logs")
	}
}

func idOf(i int) string {
	return "card-" + string(rune('a'+i%26)) + string(rune('a'+(i/26)%26)) + itoa(i)
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b [12]byte
	p := len(b)
	for i > 0 {
		p--
		b[p] = byte('0' + i%10)
		i /= 10
	}
	return string(b[p:])
}

// BenchmarkMoveIndependence 验证 Move 开销不随卡片总数增长：
// 在 10 张与 100000 张卡两种规模下，移动同一张“重前置”卡，时间应同量级。
func BenchmarkMoveIndependence(b *testing.B) {
	for _, total := range []int{100, 100000} {
		b.Run(itype(total), func(b *testing.B) {
			board := buildBenchBoard(b, total)
			start, _ := board.GetCard("target")
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				cur, _ := board.GetCard("target")
				if _, err := board.Move("bench", "target", 1, cur.Version, false, int64(100000+i*2+1)); err != nil {
					b.Fatal(err)
				}
				cur, _ = board.GetCard("target")
				if _, err := board.Move("bench", "target", 0, cur.Version, false, int64(100000+i*2+2)); err != nil {
					b.Fatal(err)
				}
			}
			_ = start
		})
	}
}

func itype(n int) string {
	switch n {
	case 100:
		return "100cards"
	default:
		return "100000cards"
	}
}

func buildBenchBoard(b *testing.B, total int) *Board {
	b.Helper()
	board, err := NewBoard(Config{
		Columns:    []string{"todo", "wip", "done"},
		Limits:     []int{0, 0, 0},
		OwnerLimit: 50,
	})
	if err != nil {
		b.Fatal(err)
	}
	// 先建 target
	if _, err := board.AddCard("target", "owner", 0); err != nil {
		b.Fatal(err)
	}
	// 建 20 个前置并完成
	setupNow := int64(1)
	for i := 0; i < 20; i++ {
		pid := "pre-" + itoa(i)
		if _, err := board.AddCard(pid, "owner", setupNow); err != nil {
			b.Fatal(err)
		}
		setupNow++
		tc, _ := board.GetCard("target")
		if err := board.AddDep("bench", "target", pid, tc.Version, setupNow); err != nil {
			b.Fatal(err)
		}
		setupNow++
		pc, _ := board.GetCard(pid)
		if _, err := board.Move("bench", pid, 1, pc.Version, false, setupNow); err != nil {
			b.Fatal(err)
		}
		setupNow++
		pc, _ = board.GetCard(pid)
		if _, err := board.Move("bench", pid, 2, pc.Version, false, setupNow); err != nil {
			b.Fatal(err)
		}
		setupNow++
	}
	// 其余卡片（大量噪音），全部留在待办
	for i := 0; i < total-21; i++ {
		if _, err := board.AddCard("noise-"+itoa(i), "other", 100); err != nil {
			b.Fatal(err)
		}
	}
	return board
}

package room

import "testing"

// BenchmarkOps 证明热点操作的单次开销为常数：
// 无论房间此前经历多少轮加入/离开/改报（“历史事件”），一次操作只做
// 常数次哈希与指针操作。用 -bench 对比不同历史规模 b.N 段即可验证。
func BenchmarkJoinSetReadyLeave(b *testing.B) {
	for n := 0; n < b.N; n++ {
		// 每轮使用 20 人满员房间，制造加入/就绪/离开的重复历史。
		// C 取很大保证循环内不触发惰性开局（开局由其他用例覆盖）。
		r, _ := New(Config{L: 2, U: 20, C: 600, R: 3600})
		for i := 0; i < 20; i++ {
			u := string(rune('a' + i))
			if err := r.Join(u, 0); err != nil {
				b.Fatal(err)
			}
			if err := r.SetReady(u, true, 0); err != nil {
				b.Fatal(err)
			}
		}
		if err := r.Leave("a", 0); err != nil {
			b.Fatal(err)
		}
		if err := r.Join("a", 0); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkReportChange 反复改报：账本只保留当前票，单次开销与既往改报次数无关。
func BenchmarkReportChange(b *testing.B) {
	r, _ := New(Config{L: 2, U: 20, C: 1, R: 3600})
	for i := 0; i < 20; i++ {
		u := string(rune('a' + i))
		_ = r.Join(u, 0)
		_ = r.SetReady(u, true, 0)
	}
	if _, err := r.Snapshot(1); err != nil {
		b.Fatal(err)
	}
	if err := r.End("a", 2); err != nil {
		b.Fatal(err)
	}
	winners := []string{"a", "b", "c"}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// 固定 now=3（End 于 2，期限 3602），反复改 b 自己的票不触发终态。
		if err := r.Report("b", winners[i%3], 3); err != nil {
			b.Fatal(err)
		}
	}
}

// setupReadyRoom 构造一个当前有 20 人在室（全部就绪后再取消 a 的就绪）的
// 等待态房间。若 churn>0，先让临时用户重复 join/leave churn 次，
// 制造大量历史事件，但当前在室集合始终不变。
func setupReadyRoom(b *testing.B, churn int) *Room {
	b.Helper()
	r, err := New(Config{L: 2, U: 20, C: 600, R: 3600})
	if err != nil {
		b.Fatal(err)
	}
	var now int64
	now++
	if err := r.Join("a", now); err != nil { // 锚定玩家，避免 churn 时空房作废
		b.Fatal(err)
	}
	for k := 0; k < churn; k++ {
		now++
		tmp := "tmp"
		if err := r.Join(tmp, now); err != nil {
			b.Fatal(err)
		}
		now++
		if err := r.Leave(tmp, now); err != nil {
			b.Fatal(err)
		}
	}
	for i := 1; i < 20; i++ {
		u := string(rune('a' + i))
		now++
		if err := r.Join(u, now); err != nil {
			b.Fatal(err)
		}
		now++
		if err := r.SetReady(u, true, now); err != nil {
			b.Fatal(err)
		}
	}
	now++
	if err := r.SetReady("a", true, now); err != nil { // 锚定玩家先就绪
		b.Fatal(err)
	}
	now++
	if err := r.SetReady("a", false, now); err != nil {
		b.Fatal(err)
	}
	return r
}

func BenchmarkSetReadyNoHistory(b *testing.B) {
	r := setupReadyRoom(b, 0)
	var now int64 = 1000
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		now++
		if err := r.SetReady("a", true, now); err != nil {
			b.Fatal(err)
		}
		now++
		if err := r.SetReady("a", false, now); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkSetReadyLargeHistory(b *testing.B) {
	r := setupReadyRoom(b, 20000)
	var now int64 = 100000
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		now++
		if err := r.SetReady("a", true, now); err != nil {
			b.Fatal(err)
		}
		now++
		if err := r.SetReady("a", false, now); err != nil {
			b.Fatal(err)
		}
	}
}

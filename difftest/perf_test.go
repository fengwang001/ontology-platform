package difftest

import (
	"testing"
	"time"

	"ontology/loto"
)

// buildHistory 创建含 k 张已完成历史票的系统（占用态集合保持为空）。
// 返回系统与当前时钟。
func buildHistory(tb testing.TB, k int) (*loto.System, int64) {
	tb.Helper()
	cfg := &loto.Config{DevicePoints: map[string][]string{
		"D1": {"P1", "P2"},
		"D2": {"P2", "P3"},
	}}
	s, err := loto.NewSystem(cfg)
	if err != nil {
		tb.Fatalf("NewSystem: %v", err)
	}
	must := func(err error) {
		if err != nil {
			tb.Fatalf("buildHistory: %v", err)
		}
	}
	must(s.AddPerson("alice", loto.RoleApplicant))
	must(s.AddPerson("bob", loto.RoleApprover))
	must(s.AddPerson("carol", loto.RoleApprover))
	var clock int64
	for i := 0; i < k; i++ {
		start := clock + 1
		id, err := s.Apply(start, "alice", []string{"D1"}, loto.WorkNormal, start, start+10)
		must(err)
		must(s.Approve(start, "bob", id))
		must(s.Verify(start, id, "carol"))
		must(s.Start(start, id, "alice"))
		must(s.Complete(start+1, id, "alice"))
		clock = start + 1
	}
	return s, clock
}

// measureOps 测量在历史票规模为 k 时：
//   - 批准冲突判定的平均耗时（占用态集合恒定，仅历史票总数不同）
//   - 送电判定的平均耗时
func measureOps(tb testing.TB, k, iters int) (approveAvg, energizeAvg time.Duration) {
	tb.Helper()
	s, clock := buildHistory(tb, k)
	must := func(err error) {
		if err != nil {
			tb.Fatalf("measureOps: %v", err)
		}
	}
	var approveTotal, energizeTotal time.Duration
	for i := 0; i < iters; i++ {
		start := clock + 1
		id, err := s.Apply(start, "alice", []string{"D1"}, loto.WorkNormal, start, start+10)
		must(err)
		t0 := time.Now()
		must(s.Approve(start, "bob", id)) // 含冲突判定
		approveTotal += time.Since(t0)
		t0 = time.Now()
		if _, err := s.Energizable("D1"); err != nil {
			tb.Fatalf("Energizable: %v", err)
		}
		energizeTotal += time.Since(t0)
		// 走完生命周期，保持占用态集合为空
		must(s.Verify(start, id, "carol"))
		must(s.Start(start, id, "alice"))
		must(s.Complete(start+1, id, "alice"))
		clock = start + 1
	}
	return approveTotal / time.Duration(iters), energizeTotal / time.Duration(iters)
}

// TestPerfScales 两档历史票规模对照：
// 证明批准冲突判定与送电判定的开销不随历史票总数增长。
func TestPerfScales(t *testing.T) {
	const small, large = 10_000, 100_000
	const iters = 2_000

	aSmall, eSmall := measureOps(t, small, iters)
	aLarge, eLarge := measureOps(t, large, iters)

	t.Logf("历史票总数 %7d: 批准冲突判定平均 %v, 送电判定平均 %v", small, aSmall, eSmall)
	t.Logf("历史票总数 %7d: 批准冲突判定平均 %v, 送电判定平均 %v", large, aLarge, eLarge)

	check := func(name string, small, large time.Duration) {
		t.Helper()
		if small <= 0 {
			small = 1
		}
		ratio := float64(large) / float64(small)
		t.Logf("%s 耗时比(10万/1万) = %.2f", name, ratio)
		// 历史票总数放大 10 倍，耗时不应显著增长（允许测量噪声）
		if ratio > 4 && large-small > 5*time.Microsecond {
			t.Fatalf("%s 开销随历史票总数增长: 1万=%v 10万=%v 比=%.2f", name, small, large, ratio)
		}
	}
	check("批准冲突判定", aSmall, aLarge)
	check("送电判定", eSmall, eLarge)
}

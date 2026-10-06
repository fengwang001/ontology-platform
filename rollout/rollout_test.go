package rollout

import (
	"errors"
	"fmt"
	"sync"
	"testing"
)

func baseCfg() Config {
	return Config{
		Ratios:      []int{1000, 3000, 10000},
		DwellMs:     100,
		MinCanary:   10,
		ToleranceBP: 500,
		MaxFailures: 3,
		StickyMs:    200,
		MaxSticky:   1000,
	}
}

func mustNew(t *testing.T, cfg Config) *Splitter {
	t.Helper()
	s, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return s
}

func TestConfigValidation(t *testing.T) {
	bad := []Config{
		{Ratios: nil, DwellMs: 1, MinCanary: 1, MaxFailures: 1, ToleranceBP: 0},
		{Ratios: []int{0}, DwellMs: 1, MinCanary: 1, MaxFailures: 1},
		{Ratios: []int{10001}, DwellMs: 1, MinCanary: 1, MaxFailures: 1},
		{Ratios: []int{100, 100}, DwellMs: 1, MinCanary: 1, MaxFailures: 1},
		{Ratios: []int{300, 100}, DwellMs: 1, MinCanary: 1, MaxFailures: 1},
		{Ratios: []int{100}, DwellMs: -1, MinCanary: 1, MaxFailures: 1},
		{Ratios: []int{100}, DwellMs: 1, MinCanary: 0, MaxFailures: 1},
		{Ratios: []int{100}, DwellMs: 1, MinCanary: 1, MaxFailures: 0},
		{Ratios: []int{100}, DwellMs: 1, MinCanary: 1, MaxFailures: 1, ToleranceBP: 10001},
		{Ratios: []int{100}, DwellMs: 1, MinCanary: 1, MaxFailures: 1, StickyMs: -1},
		{Ratios: []int{100}, DwellMs: 1, MinCanary: 1, MaxFailures: 1, MaxSticky: -1},
	}
	for i, c := range bad {
		if _, err := New(c); !errors.Is(err, ErrInvalidArgument) {
			t.Fatalf("case %d: want ErrInvalidArgument, got %v", i, err)
		}
	}
	ok := []Config{
		{Ratios: []int{1}, DwellMs: 0, MinCanary: 1, MaxFailures: 1},
		{Ratios: []int{1, 10000}, DwellMs: 1, MinCanary: 1, MaxFailures: 1, MaxSticky: 0},
	}
	for i, c := range ok {
		if _, err := New(c); err != nil {
			t.Fatalf("case %d: unexpected %v", i, err)
		}
	}
}

func TestInitialState(t *testing.T) {
	s := mustNew(t, baseCfg())
	snap := s.Snapshot()
	if snap.Phase != PhaseNotStarted || snap.Ratio != 0 {
		t.Fatalf("initial = %+v", snap)
	}
	if err := s.Start(10); err != nil {
		t.Fatal(err)
	}
	if err := s.Start(11); !errors.Is(err, ErrIllegalState) {
		t.Fatalf("second start: %v", err)
	}
	// 被拒绝的调用不得推进时钟；同一时间戳仍合法，更早时间戳仍被拒。
	if err := s.Start(9); !errors.Is(err, ErrClockRewound) {
		t.Fatalf("clock after reject: %v", err)
	}
}

func TestErrorPriority(t *testing.T) {
	s := mustNew(t, baseCfg())
	if err := s.Start(10); err != nil {
		t.Fatal(err)
	}
	// 时钟回退 + 状态不允许（idx=0 时 Downgrade 本不允许）：时钟优先。
	if err := s.Downgrade(5); !errors.Is(err, ErrClockRewound) {
		t.Fatalf("got %v", err)
	}
	// 参数非法 + 时钟回退：参数优先。
	if err := s.Observe(Version(9), true, 1); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("observe got %v", err)
	}
	// 版本非法、时间合法：仍是参数非法。
	if err := s.Observe(Version(9), true, 20); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("observe invalid version: %v", err)
	}
	// 状态不允许：时钟正常。
	if err := s.Downgrade(20); !errors.Is(err, ErrIllegalState) {
		t.Fatalf("downgrade idx0: %v", err)
	}
}

func TestPositionStableAndUniform(t *testing.T) {
	s := mustNew(t, baseCfg())
	for _, id := range []string{"a", "user-42", "中文标识", ""} {
		p1, p2 := s.Position(id), s.Position(id)
		if p1 != p2 || p1 < 0 || p1 >= 10000 {
			t.Fatalf("position unstable/out of range: %q -> %d %d", id, p1, p2)
		}
	}
	const n = 100000
	buckets := [10]int{}
	for i := 0; i < n; i++ {
		buckets[s.Position(fmt.Sprintf("uniform-%d", i))/1000]++
	}
	for i, c := range buckets {
		if c < n/10*97/100 || c > n/10*103/100 {
			t.Fatalf("bucket %d = %d, not roughly uniform", i, c)
		}
	}
}

func TestDwellBoundary(t *testing.T) {
	cfg := baseCfg()
	cfg.Ratios = []int{10000}
	s := mustNew(t, cfg)
	if err := s.Start(1000); err != nil {
		t.Fatal(err)
	}
	// D=100：差一未满；恰好满 D 即视为满（窗口空 -> 样本不足）。
	if r, _ := s.Evaluate(1099); r != EvalDwellNotMet {
		t.Fatalf("t=1099: %v", r)
	}
	if r, _ := s.Evaluate(1100); r != EvalInsufficient {
		t.Fatalf("t=1100: %v", r)
	}
}

func feedCanary(t *testing.T, s *Splitter, n int, now int64) {
	t.Helper()
	for i := 0; i < n; i++ {
		if err := s.Observe(VersionCanary, true, now+int64(i)); err != nil {
			t.Fatal(err)
		}
	}
}

func TestSampleBoundary(t *testing.T) {
	cfg := baseCfg()
	cfg.Ratios = []int{10000}
	s := mustNew(t, cfg)
	if err := s.Start(0); err != nil {
		t.Fatal(err)
	}
	feedCanary(t, s, 9, 1) // 恰为 G-1
	if r, _ := s.Evaluate(cfg.DwellMs + 100); r != EvalInsufficient {
		t.Fatalf("G-1: %v", r)
	}
	if err := s.Observe(VersionCanary, true, cfg.DwellMs+101); err != nil {
		t.Fatal(err)
	}
	if r, _ := s.Evaluate(cfg.DwellMs + 200); r != EvalPassed {
		t.Fatalf("exact G: %v", r)
	}
	if s.Snapshot().Phase != PhaseCompleted {
		t.Fatalf("phase = %v", s.Snapshot().Phase)
	}
}

func TestErrorRateExactBoundary(t *testing.T) {
	// 灰度 20 请求 1 失败（500bp），稳定 0 请求（视为 0），T=500：
	// 灰度错误率恰等于 稳定 + T，按 <= 判通过。
	cfg := Config{
		Ratios: []int{5000, 10000}, DwellMs: 10, MinCanary: 20,
		ToleranceBP: 500, MaxFailures: 1, StickyMs: 1, MaxSticky: 10,
	}
	s := mustNew(t, cfg)
	_ = s.Start(0)
	for i := 0; i < 19; i++ {
		_ = s.Observe(VersionCanary, true, 1)
	}
	_ = s.Observe(VersionCanary, false, 2)
	if r, _ := s.Evaluate(20); r != EvalPassed {
		t.Fatalf("exact tolerance should pass: %v", r)
	}

	// 同样 500bp 但 T=499：超出一万分之一也必须失败。
	cfg.ToleranceBP = 499
	s2 := mustNew(t, cfg)
	_ = s2.Start(0)
	for i := 0; i < 19; i++ {
		_ = s2.Observe(VersionCanary, true, 1)
	}
	_ = s2.Observe(VersionCanary, false, 2)
	if r, _ := s2.Evaluate(20); r != EvalFailed {
		t.Fatalf("one bp over tolerance should fail: %v", r)
	}

	// 稳定有请求的精确边界：1000bp <= 500bp + 500bp，恰好相等，通过。
	cfg3 := Config{
		Ratios: []int{10000}, DwellMs: 10, MinCanary: 100,
		ToleranceBP: 500, MaxFailures: 1, StickyMs: 1, MaxSticky: 10,
	}
	s3 := mustNew(t, cfg3)
	_ = s3.Start(0)
	for i := 0; i < 10; i++ {
		_ = s3.Observe(VersionCanary, false, 1)
	}
	for i := 0; i < 90; i++ {
		_ = s3.Observe(VersionCanary, true, 1)
	}
	for i := 0; i < 5; i++ {
		_ = s3.Observe(VersionStable, false, 1)
	}
	for i := 0; i < 95; i++ {
		_ = s3.Observe(VersionStable, true, 1)
	}
	if r, _ := s3.Evaluate(20); r != EvalPassed {
		t.Fatalf("stable + tolerance exact: %v", r)
	}
}

func TestFailureThenPassResetsStreak(t *testing.T) {
	cfg := Config{
		Ratios: []int{5000, 10000}, DwellMs: 10, MinCanary: 2,
		ToleranceBP: 0, MaxFailures: 2, StickyMs: 1, MaxSticky: 10,
	}
	s := mustNew(t, cfg)
	_ = s.Start(0)
	_ = s.Observe(VersionCanary, false, 1)
	_ = s.Observe(VersionCanary, false, 2)
	if r, _ := s.Evaluate(11); r != EvalFailed || s.Snapshot().FailStreak != 1 {
		t.Fatalf("first fail: %v streak=%d", r, s.Snapshot().FailStreak)
	}
	if snap := s.Snapshot(); snap.EnteredAtMs != 11 {
		t.Fatalf("dwell restart = %d", snap.EnteredAtMs)
	}
	if r, _ := s.Evaluate(20); r != EvalDwellNotMet {
		t.Fatalf("dwell after restart: %v", r)
	}
	_ = s.Observe(VersionCanary, true, 21)
	_ = s.Observe(VersionCanary, true, 22)
	if r, _ := s.Evaluate(22); r != EvalPassed {
		t.Fatalf("pass: %v", r)
	}
	snap := s.Snapshot()
	if snap.PhaseIndex != 1 || snap.FailStreak != 0 {
		t.Fatalf("after pass: %+v", snap)
	}
	// 再失败一次：累计从 0 开始，不回滚。
	_ = s.Observe(VersionCanary, false, 23)
	_ = s.Observe(VersionCanary, false, 24)
	if r, _ := s.Evaluate(33); r != EvalFailed {
		t.Fatalf("post-pass fail: %v", r)
	}
	if s.Snapshot().Phase != PhaseRunning {
		t.Fatal("should not rollback after streak reset")
	}
}

func TestRollbackAndStickyClear(t *testing.T) {
	cfg := Config{
		Ratios: []int{5000, 10000}, DwellMs: 0, MinCanary: 1,
		ToleranceBP: 0, MaxFailures: 2, StickyMs: 10000, MaxSticky: 100,
	}
	s := mustNew(t, cfg)
	_ = s.Start(0)
	// 找一个灰度位置的标识并建立粘性。
	canaryID := ""
	for i := 0; ; i++ {
		id := fmt.Sprintf("c-%d", i)
		if s.Position(id) < 5000 {
			canaryID = id
			break
		}
	}
	d, err := s.Route(canaryID, 1)
	if err != nil || d.Version != VersionCanary || d.Source != SourceProportion {
		t.Fatalf("first route: %+v %v", d, err)
	}
	if d2, _ := s.Route(canaryID, 2); d2.Source != SourceSticky {
		t.Fatalf("sticky expected: %+v", d2)
	}
	if s.Snapshot().StickyCount != 1 {
		t.Fatal("sticky not recorded")
	}
	// 连续 F 次失败 -> 回滚。
	for i := 0; i < cfg.MaxFailures; i++ {
		_ = s.Observe(VersionCanary, false, int64(10+i))
		if r, _ := s.Evaluate(int64(10 + i)); r != EvalFailed {
			t.Fatalf("fail eval %d: %v", i, r)
		}
	}
	snap := s.Snapshot()
	if snap.Phase != PhaseRolledBack || snap.Ratio != 0 || snap.StickyCount != 0 {
		t.Fatalf("after rollback: %+v", snap)
	}
	// 回滚后路由：强制稳定，且不重建粘性。
	d3, _ := s.Route(canaryID, 50)
	if d3.Version != VersionStable || d3.Source != SourceForceStable {
		t.Fatalf("force stable: %+v", d3)
	}
	if s.Snapshot().StickyCount != 0 {
		t.Fatal("rolled-back route must not create sticky")
	}
	if err := s.Start(60); !errors.Is(err, ErrIllegalState) {
		t.Fatalf("start after rollback: %v", err)
	}
	if err := s.Downgrade(60); !errors.Is(err, ErrIllegalState) {
		t.Fatalf("downgrade after rollback: %v", err)
	}
	if r, _ := s.Evaluate(60); r != EvalDwellNotMet || s.Snapshot().Phase != PhaseRolledBack {
		t.Fatalf("eval after rollback: %v", r)
	}
	if err := s.Observe(VersionCanary, false, 61); err != nil {
		t.Fatalf("observe rolled back: %v", err)
	}
	s.Reset()
	if s.Snapshot().Phase != PhaseNotStarted {
		t.Fatal("reset must return to not_started")
	}
	// 重置清空时钟基线，旧时间戳可重新使用。
	if err := s.Start(0); err != nil {
		t.Fatalf("start after reset: %v", err)
	}
}

func TestStickyExpiryBoundary(t *testing.T) {
	cfg := Config{
		Ratios: []int{10000}, DwellMs: 1, MinCanary: 1,
		ToleranceBP: 0, MaxFailures: 1, StickyMs: 100, MaxSticky: 10,
	}
	s := mustNew(t, cfg)
	_ = s.Start(0)
	id := "expiry-id"
	d, _ := s.Route(id, 1000)
	if d.Source != SourceProportion || d.Version != VersionCanary {
		t.Fatalf("first: %+v", d)
	}
	// L=100，左闭右开 [1000,1100)：1099 仍粘。
	if d2, _ := s.Route(id, 1099); d2.Source != SourceSticky {
		t.Fatalf("at 1099: %+v", d2)
	}
	// 上一步把时刻刷新为 1099：1198 仍粘（99），再刷新为 1198；
	// 1199 仍粘（1），1299 恰过期（100，左闭右开）。
	if d3, _ := s.Route(id, 1198); d3.Source != SourceSticky {
		t.Fatalf("refresh extends lifetime, at 1198: %+v", d3)
	}
	if d4, _ := s.Route(id, 1199); d4.Source != SourceSticky {
		t.Fatalf("at 1199: %+v", d4)
	}
	if d5, _ := s.Route(id, 1299); d5.Source != SourceProportion {
		t.Fatalf("at 1299 should expire: %+v", d5)
	}
}

func TestStickyEviction(t *testing.T) {
	cfg := Config{
		Ratios: []int{10000}, DwellMs: 1, MinCanary: 1,
		ToleranceBP: 0, MaxFailures: 1, StickyMs: 100000, MaxSticky: 2,
	}
	s := mustNew(t, cfg)
	_ = s.Start(0)
	for i, id := range []string{"a", "b"} {
		if d, err := s.Route(id, int64(i+1)); err != nil || d.Version != VersionCanary {
			t.Fatalf("route %s: %+v %v", id, d, err)
		}
	}
	// 缓存 [b, a]；刷新 a -> [a, b]。
	if d, _ := s.Route("a", 3); d.Source != SourceSticky {
		t.Fatalf("a should be sticky: %+v", d)
	}
	// 插入 c，淘汰最久未路由的 b；a 保留。
	if _, err := s.Route("c", 4); err != nil {
		t.Fatal(err)
	}
	if snap := s.Snapshot(); snap.StickyCount != 2 {
		t.Fatalf("count = %d", snap.StickyCount)
	}
	// LRU 序为 [c, a]：b 已被淘汰，再次命中必为按比例归属。
	if d2, _ := s.Route("b", 5); d2.Source != SourceProportion {
		t.Fatalf("evicted b must miss sticky: %+v", d2)
	}
	// b 重插后序为 [b, c]：尾端 a 已被淘汰（证明淘汰的是
	// “最久未被路由”的 a，而非插入更早、但在 t=4 刚被路由的 c）。
	if d3, _ := s.Route("a", 6); d3.Source != SourceProportion {
		t.Fatalf("a must be evicted: %+v", d3)
	}
	// a 的重新插入反过来淘汰尾端 c；b 始终存活。
	if d4, _ := s.Route("b", 7); d4.Source != SourceSticky {
		t.Fatalf("b must remain sticky: %+v", d4)
	}
	// P=0：永不保留粘性。
	cfg.MaxSticky = 0
	s2 := mustNew(t, cfg)
	_ = s2.Start(0)
	_, _ = s2.Route("x", 1)
	if d5, _ := s2.Route("x", 2); d5.Source != SourceProportion || s2.Snapshot().StickyCount != 0 {
		t.Fatalf("P=0 must disable sticky: %+v", d5)
	}
}

func TestDowngradeKeepsSticky(t *testing.T) {
	cfg := Config{
		Ratios: []int{5000, 10000}, DwellMs: 10, MinCanary: 1,
		ToleranceBP: 0, MaxFailures: 1, StickyMs: 100000, MaxSticky: 100,
	}
	s := mustNew(t, cfg)
	_ = s.Start(0)
	// 晋级到阶段 1（100% 灰度）。
	_ = s.Observe(VersionCanary, true, 1)
	if r, _ := s.Evaluate(10); r != EvalPassed {
		t.Fatalf("promote: %v", r)
	}
	// 阶段 1 下把一个位置 >= 5000 的稳定标识路由出去（阶段 0 时它本应是稳定）。
	id := ""
	for i := 0; ; i++ {
		cand := fmt.Sprintf("d-%d", i)
		if s.Position(cand) >= 5000 {
			id = cand
			break
		}
	}
	if d, _ := s.Route(id, 11); d.Version != VersionCanary {
		t.Fatalf("at 100%% should be canary: %+v", d)
	}
	before := s.Snapshot().StickyCount
	// 降级回阶段 0：比例 50%，但粘性保持，该标识仍走灰度。
	if err := s.Downgrade(20); err != nil {
		t.Fatal(err)
	}
	snap := s.Snapshot()
	if snap.PhaseIndex != 0 || snap.Ratio != 5000 || snap.StickyCount != before {
		t.Fatalf("after downgrade: %+v", snap)
	}
	if d2, _ := s.Route(id, 21); d2.Version != VersionCanary || d2.Source != SourceSticky {
		t.Fatalf("sticky must survive downgrade: %+v", d2)
	}
	// 降级清空窗口与失败累计：重新需要驻留。
	if r, _ := s.Evaluate(25); r != EvalDwellNotMet {
		t.Fatalf("dwell restarts after downgrade: %v", r)
	}
}

func TestProportionMonotonicNoRevert(t *testing.T) {
	// 比例只增（晋级/完成，不降级）期间，同一标识一旦归属灰度就不回退到稳定。
	cfg := Config{
		Ratios: []int{1000, 3000, 7000, 10000}, DwellMs: 0, MinCanary: 1,
		ToleranceBP: 10000, MaxFailures: 1, StickyMs: 0, MaxSticky: 0,
	}
	s := mustNew(t, cfg)
	_ = s.Start(0)
	ids := make([]string, 200)
	for i := range ids {
		ids[i] = fmt.Sprintf("mono-%d", i)
	}
	wasCanary := make(map[string]bool)
	for stage, ratio := range cfg.Ratios {
		for _, id := range ids {
			d, err := s.Route(id, int64(stage+1))
			if err != nil {
				t.Fatal(err)
			}
			want := VersionStable
			if s.Position(id) < ratio {
				want = VersionCanary
			}
			if d.Version != want || d.Source != SourceProportion {
				t.Fatalf("stage %d id %s: got %+v want %v", stage, id, d, want)
			}
			if want == VersionCanary {
				wasCanary[id] = true
			}
			if wasCanary[id] && want == VersionStable {
				t.Fatalf("id %s reverted from canary to stable at ratio %d", id, ratio)
			}
		}
		_ = s.Observe(VersionCanary, true, int64(stage+1))
		if r, _ := s.Evaluate(int64(stage + 1)); r != EvalPassed {
			t.Fatalf("promote at stage %d: %v", stage, r)
		}
	}
	if s.Snapshot().Phase != PhaseCompleted {
		t.Fatalf("final phase: %v", s.Snapshot().Phase)
	}
}

func TestObserveIgnoredOutsideRunning(t *testing.T) {
	cfg := baseCfg()
	s := mustNew(t, cfg)
	// 未开始：忽略。
	if err := s.Observe(VersionCanary, false, 5); err != nil {
		t.Fatal(err)
	}
	_ = s.Start(10)
	// 推进到完成。
	cfg2 := Config{Ratios: []int{10000}, DwellMs: 0, MinCanary: 1, ToleranceBP: 0, MaxFailures: 1}
	s2 := mustNew(t, cfg2)
	_ = s2.Start(0)
	_ = s2.Observe(VersionCanary, true, 1)
	if r, _ := s2.Evaluate(1); r != EvalPassed {
		t.Fatal(r)
	}
	if err := s2.Observe(VersionCanary, false, 2); err != nil {
		t.Fatalf("observe completed: %v", err)
	}
	if r, _ := s2.Evaluate(2); r != EvalDwellNotMet {
		t.Fatalf("completed must not evaluate again: %v", r)
	}
}

func TestConcurrentRoutesAreSafe(t *testing.T) {
	cfg := Config{
		Ratios: []int{5000}, DwellMs: 0, MinCanary: 1,
		ToleranceBP: 0, MaxFailures: 1, StickyMs: 100000, MaxSticky: 10000,
	}
	s := mustNew(t, cfg)
	_ = s.Start(0)
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 2000; i++ {
				id := fmt.Sprintf("g%d-id%d", g, i%500)
				// 并发正确性：调用方需在临界区外保证自身时间戳非递减；
				// 这里所有调用使用同一时刻（合法：允许相等），
				// 重点验证线性化与数据竞争。
				d, err := s.Route(id, 1)
				if err != nil {
					t.Errorf("route: %v", err)
					return
				}
				want := VersionStable
				if s.Position(id) < 5000 {
					want = VersionCanary
				}
				if d.Version != want {
					t.Errorf("wrong version: %v", d.Version)
					return
				}
			}
		}(g)
	}
	wg.Wait()
}

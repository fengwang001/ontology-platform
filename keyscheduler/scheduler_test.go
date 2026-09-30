package keyscheduler

import (
	"errors"
	"fmt"
	"sync"
	"testing"
)

const (
	cCache = int64(10)
	cTTL   = int64(30)
	cSkew  = int64(2)
)

type testLogger struct{ t *testing.T }

func (l testLogger) Printf(format string, args ...any) {
	l.t.Logf(format, args...)
}

func newTestScheduler(t *testing.T, now int64) *Scheduler {
	t.Helper()
	s, err := New(cCache, cTTL, cSkew, "k0", now, testLogger{t})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return s
}

func mustPhase(t *testing.T, s *Scheduler, id string, want Phase) KeyInfo {
	t.Helper()
	info, err := s.Key(id)
	if err != nil {
		t.Fatalf("Key(%q): %v", id, err)
	}
	if info.Phase != want {
		t.Fatalf("Key(%q) phase=%s, want %s", id, info.Phase, want)
	}
	return info
}

func mustSet(t *testing.T, s *Scheduler, now int64, want ...string) {
	t.Helper()
	got, err := s.VerificationSet(now)
	if err != nil {
		t.Fatalf("VerificationSet(%d): %v", now, err)
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("VerificationSet(%d)=%v, want %v", now, got, want)
	}
}

func TestNewRejectsBadParams(t *testing.T) {
	cases := []struct {
		name      string
		c, tt, sk int64
	}{
		{"C zero", 0, cTTL, cSkew},
		{"C negative", -1, cTTL, cSkew},
		{"T zero", cCache, 0, cSkew},
		{"T negative", cCache, -5, cSkew},
		{"S negative", cCache, cTTL, -1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := New(tc.c, tc.tt, tc.sk, "k0", 0, testLogger{t}); !errors.Is(err, ErrInvalidParam) {
				t.Fatalf("err=%v, want ErrInvalidParam", err)
			}
		})
	}
}

func TestRotateActivatesExactlyCAfterRequest(t *testing.T) {
	s := newTestScheduler(t, 100)

	at, err := s.Rotate("k1", 500)
	if err != nil {
		t.Fatalf("Rotate: %v", err)
	}
	if at != 510 {
		t.Fatalf("activateAt=%d, want 510 (t+C)", at)
	}
	mustPhase(t, s, "k1", Published)
	mustPhase(t, s, "k0", Active)
	mustSet(t, s, 505, "k0", "k1")

	signAt505, err := s.Sign(505)
	if err != nil || signAt505 != "k0" {
		t.Fatalf("Sign(505)=%q,%v, want k0", signAt505, err)
	}
	// 差一刻（509）尚未启用。
	if active, err := s.Advance(509); err != nil || active != "k0" {
		t.Fatalf("Advance(509)=%q,%v, want k0", active, err)
	}
	mustPhase(t, s, "k1", Published)
	mustPhase(t, s, "k0", Active)

	// 恰到点 510：k1 成为唯一活跃，k0 同时停签。
	if active, err := s.Advance(510); err != nil || active != "k1" {
		t.Fatalf("Advance(510)=%q,%v, want k1", active, err)
	}
	k0 := mustPhase(t, s, "k0", SignOff)
	k1 := mustPhase(t, s, "k1", Active)
	// 停签时刻记为启用时刻 510，而非被观察到的时刻；最早退役 = 510+30+2。
	if k0.SignOffAt != 510 || k0.RetiresAt != 542 {
		t.Fatalf("k0 signOff=%d retires=%d, want 510/542", k0.SignOffAt, k0.RetiresAt)
	}
	if k1.Activated != 510 {
		t.Fatalf("k1 activated=%d, want 510", k1.Activated)
	}
	signAt510, err := s.Sign(510)
	if err != nil || signAt510 != "k1" {
		t.Fatalf("Sign(510)=%q,%v, want k1", signAt510, err)
	}
}

func TestRetirementTimeComputedFromActivationEvenIfObservedLate(t *testing.T) {
	s := newTestScheduler(t, 0)
	// 请求时刻 100，启用时刻恒为 110；之后久无操作，到 130 才观察。
	if _, err := s.Rotate("k1", 100); err != nil {
		t.Fatalf("Rotate: %v", err)
	}
	mustSet(t, s, 130, "k0", "k1")
	k0 := mustPhase(t, s, "k0", SignOff)
	k1 := mustPhase(t, s, "k1", Active)
	if k0.SignOffAt != 110 || k0.RetiresAt != 142 {
		t.Fatalf("k0 signOff=%d retires=%d, want 110/142（按启用时刻而非观察时刻 130）",
			k0.SignOffAt, k0.RetiresAt)
	}
	if k1.Activated != 110 {
		t.Fatalf("k1 activated=%d, want 110", k1.Activated)
	}

	// 差一刻 141 不可退役，并返回最早时刻 142。
	err := s.Retire("k0", 141)
	var early *EarlyRetirementError
	if !errors.As(err, &early) || early.Earliest != 142 {
		t.Fatalf("Retire(141) err=%v, want *EarlyRetirementError{142}", err)
	}
	mustPhase(t, s, "k0", SignOff)
	mustSet(t, s, 141, "k0", "k1")

	// 恰到点 142 可退役，退役即移出验证集合。
	if err := s.Retire("k0", 142); err != nil {
		t.Fatalf("Retire(142): %v", err)
	}
	mustPhase(t, s, "k0", Retired)
	mustSet(t, s, 142, "k1")
}

func TestOverlappingRotationsYieldThreeKeys(t *testing.T) {
	s := newTestScheduler(t, 0)
	// t=0 活跃 k0；t=100 请求 k1，启用 110。
	if _, err := s.Rotate("k1", 100); err != nil {
		t.Fatalf("Rotate k1: %v", err)
	}
	// 110 到点后立即请求 k2：启用 120。此时 k0 停签、k1 活跃、k2 已发布，三把重叠。
	if _, err := s.Rotate("k2", 110); err != nil {
		t.Fatalf("Rotate k2: %v", err)
	}
	mustPhase(t, s, "k0", SignOff)
	mustPhase(t, s, "k1", Active)
	mustPhase(t, s, "k2", Published)
	mustSet(t, s, 115, "k0", "k1", "k2")

	// 120 后 k2 活跃、k1 停签，k0 仍在停签窗口（退役时刻 142）。
	mustSet(t, s, 120, "k0", "k1", "k2")
	mustPhase(t, s, "k1", SignOff)
	mustPhase(t, s, "k2", Active)
	k1 := info(t, s, "k1")
	if k1.SignOffAt != 120 || k1.RetiresAt != 152 {
		t.Fatalf("k1 signOff=%d retires=%d, want 120/152", k1.SignOffAt, k1.RetiresAt)
	}
}

func info(t *testing.T, s *Scheduler, id string) KeyInfo {
	t.Helper()
	got, err := s.Key(id)
	if err != nil {
		t.Fatalf("Key(%q): %v", id, err)
	}
	return got
}

func TestRetireRejectionOrdering(t *testing.T) {
	s := newTestScheduler(t, 0)
	if _, err := s.Rotate("k1", 100); err != nil {
		t.Fatalf("Rotate: %v", err)
	}
	// 不存在（即便标识恰好是已退役也不存在 → k0 此刻仍活跃，用未知 id）。
	if err := s.Retire("ghost", 120); !errors.Is(err, ErrKeyNotFound) {
		t.Fatalf("Retire ghost: err=%v, want ErrKeyNotFound", err)
	}
	// 存在但不是停签：k1 在 120 已活跃。
	if err := s.Retire("k1", 120); !errors.Is(err, ErrNotSignOff) {
		t.Fatalf("Retire active: err=%v, want ErrNotSignOff", err)
	}
	// 停签但未到点。
	err := s.Retire("k0", 120)
	var early *EarlyRetirementError
	if !errors.As(err, &early) || early.Earliest != 142 {
		t.Fatalf("Retire early: err=%v, want EarlyRetirement{142}", err)
	}
}

func TestRotateRejectionsDoNotChangeState(t *testing.T) {
	s := newTestScheduler(t, 0)
	if _, err := s.Rotate("k1", 100); err != nil {
		t.Fatalf("Rotate: %v", err)
	}
	snapshot := s.ActiveID()

	// 已有待启用。
	if _, err := s.Rotate("k2", 105); !errors.Is(err, ErrPendingActivation) {
		t.Fatalf("err=%v, want ErrPendingActivation", err)
	}
	// 标识重复（活跃 k0）。
	if _, err := s.Rotate("k0", 105); !errors.Is(err, ErrKeyIDExists) {
		t.Fatalf("err=%v, want ErrKeyIDExists", err)
	}
	// 时钟回退优先于一切，且必须是第一个原因。
	if _, err := s.Rotate("k3", 99); !errors.Is(err, ErrClockMovedBackwards) {
		t.Fatalf("err=%v, want ErrClockMovedBackwards", err)
	}
	if s.ActiveID() != snapshot {
		t.Fatalf("active changed to %q, want %q", s.ActiveID(), snapshot)
	}
	if got := len(mustSetRaw(t, s, 105)); got != 2 {
		t.Fatalf("verification set size=%d, want 2", got)
	}

	// 退役后标识仍占用、不可复用。
	if _, err := s.Advance(510); err != nil {
		t.Fatalf("Advance: %v", err)
	}
	if err := s.Retire("k0", 542); err != nil {
		t.Fatalf("Retire k0: %v", err)
	}
	if _, err := s.Rotate("k0", 543); !errors.Is(err, ErrKeyIDExists) {
		t.Fatalf("reuse retired id: err=%v, want ErrKeyIDExists", err)
	}
}

func mustSetRaw(t *testing.T, s *Scheduler, now int64) []string {
	t.Helper()
	set, err := s.VerificationSet(now)
	if err != nil {
		t.Fatalf("VerificationSet: %v", err)
	}
	return set
}

func TestSingleActiveAndTokenLivenessInvariant(t *testing.T) {
	// 随机化时序，验证两条不变量：
	// 1) 任意时刻至多一个活跃密钥且它必在验证集合；
	// 2) 在 q 时刻签发的令牌，密钥在 [q, q+T+S) 内始终在验证集合。
	s := newTestScheduler(t, 0)
	type token struct {
		key string
		at  int64
	}
	var tokens []token

	// 手工构造含重叠轮换与延迟观察的时序。
	timeline := []struct {
		now    int64
		rotate string
		retire string
		sign   bool
	}{
		{5, "", "", true},
		{10, "k1", "", false},
		{15, "", "", true},  // k0 仍活跃
		{20, "", "", false}, // 观察到 k1 启用（20=10+C）
		{20, "k2", "", false},
		{21, "", "", true},  // k1 活跃
		{30, "", "", false}, // k2 启用（30=20+C）
		{55, "", "k0", false},
		{62, "", "k1", false},
		{62, "", "", true},
	}
	for _, step := range timeline {
		if step.rotate != "" {
			if _, err := s.Rotate(step.rotate, step.now); err != nil {
				t.Fatalf("t=%d Rotate %s: %v", step.now, step.rotate, err)
			}
		}
		if step.sign {
			id, err := s.Sign(step.now)
			if err != nil {
				t.Fatalf("t=%d Sign: %v", step.now, err)
			}
			tokens = append(tokens, token{id, step.now})
		}
		if step.retire != "" {
			err := s.Retire(step.retire, step.now)
			if err != nil {
				t.Fatalf("t=%d Retire %s: %v", step.now, step.retire, err)
			}
		}
		infos, err := s.Snapshot(step.now)
		if err != nil {
			t.Fatalf("t=%d Snapshot: %v", step.now, err)
		}
		set := make([]string, len(infos))
		active := ""
		for i, info := range infos {
			set[i] = info.ID
			if info.Phase == Active {
				active = info.ID
			}
		}
		count := 0
		inSet := false
		for _, id := range set {
			if id == active {
				inSet = true
			}
			if id == active {
				count++
			}
		}
		if active != "" && count != 1 {
			t.Fatalf("t=%d active count=%d, want exactly 1", step.now, count)
		}
		if active == "" || !inSet {
			t.Fatalf("t=%d active %q missing from verification set %v", step.now, active, set)
		}
		// 每条已签发令牌在其 q+T+S 之前必须仍可验证。
		for _, tk := range tokens {
			if step.now < tk.at+cTTL+cSkew {
				found := false
				for _, id := range set {
					if id == tk.key {
						found = true
					}
				}
				if !found {
					t.Fatalf("token signed at %d by %q vanished at %d (before %d)",
						tk.at, tk.key, step.now, tk.at+cTTL+cSkew)
				}
			}
		}
	}
}

func TestConcurrentOperations(t *testing.T) {
	s := newTestScheduler(t, 0)
	var wg sync.WaitGroup
	// 单调时钟由独立推进器提供；轮换/退役/查询/推进任意交错。
	stop := make(chan struct{})
	var clock int64
	var clockMu sync.Mutex

	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
				clockMu.Lock()
				clock++
				now := clock
				clockMu.Unlock()
				if now%3 == 0 {
					_, _ = s.Advance(now)
				}
			}
		}
	}()

	for _, id := range []string{"a", "b", "c", "d", "e"} {
		id := id
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 50; i++ {
				clockMu.Lock()
				now := clock
				clockMu.Unlock()
				_, _ = s.Rotate(id, now) // 重复与待启用拒绝都可接受
				infos, err := s.Snapshot(now)
				if err == nil {
					active := ""
					count := 0
					for _, info := range infos {
						if info.Phase == Active {
							count++
							active = info.ID
						}
					}
					if count > 1 {
						t.Errorf("concurrent: %d active keys", count)
					}
					if count == 0 && active != "" {
						t.Errorf("concurrent: active %q not in set", active)
					}
				}
			}
		}()
	}

	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 200; i++ {
			clockMu.Lock()
			now := clock
			clockMu.Unlock()
			for _, id := range []string{"a", "b", "c", "d", "e"} {
				_ = s.Retire(id, now)
			}
		}
	}()

	for i := 0; i < 200; i++ {
		clockMu.Lock()
		now := clock
		clockMu.Unlock()
		_, _ = s.Sign(now)
	}
	close(stop)
	wg.Wait()
}

func TestDeterministicReplay(t *testing.T) {
	// 相同的操作与时钟序列在两个独立调度器上得到相同状态。
	ops := func(s *Scheduler) {
		_, _ = s.Rotate("a", 5)
		_, _ = s.Rotate("b", 9) // 待启用，拒绝
		_, _ = s.Advance(14)    // a 于 15 之前不启用 → 到 15 才启用；这里无变化
		_, _ = s.Advance(15)
		_, _ = s.Rotate("b", 15)
		_ = s.Retire("k0", 20)
		_, _ = s.Advance(25)
		_ = s.Retire("k0", 47)
		_ = s.Retire("k0", 48)
	}
	s1 := newTestScheduler(t, 0)
	s2 := newTestScheduler(t, 0)
	ops(s1)
	ops(s2)
	for _, id := range append(append([]string{}, s1.order...), "ghost") {
		i1, e1 := s1.Key(id)
		i2, e2 := s2.Key(id)
		if (e1 == nil) != (e2 == nil) {
			t.Fatalf("Key(%q) existence differs: %v vs %v", id, e1, e2)
		}
		if e1 == nil && i1 != i2 {
			t.Fatalf("Key(%q) differs: %+v vs %+v", id, i1, i2)
		}
	}
}

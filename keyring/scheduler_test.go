package keyring

import (
	"errors"
	"fmt"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
)

func mustNew(t *testing.T, c, ttl, skew int64, initial string, now int64) *Scheduler {
	t.Helper()
	s, err := New(c, ttl, skew, initial, now)
	if err != nil {
		t.Fatalf("New(C=%d, T=%d, S=%d, initial=%q, now=%d) 失败: %v", c, ttl, skew, initial, now, err)
	}
	t.Logf("输入: New(C=%d, T=%d, S=%d, initial=%q, now=%d) 输出: 成功 依据: 参数合法, 初始密钥直接活跃",
		c, ttl, skew, initial, now)
	return s
}

func mustSnapshot(t *testing.T, s *Scheduler, now int64) []KeyInfo {
	t.Helper()
	infos, err := s.Snapshot(now)
	if err != nil {
		t.Fatalf("Snapshot(now=%d) 失败: %v", now, err)
	}
	t.Logf("输入: Snapshot(now=%d) 输出: %v", now, infos)
	return infos
}

func findKey(t *testing.T, infos []KeyInfo, id string) KeyInfo {
	t.Helper()
	for _, info := range infos {
		if info.ID == id {
			return info
		}
	}
	t.Fatalf("快照中缺少密钥 %q: %v", id, infos)
	return KeyInfo{}
}

func TestNewRejectsInvalidParams(t *testing.T) {
	cases := []struct {
		name       string
		c, ttl, sk int64
		want       error
	}{
		{"C 为零", 0, 5, 1, ErrNonPositiveCacheTTL},
		{"C 为负数", -1, 5, 1, ErrNonPositiveCacheTTL},
		{"T 为零", 10, 0, 1, ErrNonPositiveTokenTTL},
		{"T 为负数", 10, -5, 1, ErrNonPositiveTokenTTL},
		{"S 为负数", 10, 5, -1, ErrNegativeSkew},
		{"C 优先于 T 报告", 0, 0, -1, ErrNonPositiveCacheTTL},
		{"T 优先于 S 报告", 10, 0, -1, ErrNonPositiveTokenTTL},
	}
	for _, tc := range cases {
		got, err := New(tc.c, tc.ttl, tc.sk, "k0", 0)
		t.Logf("输入: New(C=%d, T=%d, S=%d) 输出: err=%v 依据: %s", tc.c, tc.ttl, tc.sk, err, tc.name)
		if !errors.Is(err, tc.want) {
			t.Errorf("期望 %v, 得到 %v", tc.want, err)
		}
		if got != nil {
			t.Errorf("失败时不得返回调度器")
		}
	}
}

func TestActivationIsCAfterRequest(t *testing.T) {
	s := mustNew(t, 10, 5, 1, "k0", 0)
	activateAt, err := s.Rotate("k1", 3)
	if err != nil {
		t.Fatalf("Rotate 失败: %v", err)
	}
	t.Logf("输入: Rotate(k1, now=3) 输出: activateAt=%d 依据: 启用时刻恒为请求时刻+C=3+10=13", activateAt)
	if activateAt != 13 {
		t.Fatalf("启用时刻应为 13, 得到 %d", activateAt)
	}

	infos := mustSnapshot(t, s, 12)
	if info := findKey(t, infos, "k1"); info.State != StatePublished {
		t.Errorf("now=12 时 k1 应为已发布, 得到 %v", info.State)
	}
	if info := findKey(t, infos, "k0"); info.State != StateActive {
		t.Errorf("now=12 时 k0 应仍活跃, 得到 %v", info.State)
	}

	infos = mustSnapshot(t, s, 13)
	if info := findKey(t, infos, "k1"); info.State != StateActive {
		t.Errorf("now=13 时 k1 应已启用为活跃, 得到 %v", info.State)
	}
	old := findKey(t, infos, "k0")
	if old.State != StateDeactivated || old.DeactivatedAt != 13 {
		t.Errorf("now=13 时 k0 应停签且停签时刻为启用时刻 13, 得到 %+v", old)
	}
	t.Logf("判定依据: 启用时刻 13 到点即完成启用, 旧活跃者停签时刻记为启用时刻而非被观察到的时刻")
}

func TestLazyObservationKeepsRetirementFromActivation(t *testing.T) {
	s := mustNew(t, 10, 5, 2, "k0", 0)
	if _, err := s.Rotate("k1", 3); err != nil {
		t.Fatalf("Rotate 失败: %v", err)
	}
	t.Logf("输入: Rotate(k1, now=3) 后久无操作, 直到 now=1000 才观察")
	infos := mustSnapshot(t, s, 1000)
	old := findKey(t, infos, "k0")
	if old.State != StateDeactivated {
		t.Fatalf("k0 应为停签, 得到 %v", old.State)
	}
	if old.DeactivatedAt != 13 {
		t.Errorf("停签时刻应按启用时刻 13 计算, 得到 %d", old.DeactivatedAt)
	}
	if old.EarliestRetireAt != 20 {
		t.Errorf("最早退役时刻应为 13+T+S=20, 得到 %d", old.EarliestRetireAt)
	}
	t.Logf("判定依据: 惰性推进只影响观察时机, 停签时刻=启用时刻=13, 最早退役=13+T+S=20, 与观察时刻 1000 无关")
}

func TestOverlappingRotationsYieldThreeKeys(t *testing.T) {
	s := mustNew(t, 10, 5, 1, "k0", 0)
	if _, err := s.Rotate("k1", 0); err != nil {
		t.Fatalf("第一次 Rotate 失败: %v", err)
	}
	if _, err := s.Rotate("k2", 5); !errors.Is(err, ErrRotationPending) {
		t.Fatalf("k1 待启用期间第二次 Rotate 应拒绝, 得到 %v", err)
	}
	t.Logf("输入: Rotate(k2, now=5) 输出: ErrRotationPending 依据: k1 待启用, 已有待启用的新密钥")
	activateAt, err := s.Rotate("k2", 10)
	if err != nil {
		t.Fatalf("第二次 Rotate 失败: %v", err)
	}
	t.Logf("输入: Rotate(k2, now=10) 输出: activateAt=%d 依据: 此刻 k1 到点启用, k0 停签, k2 发布", activateAt)
	if activateAt != 20 {
		t.Fatalf("k2 启用时刻应为 20, 得到 %d", activateAt)
	}
	set, err := s.VerificationSet(15)
	if err != nil {
		t.Fatalf("VerificationSet 失败: %v", err)
	}
	t.Logf("输入: VerificationSet(now=15) 输出: %v 依据: k0 停签 + k1 活跃 + k2 已发布, 三态均在验证集合", set)
	want := []string{"k0", "k1", "k2"}
	if !reflect.DeepEqual(set, want) {
		t.Fatalf("验证集合应为 %v, 得到 %v", want, set)
	}
	infos := mustSnapshot(t, s, 15)
	if info := findKey(t, infos, "k0"); info.State != StateDeactivated {
		t.Errorf("k0 应停签, 得到 %v", info.State)
	}
	if info := findKey(t, infos, "k1"); info.State != StateActive {
		t.Errorf("k1 应活跃, 得到 %v", info.State)
	}
	if info := findKey(t, infos, "k2"); info.State != StatePublished {
		t.Errorf("k2 应已发布, 得到 %v", info.State)
	}
}

func TestRetireBoundary(t *testing.T) {
	s := mustNew(t, 10, 5, 2, "k0", 0)
	if _, err := s.Rotate("k1", 0); err != nil {
		t.Fatalf("Rotate 失败: %v", err)
	}
	if _, err := s.Sign(10); err != nil {
		t.Fatalf("Sign 失败: %v", err)
	}
	err := s.Retire("k0", 16)
	var tooEarly *RetireTooEarlyError
	if !errors.As(err, &tooEarly) {
		t.Fatalf("now=16 退役应拒绝, 得到 %v", err)
	}
	t.Logf("输入: Retire(k0, now=16) 输出: %v (Earliest=%d) 依据: 最早退役=停签 10+T 5+S 2=17, 差一刻不可",
		err, tooEarly.Earliest)
	if tooEarly.Earliest != 17 {
		t.Errorf("拒绝应返回最早退役时刻 17, 得到 %d", tooEarly.Earliest)
	}
	if err := s.Retire("k0", 17); err != nil {
		t.Fatalf("now=17 恰到点应可退役, 得到 %v", err)
	}
	t.Logf("输入: Retire(k0, now=17) 输出: 成功 依据: 恰到最早退役时刻即可退役")
	set, err := s.VerificationSet(17)
	if err != nil {
		t.Fatalf("VerificationSet 失败: %v", err)
	}
	for _, id := range set {
		if id == "k0" {
			t.Fatalf("退役后 k0 不应在验证集合: %v", set)
		}
	}
	t.Logf("输入: VerificationSet(now=17) 输出: %v 依据: 退役即从验证集合移除", set)
	if _, err := s.Rotate("k0", 20); !errors.Is(err, ErrDuplicateKeyID) {
		t.Fatalf("已退役标识不得复用, 得到 %v", err)
	}
	t.Logf("输入: Rotate(k0, now=20) 输出: ErrDuplicateKeyID 依据: 已退役标识不得复用")
}

func TestRejectedOpsDoNotChangeState(t *testing.T) {
	s := mustNew(t, 10, 5, 1, "k0", 0)
	if _, err := s.Rotate("k1", 2); err != nil {
		t.Fatalf("Rotate 失败: %v", err)
	}
	before := mustSnapshot(t, s, 4)

	rejects := []struct {
		name string
		op   func() error
		want error
	}{
		{"时钟回拨", func() error { _, err := s.Rotate("k2", 3); return err }, ErrClockRegression},
		{"标识重复(活跃)", func() error { _, err := s.Rotate("k0", 5); return err }, ErrDuplicateKeyID},
		{"标识重复(待启用)", func() error { _, err := s.Rotate("k1", 5); return err }, ErrDuplicateKeyID},
		{"已有待启用", func() error { _, err := s.Rotate("k2", 5); return err }, ErrRotationPending},
		{"退役不存在", func() error { return s.Retire("nope", 5) }, ErrKeyNotFound},
		{"退役活跃密钥", func() error { return s.Retire("k0", 5) }, ErrKeyNotDeactivated},
		{"退役已发布密钥", func() error { return s.Retire("k1", 5) }, ErrKeyNotDeactivated},
	}
	for _, tc := range rejects {
		err := tc.op()
		t.Logf("输入: %s 输出: err=%v 依据: 按顺序只报第一个原因", tc.name, err)
		if !errors.Is(err, tc.want) {
			t.Errorf("%s: 期望 %v, 得到 %v", tc.name, tc.want, err)
		}
	}

	after := mustSnapshot(t, s, 4)
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("被拒绝的操作改变了状态:\n前: %v\n后: %v", before, after)
	}
	t.Logf("判定依据: 全部被拒操作后 now=4 快照与之前完全一致")

	err := s.Retire("k0", 17)
	var tooEarly *RetireTooEarlyError
	if !errors.As(err, &tooEarly) || tooEarly.Earliest != 18 {
		t.Fatalf("now=17 退役应拒绝且 Earliest=18, 得到 %v", err)
	}
	t.Logf("输入: Retire(k0, now=17) 输出: %v 依据: k1 启用时刻 12, 最早退役=12+5+1=18", err)

	signer, err := s.Sign(4)
	if err != nil || signer != "k0" {
		t.Fatalf("被拒操作不得推进已见最大读数, Sign(4) 应成功返回 k0, 得到 %q, %v", signer, err)
	}
	t.Logf("输入: Sign(now=4) 输出: %q 依据: 被拒操作未更新已见最大读数, now=4 仍合法", signer)

	infos := mustSnapshot(t, s, 11)
	if info := findKey(t, infos, "k0"); info.State != StateActive {
		t.Errorf("被拒退役不得提前完成启用, now=11 时 k0 应仍活跃, 得到 %v", info.State)
	}
	if info := findKey(t, infos, "k1"); info.State != StatePublished {
		t.Errorf("now=11 时 k1 应为已发布, 得到 %v", info.State)
	}
	t.Logf("判定依据: 被拒操作触发的惰性推进已回滚, now=11 时启用(到点时刻 12)尚未生效")

	infos = mustSnapshot(t, s, 12)
	old := findKey(t, infos, "k0")
	if old.State != StateDeactivated || old.DeactivatedAt != 12 || old.EarliestRetireAt != 18 {
		t.Errorf("now=12 时 k0 应停签于 12 且最早退役 18, 得到 %+v", old)
	}
}

func TestTokenKeyStaysVerifiable(t *testing.T) {
	s := mustNew(t, 10, 5, 2, "k0", 0)
	if _, err := s.Rotate("k1", 0); err != nil {
		t.Fatalf("Rotate 失败: %v", err)
	}
	issuer, err := s.Sign(9)
	if err != nil {
		t.Fatalf("Sign 失败: %v", err)
	}
	t.Logf("输入: Sign(now=9) 输出: %q 依据: 令牌在启用前一刻由 k0 签发", issuer)
	for now := int64(9); now < 9+5+2; now++ {
		set, err := s.VerificationSet(now)
		if err != nil {
			t.Fatalf("VerificationSet(now=%d) 失败: %v", now, err)
		}
		found := false
		for _, id := range set {
			if id == issuer {
				found = true
			}
		}
		if !found {
			t.Fatalf("now=%d 时签发密钥 %q 不在验证集合 %v", now, issuer, set)
		}
	}
	t.Logf("判定依据: 令牌签发时刻 9 起到 9+T+S=16 之前, k0 始终在验证集合 (k0 停签于 10, 最早退役 17)")
	if err := s.Retire("k0", 16); err == nil {
		t.Fatalf("now=16 退役 k0 应拒绝")
	}
	if err := s.Retire("k0", 17); err != nil {
		t.Fatalf("now=17 退役 k0 应成功: %v", err)
	}
	t.Logf("判定依据: 已退役的密钥不再被任何合法令牌需要, 17 = 停签 10 + T 5 + S 2 >= 签发 9 + T 5 + S 2 = 16")
}

func TestConcurrentOps(t *testing.T) {
	s := mustNew(t, 3, 2, 1, "k0", 0)
	var clock atomic.Int64
	errs := make(chan error, 4096)
	next := func() int64 { return clock.Add(1) }
	allowed := func(err error) bool {
		return err == nil || errors.Is(err, ErrClockRegression)
	}

	var wg sync.WaitGroup
	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < 50; i++ {
				id := fmt.Sprintf("w%d-k%d", w, i)
				_, err := s.Rotate(id, next())
				if !allowed(err) && !errors.Is(err, ErrRotationPending) {
					errs <- fmt.Errorf("Rotate(%q): %w", id, err)
				}
			}
		}(w)
	}
	for w := 0; w < 2; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				if _, err := s.Sign(next()); err != nil {
					if !errors.Is(err, ErrClockRegression) {
						errs <- fmt.Errorf("Sign: %w", err)
					}
				}
				if _, err := s.VerificationSet(next()); err != nil {
					if !errors.Is(err, ErrClockRegression) {
						errs <- fmt.Errorf("VerificationSet: %w", err)
					}
				}
				infos, err := s.Snapshot(next())
				if err != nil {
					if !errors.Is(err, ErrClockRegression) {
						errs <- fmt.Errorf("Snapshot: %w", err)
					}
					continue
				}
				actives := 0
				for _, info := range infos {
					if info.State == StateActive {
						actives++
					}
				}
				if actives != 1 {
					errs <- fmt.Errorf("活跃密钥数应为 1, 得到 %d: %v", actives, infos)
				}
			}
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 100; i++ {
			err := s.Retire("k0", next())
			if !allowed(err) && !errors.Is(err, ErrKeyNotFound) &&
				!errors.Is(err, ErrKeyNotDeactivated) && !errors.As(err, new(*RetireTooEarlyError)) {
				errs <- fmt.Errorf("Retire: %w", err)
			}
		}
	}()
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}

	infos := mustSnapshot(t, s, clock.Load())
	actives := 0
	var activeID string
	for _, info := range infos {
		if info.State == StateActive {
			actives++
			activeID = info.ID
		}
	}
	if actives != 1 {
		t.Fatalf("并发后活跃密钥数应为 1, 得到 %d", actives)
	}
	set, err := s.VerificationSet(clock.Load())
	if err != nil {
		t.Fatalf("VerificationSet 失败: %v", err)
	}
	found := false
	for _, id := range set {
		if id == activeID {
			found = true
		}
	}
	if !found {
		t.Fatalf("活跃密钥 %q 不在验证集合 %v", activeID, set)
	}
	t.Logf("判定依据: 任意交错下至多一个活跃密钥 (%q) 且必在验证集合中", activeID)
}

func TestDeterministicReplay(t *testing.T) {
	script := func() []string {
		s := mustNew(t, 4, 3, 1, "k0", 0)
		var out []string
		record := func(format string, args ...any) {
			out = append(out, fmt.Sprintf(format, args...))
		}
		activateAt, err := s.Rotate("a", 1)
		record("Rotate(a,1) -> %d, %v", activateAt, err)
		signer, err := s.Sign(3)
		record("Sign(3) -> %q, %v", signer, err)
		signer, err = s.Sign(6)
		record("Sign(6) -> %q, %v", signer, err)
		activateAt, err = s.Rotate("b", 6)
		record("Rotate(b,6) -> %d, %v", activateAt, err)
		record("Retire(k0,8) -> %v", s.Retire("k0", 8))
		record("Retire(k0,9) -> %v", s.Retire("k0", 9))
		set, err := s.VerificationSet(10)
		record("VerificationSet(10) -> %v, %v", set, err)
		infos, err := s.Snapshot(12)
		record("Snapshot(12) -> %v, %v", infos, err)
		return out
	}
	first := script()
	second := script()
	t.Logf("输入: 固定操作与时钟序列执行两遍 输出: %v 依据: 相同序列得到相同状态", first)
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("相同操作与时钟序列得到不同状态:\n一: %v\n二: %v", first, second)
	}
}

package suppression

import (
	"errors"
	"strings"
	"sync"
	"testing"
)

func testConfig() Config {
	return Config{
		SoftThreshold:   2,
		SoftWindow:      10,
		SoftTTL:         100,
		TokenTTL:        50,
		DomainThreshold: 2,
		DomainWindow:    30,
		DomainTTL:       200,
	}
}

func mustNew(t *testing.T, cfg Config) *Suppressor {
	t.Helper()
	s, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return s
}

func mustEvent(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("event: %v", err)
	}
}

func checkSuppressed(t *testing.T, s *Suppressor, addr string, now int64, wantSuppressed bool, wantCause Cause) {
	t.Helper()
	got, cause, err := s.IsSuppressed(addr, now)
	if err != nil {
		t.Fatalf("IsSuppressed(%q, %d): %v", addr, now, err)
	}
	if got != wantSuppressed || cause != wantCause {
		t.Fatalf("IsSuppressed(%q, %d) = (%v, %v), want (%v, %v)",
			addr, now, got, cause, wantSuppressed, wantCause)
	}
}

func TestNormalize(t *testing.T) {
	valid := map[string]string{
		"a.b+x@Gmail.com":     "ab@gmail.com",
		"AB@googlemail.com":   "ab@gmail.com",
		"a.b@googlemail.com":  "ab@gmail.com",
		"a.b@example.com":     "a.b@example.com",
		"a.b+c+d@EXAMPLE.com": "a.b@example.com",
		"A@B.COM":             "a@b.com",
	}
	for in, want := range valid {
		got, err := Normalize(in)
		if err != nil || got != want {
			t.Fatalf("Normalize(%q) = (%q, %v), want (%q, nil)", in, got, err, want)
		}
	}

	invalid := []string{
		"..@gmail.com",
		"+x@gmail.com",
		"",
		"a@b",
		"a@b..com",
		"a@.com",
		"a@com.",
		"a b@c.com",
		"a\tb@c.com",
		"a@c.com\x7f",
		"a@@c.com",
		"@c.com",
		"a@",
		"ab.com",
		strings.Repeat("a", 249) + "@c.com",
	}
	for _, in := range invalid {
		if got, err := Normalize(in); !errors.Is(err, ErrInvalidAddress) {
			t.Fatalf("Normalize(%q) = (%q, %v), want ErrInvalidAddress", in, got, err)
		}
	}
	if got, err := Normalize(strings.Repeat("a", 248) + "@c.com"); err != nil || len(got) > 254 {
		t.Fatalf("Normalize(254 bytes) = (%q, %v)", got, err)
	}
}

func TestConfigValidation(t *testing.T) {
	base := testConfig()
	if _, err := New(base); err != nil {
		t.Fatalf("valid config rejected: %v", err)
	}
	bad := []Config{}
	c := base
	c.SoftThreshold = 0
	bad = append(bad, c)
	c = base
	c.SoftThreshold = 17
	bad = append(bad, c)
	c = base
	c.SoftWindow = 0
	bad = append(bad, c)
	c = base
	c.SoftWindow = 1e9 + 1
	bad = append(bad, c)
	c = base
	c.SoftTTL = 0
	bad = append(bad, c)
	c = base
	c.TokenTTL = 1e9 + 1
	bad = append(bad, c)
	c = base
	c.DomainThreshold = 0
	bad = append(bad, c)
	c = base
	c.DomainThreshold = 1001
	bad = append(bad, c)
	c = base
	c.DomainWindow = 0
	bad = append(bad, c)
	c = base
	c.DomainTTL = -1
	bad = append(bad, c)
	for i, cfg := range bad {
		if _, err := New(cfg); !errors.Is(err, ErrInvalidParam) {
			t.Fatalf("bad config %d: err = %v, want ErrInvalidParam", i, err)
		}
	}
	edge := base
	edge.SoftThreshold = 16
	edge.DomainThreshold = 1000
	edge.SoftWindow = 1e9
	if _, err := New(edge); err != nil {
		t.Fatalf("boundary config rejected: %v", err)
	}
}

func TestSpecSoftExample(t *testing.T) {
	s := mustNew(t, testConfig())
	mustEvent(t, s.Soft("a.b+x@Gmail.com", 1))
	mustEvent(t, s.Soft("AB@googlemail.com", 8))
	checkSuppressed(t, s, "ab@gmail.com", 107, true, CauseSoft)
	checkSuppressed(t, s, "ab@gmail.com", 108, false, CauseNone)

	s2 := mustNew(t, testConfig())
	mustEvent(t, s2.Soft("a.b+x@Gmail.com", 1))
	mustEvent(t, s2.Soft("AB@googlemail.com", 11))
	checkSuppressed(t, s2, "ab@gmail.com", 11, false, CauseNone)
}

func TestSoftWindowStrictlyGreater(t *testing.T) {
	s := mustNew(t, testConfig())
	mustEvent(t, s.Soft("a@corp.com", 1))
	mustEvent(t, s.Soft("a@corp.com", 11)) // 窗口保留严格大于 1 的时刻，1 被丢弃
	checkSuppressed(t, s, "a@corp.com", 11, false, CauseNone)
	mustEvent(t, s.Soft("a@corp.com", 12)) // 窗口内 11、12 共 2 个
	checkSuppressed(t, s, "a@corp.com", 12, true, CauseSoft)
}

func TestSoftLazyRecoveryAtSoftUntil(t *testing.T) {
	s := mustNew(t, testConfig())
	mustEvent(t, s.Soft("a@corp.com", 0))
	mustEvent(t, s.Soft("a@corp.com", 1)) // softUntil = 101
	checkSuppressed(t, s, "a@corp.com", 100, true, CauseSoft)
	checkSuppressed(t, s, "a@corp.com", 101, false, CauseNone)
}

func TestHardUpgradesSoft(t *testing.T) {
	s := mustNew(t, testConfig())
	mustEvent(t, s.Soft("a@corp.com", 0))
	mustEvent(t, s.Soft("a@corp.com", 1))
	mustEvent(t, s.Hard("a@corp.com", 5))
	checkSuppressed(t, s, "a@corp.com", 6, true, CauseHard)
	checkSuppressed(t, s, "a@corp.com", 200, true, CauseHard) // 不随 softUntil 恢复
}

func TestRepeatedHardKeepsSinceRefreshesLastHard(t *testing.T) {
	s := mustNew(t, testConfig())
	mustEvent(t, s.Hard("a@corp.com", 100))
	seq, err := s.RequestConfirm("a@corp.com", 120) // 令牌签发于 120
	if err != nil {
		t.Fatalf("RequestConfirm: %v", err)
	}
	mustEvent(t, s.Hard("a@corp.com", 150)) // since 不变（仍为 100），lastHard 刷新为 150
	// 若 since 被刷新为 150，则 issuedAt(120) <= since(150)，令牌应失效。
	if err := s.Confirm("a@corp.com", seq, 151); err != nil {
		t.Fatalf("Confirm 应成功（since 未变）: %v", err)
	}
	checkSuppressed(t, s, "a@corp.com", 152, false, CauseNone)

	// lastHard 被刷新：x 在 140 的硬退信应计入 165 时刻的域级窗口（>135）。
	s2 := mustNew(t, testConfig())
	mustEvent(t, s2.Hard("x@corp.com", 100))
	mustEvent(t, s2.Hard("x@corp.com", 140))
	mustEvent(t, s2.Hard("y@corp.com", 165))
	checkSuppressed(t, s2, "z@corp.com", 166, true, CauseDomain)
}

func TestUnsubIgnoredUnderHard(t *testing.T) {
	s := mustNew(t, testConfig())
	mustEvent(t, s.Hard("a@corp.com", 10))
	mustEvent(t, s.Unsub("a@corp.com", 20))
	checkSuppressed(t, s, "a@corp.com", 21, true, CauseHard)
}

func TestUnsubOverridesSoft(t *testing.T) {
	s := mustNew(t, testConfig())
	mustEvent(t, s.Soft("a@corp.com", 0))
	mustEvent(t, s.Soft("a@corp.com", 1))
	mustEvent(t, s.Unsub("a@corp.com", 5))
	checkSuppressed(t, s, "a@corp.com", 6, true, CauseUnsub)
}

func TestDomainWindowStrictlyGreater(t *testing.T) {
	s := mustNew(t, testConfig())
	mustEvent(t, s.Hard("x@corp.com", 100))
	mustEvent(t, s.Hard("y@corp.com", 130)) // 窗口保留严格大于 100，x 被排除
	checkSuppressed(t, s, "z@corp.com", 130, false, CauseNone)
	mustEvent(t, s.Hard("x@corp.com", 131)) // 窗口内 x=131、y=130 共 2 个，触发
	checkSuppressed(t, s, "z@corp.com", 132, true, CauseDomain)
}

func TestSpecDomainExample(t *testing.T) {
	s := mustNew(t, testConfig())
	mustEvent(t, s.Hard("x@corp.com", 100))
	mustEvent(t, s.Hard("y@corp.com", 120)) // domSince=120, domUntil=320
	checkSuppressed(t, s, "z@corp.com", 200, true, CauseDomain)
	mustEvent(t, s.Unsub("z@corp.com", 130))
	seq, err := s.RequestConfirm("z@corp.com", 140)
	if err != nil {
		t.Fatalf("RequestConfirm: %v", err)
	}
	mustEvent(t, s.Confirm("z@corp.com", seq, 150)) // confirmedAt=150 >= domSince
	checkSuppressed(t, s, "z@corp.com", 200, false, CauseNone)
}

func TestDomainExtendAndRestart(t *testing.T) {
	s := mustNew(t, testConfig())
	mustEvent(t, s.Hard("x@corp.com", 100))
	mustEvent(t, s.Hard("y@corp.com", 120)) // domSince=120, domUntil=320
	mustEvent(t, s.Hard("x@corp.com", 190))
	mustEvent(t, s.Hard("y@corp.com", 200)) // 抑制中再触发：domUntil 延至 400，domSince 不变
	mustEvent(t, s.Unsub("w@corp.com", 210))
	seq, err := s.RequestConfirm("w@corp.com", 220)
	if err != nil {
		t.Fatalf("RequestConfirm: %v", err)
	}
	mustEvent(t, s.Confirm("w@corp.com", seq, 230)) // confirmedAt=230 >= domSince=120，豁免
	checkSuppressed(t, s, "w@corp.com", 399, false, CauseNone)
	checkSuppressed(t, s, "z@corp.com", 399, true, CauseDomain)
	checkSuppressed(t, s, "z@corp.com", 400, false, CauseNone) // domUntil=400 恰等不抑制
	mustEvent(t, s.Hard("x@corp.com", 490))
	mustEvent(t, s.Hard("y@corp.com", 500)) // 已过期再触发：domSince 重起为 500，domUntil=700
	checkSuppressed(t, s, "z@corp.com", 600, true, CauseDomain)
	checkSuppressed(t, s, "w@corp.com", 600, true, CauseDomain) // 230 < 新 domSince=500，不再豁免
}

func TestTokenOverwrittenByNewRequest(t *testing.T) {
	s := mustNew(t, testConfig())
	mustEvent(t, s.Unsub("a@corp.com", 10))
	seq1, err := s.RequestConfirm("a@corp.com", 20)
	if err != nil {
		t.Fatalf("RequestConfirm: %v", err)
	}
	seq2, err := s.RequestConfirm("a@corp.com", 30)
	if err != nil {
		t.Fatalf("RequestConfirm: %v", err)
	}
	if seq2 != seq1+1 {
		t.Fatalf("seq2 = %d, want %d（全局递增）", seq2, seq1+1)
	}
	if err := s.Confirm("a@corp.com", seq1, 40); !errors.Is(err, ErrStaleToken) {
		t.Fatalf("Confirm(旧编号) err = %v, want ErrStaleToken", err)
	}
	mustEvent(t, s.Confirm("a@corp.com", seq2, 40))
	checkSuppressed(t, s, "a@corp.com", 41, false, CauseNone)
	if err := s.Confirm("a@corp.com", seq2, 42); !errors.Is(err, ErrNoToken) {
		t.Fatalf("Confirm(已用令牌) err = %v, want ErrNoToken", err)
	}
}

func TestTokenTTLBoundary(t *testing.T) {
	s := mustNew(t, testConfig()) // TTL=50
	mustEvent(t, s.Unsub("a@corp.com", 10))
	seqA, err := s.RequestConfirm("a@corp.com", 20)
	if err != nil {
		t.Fatalf("RequestConfirm: %v", err)
	}
	mustEvent(t, s.Unsub("b@corp.com", 30))
	seqB, err := s.RequestConfirm("b@corp.com", 40)
	if err != nil {
		t.Fatalf("RequestConfirm: %v", err)
	}
	if err := s.Confirm("a@corp.com", seqA, 70); !errors.Is(err, ErrTokenExpired) {
		t.Fatalf("Confirm(恰等 issuedAt+TTL) err = %v, want ErrTokenExpired", err)
	}
	mustEvent(t, s.Confirm("b@corp.com", seqB, 89)) // 89 < 40+50，差 1 未过期
	checkSuppressed(t, s, "b@corp.com", 90, false, CauseNone)
}

func TestTokenPrecedesSuppressionStart(t *testing.T) {
	s := mustNew(t, testConfig())
	mustEvent(t, s.Unsub("a@corp.com", 10))
	seq, err := s.RequestConfirm("a@corp.com", 20)
	if err != nil {
		t.Fatalf("RequestConfirm: %v", err)
	}
	mustEvent(t, s.Hard("a@corp.com", 30)) // 抑制起点 30 晚于令牌签发 20
	if err := s.Confirm("a@corp.com", seq, 40); !errors.Is(err, ErrTokenPreceded) {
		t.Fatalf("Confirm err = %v, want ErrTokenPreceded", err)
	}
	checkSuppressed(t, s, "a@corp.com", 41, true, CauseHard)
}

func TestComplaintNotRecoverable(t *testing.T) {
	s := mustNew(t, testConfig())
	mustEvent(t, s.Complaint("a@corp.com", 10))
	if _, err := s.RequestConfirm("a@corp.com", 20); !errors.Is(err, ErrRecoveryForbidden) {
		t.Fatalf("RequestConfirm err = %v, want ErrRecoveryForbidden", err)
	}

	mustEvent(t, s.Unsub("b@corp.com", 10))
	seq, err := s.RequestConfirm("b@corp.com", 20)
	if err != nil {
		t.Fatalf("RequestConfirm: %v", err)
	}
	mustEvent(t, s.Complaint("b@corp.com", 30))
	if err := s.Confirm("b@corp.com", seq, 40); !errors.Is(err, ErrConfirmForbidden) {
		t.Fatalf("Confirm err = %v, want ErrConfirmForbidden", err)
	}
	mustEvent(t, s.Hard("b@corp.com", 50)) // 事件不降低 complaint 等级
	checkSuppressed(t, s, "b@corp.com", 60, true, CauseComplaint)
}

func TestConfirmedAtEqualsDomSinceExempt(t *testing.T) {
	s := mustNew(t, testConfig())
	mustEvent(t, s.Hard("x@corp.com", 100))
	mustEvent(t, s.Unsub("w@corp.com", 110))
	seq, err := s.RequestConfirm("w@corp.com", 115)
	if err != nil {
		t.Fatalf("RequestConfirm: %v", err)
	}
	mustEvent(t, s.Confirm("w@corp.com", seq, 120)) // confirmedAt=120
	mustEvent(t, s.Hard("y@corp.com", 120))         // 触发域级：domSince=120
	checkSuppressed(t, s, "z@corp.com", 200, true, CauseDomain)
	checkSuppressed(t, s, "w@corp.com", 200, false, CauseNone) // confirmedAt == domSince 也豁免
}

func TestRejectedOpsNoStateChange(t *testing.T) {
	s := mustNew(t, testConfig())
	if err := s.Soft("bad", 10); !errors.Is(err, ErrInvalidAddress) {
		t.Fatalf("Soft(非法地址) err = %v, want ErrInvalidAddress", err)
	}
	mustEvent(t, s.Soft("a@corp.com", 5)) // 最大 now 未被拒绝对象推进，5 仍合法
	if err := s.Soft("a@corp.com", 4); !errors.Is(err, ErrClockBackwards) {
		t.Fatalf("Soft(时钟回退) err = %v, want ErrClockBackwards", err)
	}
	if _, err := s.RequestConfirm("b@corp.com", 20); !errors.Is(err, ErrRecoveryUnneeded) {
		t.Fatalf("RequestConfirm(无抑制) err = %v, want ErrRecoveryUnneeded", err)
	}
	mustEvent(t, s.Soft("a@corp.com", 6)) // 被拒的 RequestConfirm 未推进最大 now？6 >= 5 即可
	if err := s.Confirm("b@corp.com", 99, 30); !errors.Is(err, ErrNoToken) {
		t.Fatalf("Confirm(无令牌) err = %v, want ErrNoToken", err)
	}
	mustEvent(t, s.Soft("a@corp.com", 7))                   // 被拒的 Confirm 未推进最大 now
	checkSuppressed(t, s, "a@corp.com", 7, true, CauseSoft) // 5、6、7 三次软退信，S=2 早已抑制
	checkSuppressed(t, s, "b@corp.com", 7, false, CauseNone)
	if _, _, err := s.IsSuppressed("bad", 7); !errors.Is(err, ErrInvalidAddress) {
		t.Fatalf("IsSuppressed(非法地址) err = %v, want ErrInvalidAddress", err)
	}
	if _, _, err := s.IsSuppressed("a@corp.com", 6); !errors.Is(err, ErrClockBackwards) {
		t.Fatalf("IsSuppressed(时钟回退) err = %v, want ErrClockBackwards", err)
	}
}

func TestDomainScanCounter(t *testing.T) {
	s := mustNew(t, testConfig())
	mustEvent(t, s.Hard("a@corp.com", 1))
	mustEvent(t, s.Hard("b@corp.com", 2))
	mustEvent(t, s.Unsub("c@corp.com", 3))
	for i := 0; i < 5; i++ {
		mustEvent(t, s.Hard(string(rune('a'+i))+"@other.com", 10))
	}
	mustEvent(t, s.Hard("d@corp.com", 20))
	if s.lastDomainScan != 4 {
		t.Fatalf("lastDomainScan = %d, want 4（仅 corp.com 的地址数）", s.lastDomainScan)
	}
	if got := len(s.dom["corp.com"].addrs); s.lastDomainScan > got {
		t.Fatalf("lastDomainScan = %d 超过该域名地址数 %d", s.lastDomainScan, got)
	}
}

func TestConcurrentUse(t *testing.T) {
	s := mustNew(t, testConfig())
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			addr := string(rune('a'+g)) + "@corp.com"
			for i := 0; i < 200; i++ {
				now := int64(i)
				_ = s.Soft(addr, now)
				_ = s.Hard(addr, now)
				_, _ = s.RequestConfirm(addr, now)
				_ = s.Confirm(addr, uint64(i+1), now)
				_, _, _ = s.IsSuppressed(addr, now)
			}
		}(g)
	}
	wg.Wait()
}

package booking

import (
	"errors"
	"testing"

	"ontology/slotpool"
)

func pb(s string) []byte { return []byte(s) }

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestSpecExample 复现题目给出的完整示例。
func TestSpecExample(t *testing.T) {
	s := New(60, 30, 10, 120, 2, 10000)
	must(t, s.AddSlot(0, "s", 600, 3, 2))

	if _, err := s.Book(100, pb("A"), "s", slotpool.Online); err != nil {
		t.Fatalf("A online: %v", err)
	}
	if _, err := s.Book(100, pb("B"), "s", slotpool.Online); err != nil {
		t.Fatalf("B online: %v", err)
	}
	if _, err := s.Book(100, pb("X"), "s", slotpool.Online); !errors.Is(err, ErrNoQuota) {
		t.Fatalf("X online = %v, want ErrNoQuota", err)
	}
	if _, err := s.Book(100, pb("D"), "s", slotpool.OnSite); err != nil {
		t.Fatalf("D onsite: %v", err)
	}
	if _, err := s.Book(100, pb("F"), "s", slotpool.OnSite); !errors.Is(err, ErrNoQuota) {
		t.Fatalf("F onsite = %v, want ErrNoQuota", err)
	}
	must(t, s.Cancel(480, pb("A"), "s")) // 恰等 start-C，免责
	if _, err := s.Book(539, pb("F"), "s", slotpool.OnSite); !errors.Is(err, ErrNoQuota) {
		t.Fatalf("F@539 = %v, want ErrNoQuota", err)
	}
	if _, err := s.Book(540, pb("F"), "s", slotpool.OnSite); err != nil {
		t.Fatalf("F@540 borrow: %v", err)
	}
	must(t, s.JoinWait(545, pb("H"), "s"))
	must(t, s.CheckIn(570, pb("B"), "s"))
	must(t, s.CheckIn(570, pb("F"), "s"))
	must(t, s.CheckIn(611, pb("H"), "s")) // H 已在落地时递补并标记签到
	sp := s.pool.Get("s")
	if sp.UO+sp.US > sp.Cap {
		t.Fatalf("invariant violated: %d+%d > %d", sp.UO, sp.US, sp.Cap)
	}
}

// TestBannedWindow 验证禁约窗口取等与现场渠道豁免。
func TestBannedWindow(t *testing.T) {
	s := New(60, 30, 10, 120, 2, 10000)
	must(t, s.AddSlot(0, "s", 600, 3, 2))
	_, errD := s.Book(100, pb("D"), "s", slotpool.OnSite)
	must(t, errD)
	if _, err := s.Book(100, pb("B"), "s", slotpool.Online); err != nil {
		t.Fatalf("B online: %v", err)
	}
	if _, err := s.Book(540, pb("F"), "s", slotpool.OnSite); err != nil {
		t.Fatalf("F borrow: %v", err)
	}
	must(t, s.JoinWait(540, pb("H"), "s"))
	must(t, s.CheckIn(611, pb("H"), "s")) // 触发 D 于 610 爽约
	s.cred.Add("D", 5000)
	if !s.Banned(10609, pb("D")) {
		t.Fatal("D 应在 10609 被禁约")
	}
	if s.Banned(10610, pb("D")) {
		t.Fatal("610 恰等 now-W 时不应禁约")
	}
	must(t, s.AddSlot(611, "s2", 20000, 2, 1))
	if _, err := s.Book(10609, pb("D"), "s2", slotpool.Online); !errors.Is(err, ErrBanned) {
		t.Fatalf("online banned = %v", err)
	}
	if _, err := s.Book(10609, pb("D"), "s2", slotpool.OnSite); err != nil {
		t.Fatalf("onsite exempt: %v", err)
	}
}

func must2(t *testing.T, _ int64, err error) {
	t.Helper()
	must(t, err)
}

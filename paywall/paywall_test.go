package paywall_test

import (
	"errors"
	"strconv"
	"testing"

	"ontology/paywall"
)

func mustNew(t *testing.T, cfg paywall.Config) *paywall.Paywall {
	t.Helper()
	p, err := paywall.New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return p
}

func addArts(t *testing.T, p *paywall.Paywall, ids ...string) {
	t.Helper()
	for _, id := range ids {
		if err := p.AddArticle([]byte(id), false); err != nil {
			t.Fatalf("AddArticle %s: %v", id, err)
		}
	}
}

func rd(t *testing.T, p *paywall.Paywall, now int64, device, article string, token []byte) paywall.Result {
	t.Helper()
	r, err := p.Read(now, []byte(device), []byte(article), token)
	if err != nil {
		t.Fatalf("Read %s: %v", article, err)
	}
	return r
}

func wantReason(t *testing.T, r paywall.Result, allowed bool, reason paywall.Reason, ctx string) {
	t.Helper()
	if r.Allowed != allowed || r.Reason != reason {
		t.Fatalf("%s: got allowed=%v reason=%v, want allowed=%v reason=%v", ctx, r.Allowed, r.Reason, allowed, reason)
	}
}

func tb(tok int64) []byte { return []byte(strconv.FormatInt(tok, 10)) }

// 规格例一：N=3，M=1000 的登录合并、登出与跨月。
func TestSpecExampleMerging(t *testing.T) {
	p := mustNew(t, paywall.Config{N: 3, M: 1000, G: 5, K: 1, E: 100})
	addArts(t, p, "a1", "a2", "a3", "a4", "a5", "a6")
	if err := p.Login(0, []byte("d2"), []byte("u")); err != nil {
		t.Fatal(err)
	}
	wantReason(t, rd(t, p, 5, "d2", "a3", nil), true, paywall.ReasonQuota, "t5 u a3")
	wantReason(t, rd(t, p, 10, "d", "a1", nil), true, paywall.ReasonQuota, "t10 d a1")
	wantReason(t, rd(t, p, 15, "d2", "a4", nil), true, paywall.ReasonQuota, "t15 u a4")
	wantReason(t, rd(t, p, 20, "d", "a2", nil), true, paywall.ReasonQuota, "t20 d a2")
	wantReason(t, rd(t, p, 25, "d", "a1", nil), true, paywall.ReasonUnlocked, "t25 d a1")
	if err := p.Login(30, []byte("d"), []byte("u")); err != nil {
		t.Fatalf("login: %v", err)
	}
	wantReason(t, rd(t, p, 31, "d", "a2", nil), false, paywall.ReasonBlocked, "t31 a2 revoked then blocked")
	if err := p.Logout(40, []byte("d")); err != nil {
		t.Fatal(err)
	}
	wantReason(t, rd(t, p, 41, "d", "a2", nil), true, paywall.ReasonUnlocked, "t41 anon a2")
	wantReason(t, rd(t, p, 42, "d", "a5", nil), true, paywall.ReasonQuota, "t42 a5 quota")
	wantReason(t, rd(t, p, 43, "d", "a6", nil), false, paywall.ReasonBlocked, "t43 a6 blocked")
	wantReason(t, rd(t, p, 1000, "d", "a6", nil), true, paywall.ReasonQuota, "new month resets")
}

// 规格例二：礼赠令牌 K=1，E=100。
func TestSpecExampleGift(t *testing.T) {
	p := mustNew(t, paywall.Config{N: 3, M: 1000, G: 5, K: 1, E: 100})
	addArts(t, p, "a7", "a9")
	if err := p.Subscribe(50, []byte("u"), 60); err != nil {
		t.Fatal(err)
	}
	tok, err := p.Gift(55, []byte("u"), []byte("a9"))
	if err != nil || tok != 1 {
		t.Fatalf("gift tok=%d err=%v", tok, err)
	}
	wantReason(t, rd(t, p, 56, "x", "a9", tb(tok)), true, paywall.ReasonGift, "x gift")
	wantReason(t, rd(t, p, 57, "y", "a9", tb(tok)), true, paywall.ReasonQuota, "y full -> quota")
	if err := p.Login(58, []byte("du"), []byte("u")); err != nil {
		t.Fatal(err)
	}
	wantReason(t, rd(t, p, 59, "du", "a7", nil), true, paywall.ReasonSubscription, "u subscribed")
	wantReason(t, rd(t, p, 60, "du", "a7", nil), true, paywall.ReasonQuota, "now==until expired")
	wantReason(t, rd(t, p, 155, "x", "a9", tb(tok)), true, paywall.ReasonUnlocked, "x still unlocked in month")
	// 新主体 z 在 t=155 使用过期令牌：无效，回落额度。
	wantReason(t, rd(t, p, 156, "z", "a9", tb(tok)), true, paywall.ReasonQuota, "fresh subject token expired exactly")
}

func TestSubscriptionBoundaryAndNoLedger(t *testing.T) {
	p := mustNew(t, paywall.Config{N: 1, M: 1000, G: 5, K: 1, E: 100})
	addArts(t, p, "a")
	if err := p.Subscribe(1, []byte("u"), 10); err != nil {
		t.Fatal(err)
	}
	if err := p.Login(2, []byte("d"), []byte("u")); err != nil {
		t.Fatal(err)
	}
	wantReason(t, rd(t, p, 5, "d", "a", nil), true, paywall.ReasonSubscription, "during sub")
	if err := p.Logout(6, []byte("d")); err != nil {
		t.Fatal(err)
	}
	wantReason(t, rd(t, p, 10, "d", "a", nil), true, paywall.ReasonQuota, "now==until, no ledger write during sub")
}

func TestFreeArticleAndNZero(t *testing.T) {
	p := mustNew(t, paywall.Config{N: 0, M: 1000, G: 5, K: 1, E: 100})
	if err := p.AddArticle([]byte("f"), true); err != nil {
		t.Fatal(err)
	}
	addArts(t, p, "p")
	for _, now := range []int64{1, 2, 3} {
		wantReason(t, rd(t, p, now, "d", "f", nil), true, paywall.ReasonFree, "free")
	}
	if r := rd(t, p, 4, "d", "p", nil); r.Allowed {
		t.Fatalf("N=0 must block paid: %+v", r)
	}
}

func TestMergeTieByteOrder(t *testing.T) {
	p := mustNew(t, paywall.Config{N: 2, M: 1000, G: 5, K: 1, E: 100})
	addArts(t, p, "aaa", "bbb", "ccc", "ddd")
	if err := p.Login(0, []byte("d2"), []byte("u")); err != nil {
		t.Fatal(err)
	}
	rd(t, p, 10, "d2", "ccc", nil)
	rd(t, p, 10, "d2", "ddd", nil)
	rd(t, p, 10, "d", "aaa", nil)
	rd(t, p, 10, "d", "bbb", nil)
	if err := p.Login(11, []byte("d"), []byte("u")); err != nil {
		t.Fatal(err)
	}
	wantReason(t, rd(t, p, 12, "d", "aaa", nil), true, paywall.ReasonUnlocked, "aaa kept")
	wantReason(t, rd(t, p, 12, "d2", "bbb", nil), true, paywall.ReasonUnlocked, "bbb kept")
	wantReason(t, rd(t, p, 13, "d", "ccc", nil), false, paywall.ReasonBlocked, "ccc revoked")
	wantReason(t, rd(t, p, 13, "d2", "ddd", nil), false, paywall.ReasonBlocked, "ddd revoked")
}

func TestMergeGiftKeptBeyondN(t *testing.T) {
	p := mustNew(t, paywall.Config{N: 1, M: 1000, G: 5, K: 2, E: 100})
	addArts(t, p, "a", "b", "g")
	if err := p.Login(0, []byte("d2"), []byte("u")); err != nil {
		t.Fatal(err)
	}
	if err := p.Subscribe(1, []byte("u"), 35); err != nil {
		t.Fatal(err)
	}
	tok, err := p.Gift(2, []byte("u"), []byte("g"))
	if err != nil {
		t.Fatal(err)
	}
	rd(t, p, 36, "d2", "b", nil) // t=36 订阅已失效，b 走额度
	rd(t, p, 38, "d", "g", tb(tok))
	rd(t, p, 39, "d", "a", nil)
	if err := p.Login(40, []byte("d"), []byte("u")); err != nil {
		t.Fatal(err)
	}
	wantReason(t, rd(t, p, 41, "d", "g", nil), true, paywall.ReasonUnlocked, "gift merged")
	wantReason(t, rd(t, p, 41, "d2", "b", nil), true, paywall.ReasonUnlocked, "quota kept")
	wantReason(t, rd(t, p, 42, "d", "a", nil), false, paywall.ReasonBlocked, "quota revoked")
}

func TestLogoutRestoresAndRepeatedLogin(t *testing.T) {
	p := mustNew(t, paywall.Config{N: 3, M: 1000, G: 5, K: 1, E: 100})
	addArts(t, p, "a1", "a2", "a3")
	rd(t, p, 10, "d", "a1", nil)
	rd(t, p, 11, "d", "a2", nil)
	if err := p.Login(20, []byte("d"), []byte("u")); err != nil {
		t.Fatal(err)
	}
	if err := p.Logout(21, []byte("d")); err != nil {
		t.Fatal(err)
	}
	wantReason(t, rd(t, p, 22, "d", "a1", nil), true, paywall.ReasonUnlocked, "anon a1")
	wantReason(t, rd(t, p, 23, "d", "a2", nil), true, paywall.ReasonUnlocked, "anon a2")
	wantReason(t, rd(t, p, 24, "d", "a3", nil), true, paywall.ReasonQuota, "anon a3 quota")
	if err := p.Login(25, []byte("d"), []byte("u")); err != nil {
		t.Fatal(err)
	}
	if err := p.Logout(26, []byte("d")); err != nil {
		t.Fatal(err)
	}
	wantReason(t, rd(t, p, 27, "d", "a1", nil), true, paywall.ReasonUnlocked, "a1 after relogin")
}

func TestTokenReuseFullAndInvalid(t *testing.T) {
	p := mustNew(t, paywall.Config{N: 3, M: 1000, G: 5, K: 2, E: 100})
	addArts(t, p, "g", "a9")
	if err := p.Subscribe(1, []byte("u"), 35); err != nil {
		t.Fatal(err)
	}
	tok, _ := p.Gift(2, []byte("u"), []byte("g"))
	wantReason(t, rd(t, p, 10, "x", "g", tb(tok)), true, paywall.ReasonGift, "x first")
	wantReason(t, rd(t, p, 11, "x", "g", tb(tok)), true, paywall.ReasonUnlocked, "x reuse unlocked")
	wantReason(t, rd(t, p, 12, "y", "g", tb(tok)), true, paywall.ReasonGift, "y second subject")
	wantReason(t, rd(t, p, 13, "z", "g", tb(tok)), true, paywall.ReasonQuota, "z full -> quota")
	wantReason(t, rd(t, p, 14, "x", "a9", tb(tok)), true, paywall.ReasonQuota, "wrong article ignored")
	wantReason(t, rd(t, p, 15, "w", "g", []byte("nonsense")), true, paywall.ReasonQuota, "malformed token ignored")
}

func TestGiftMonthlyCapNonSubscriber(t *testing.T) {
	p := mustNew(t, paywall.Config{N: 3, M: 1000, G: 1, K: 1, E: 100})
	addArts(t, p, "a", "b")
	if _, err := p.Gift(1, []byte("u"), []byte("a")); !errors.Is(err, paywall.ErrNotSubscribed) {
		t.Fatalf("gift without sub err=%v", err)
	}
	if err := p.Subscribe(1, []byte("u"), 5000); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Gift(2, []byte("u"), []byte("a")); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Gift(3, []byte("u"), []byte("b")); !errors.Is(err, paywall.ErrGiftExhausted) {
		t.Fatalf("second gift err=%v", err)
	}
	if _, err := p.Gift(1000, []byte("u"), []byte("b")); err != nil {
		t.Fatalf("new month gift: %v", err)
	}
}

func TestCrossMonthZero(t *testing.T) {
	p := mustNew(t, paywall.Config{N: 1, M: 100, G: 5, K: 1, E: 1000})
	addArts(t, p, "a")
	wantReason(t, rd(t, p, 50, "d", "a", nil), true, paywall.ReasonQuota, "quota m0")
	wantReason(t, rd(t, p, 99, "d", "a", nil), true, paywall.ReasonUnlocked, "unlocked m0")
	wantReason(t, rd(t, p, 100, "d", "a", nil), true, paywall.ReasonQuota, "fresh m1")
}

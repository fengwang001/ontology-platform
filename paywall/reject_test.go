package paywall_test

import (
	"errors"
	"testing"

	"ontology/paywall"
)

func TestConfigValidation(t *testing.T) {
	good := paywall.Config{N: 3, M: 1000, G: 1, K: 1, E: 100}
	bad := []paywall.Config{
		{-1, 1000, 1, 1, 100},
		{1001, 1000, 1, 1, 100},
		{3, 0, 1, 1, 100},
		{3, 1000000001, 1, 1, 100},
		{3, 1000, 0, 1, 100},
		{3, 1000, 1, 0, 100},
		{3, 1000, 1, 1, 0},
	}
	for i, cfg := range bad {
		if _, err := paywall.New(cfg); !errors.Is(err, paywall.ErrInvalidArgument) {
			t.Fatalf("bad cfg #%d: err=%v", i, err)
		}
	}
	if _, err := paywall.New(good); err != nil {
		t.Fatalf("good cfg: %v", err)
	}
}

// Read 拒绝次序：参数非法 > 时钟回退 > 文章不存在。
func TestReadRejectOrder(t *testing.T) {
	p := mustNew(t, paywall.Config{N: 1, M: 1000, G: 1, K: 1, E: 100})
	addArts(t, p, "a")
	rd(t, p, 10, "d", "a", nil) // 推进时钟到 10
	// 参数非法优先于时钟回退与文章不存在。
	if _, err := p.Read(5, nil, []byte("missing"), nil); !errors.Is(err, paywall.ErrInvalidArgument) {
		t.Fatalf("empty device: %v", err)
	}
	if _, err := p.Read(5, []byte("d"), nil, nil); !errors.Is(err, paywall.ErrInvalidArgument) {
		t.Fatalf("empty article: %v", err)
	}
	if _, err := p.Read(-1, []byte("d"), []byte("a"), nil); !errors.Is(err, paywall.ErrInvalidArgument) {
		t.Fatalf("negative now: %v", err)
	}
	// 时钟回退优先于文章不存在。
	if _, err := p.Read(9, []byte("d"), []byte("missing"), nil); !errors.Is(err, paywall.ErrClockRollback) {
		t.Fatalf("rollback beats unknown article: %v", err)
	}
	if _, err := p.Read(11, []byte("d"), []byte("missing"), nil); !errors.Is(err, paywall.ErrArticleUnknown) {
		t.Fatalf("unknown article: %v", err)
	}
	// 被拒绝的操作不推进时钟：now=11 的 Read 因文章不存在被拒，时钟仍是 10，now=10 合法。
	if _, err := p.Read(10, []byte("d2"), []byte("a"), nil); err != nil {
		t.Fatalf("rejected op must not advance clock: %v", err)
	}
}

// Login 拒绝次序：参数非法 > 时钟回退 > 已绑定（含同一用户）。
func TestLoginRejectOrder(t *testing.T) {
	p := mustNew(t, paywall.Config{N: 1, M: 1000, G: 1, K: 1, E: 100})
	if err := p.Login(10, []byte("d"), []byte("u")); err != nil {
		t.Fatal(err)
	}
	if err := p.Login(5, nil, []byte("u")); !errors.Is(err, paywall.ErrInvalidArgument) {
		t.Fatalf("empty device: %v", err)
	}
	if err := p.Login(5, []byte("d"), nil); !errors.Is(err, paywall.ErrInvalidArgument) {
		t.Fatalf("empty user: %v", err)
	}
	// 时钟回退优先于已绑定。
	if err := p.Login(9, []byte("d"), []byte("u")); !errors.Is(err, paywall.ErrClockRollback) {
		t.Fatalf("rollback beats bound: %v", err)
	}
	// 已绑定（同一用户）。
	if err := p.Login(11, []byte("d"), []byte("u")); !errors.Is(err, paywall.ErrAlreadyBound) {
		t.Fatalf("same user rebind: %v", err)
	}
	if err := p.Login(12, []byte("d"), []byte("v")); !errors.Is(err, paywall.ErrAlreadyBound) {
		t.Fatalf("other user rebind: %v", err)
	}
}

// Logout 拒绝次序：参数非法 > 时钟回退 > 未绑定。
func TestLogoutRejectOrder(t *testing.T) {
	p := mustNew(t, paywall.Config{N: 1, M: 1000, G: 1, K: 1, E: 100})
	addArts(t, p, "a")
	rd(t, p, 10, "seed", "a", nil) // 接受的操作，时钟推进到 10
	if err := p.Logout(10, nil); !errors.Is(err, paywall.ErrInvalidArgument) {
		t.Fatalf("empty device: %v", err)
	}
	if err := p.Logout(5, []byte("d")); !errors.Is(err, paywall.ErrClockRollback) {
		t.Fatalf("rollback beats not bound: %v", err)
	}
	if err := p.Logout(10, []byte("d")); !errors.Is(err, paywall.ErrNotBound) {
		t.Fatalf("not bound: %v", err)
	}
}

// Subscribe 拒绝次序：参数非法（含 until<=now）> 时钟回退。
func TestSubscribeRejectOrder(t *testing.T) {
	p := mustNew(t, paywall.Config{N: 1, M: 1000, G: 1, K: 1, E: 100})
	if err := p.Subscribe(10, []byte("u"), 10); !errors.Is(err, paywall.ErrInvalidArgument) {
		t.Fatalf("until==now: %v", err)
	}
	if err := p.Subscribe(10, []byte("u"), 9); !errors.Is(err, paywall.ErrInvalidArgument) {
		t.Fatalf("until<now: %v", err)
	}
	if err := p.Subscribe(10, nil, 20); !errors.Is(err, paywall.ErrInvalidArgument) {
		t.Fatalf("empty user: %v", err)
	}
	if err := p.Subscribe(10, []byte("u"), 20); err != nil {
		t.Fatal(err)
	}
	if err := p.Subscribe(9, []byte("u"), 100); !errors.Is(err, paywall.ErrClockRollback) {
		t.Fatalf("rollback: %v", err)
	}
}

// Gift 拒绝次序：参数非法 > 时钟回退 > 文章不存在 > 非订阅者 > 额度用尽。
func TestGiftRejectOrder(t *testing.T) {
	p := mustNew(t, paywall.Config{N: 1, M: 1000, G: 1, K: 1, E: 100})
	addArts(t, p, "a", "b")
	if _, err := p.Gift(5, nil, []byte("a")); !errors.Is(err, paywall.ErrInvalidArgument) {
		t.Fatalf("empty user: %v", err)
	}
	if _, err := p.Gift(5, []byte("u"), nil); !errors.Is(err, paywall.ErrInvalidArgument) {
		t.Fatalf("empty article: %v", err)
	}
	// 时钟回退优先于文章不存在。
	if err := p.Subscribe(10, []byte("u"), 100); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Gift(9, []byte("u"), []byte("missing")); !errors.Is(err, paywall.ErrClockRollback) {
		t.Fatalf("rollback beats unknown article: %v", err)
	}
	if _, err := p.Gift(11, []byte("u"), []byte("missing")); !errors.Is(err, paywall.ErrArticleUnknown) {
		t.Fatalf("unknown article: %v", err)
	}
	// 非订阅者优先于额度用尽（另一用户）。
	if _, err := p.Gift(12, []byte("v"), []byte("a")); !errors.Is(err, paywall.ErrNotSubscribed) {
		t.Fatalf("non subscriber: %v", err)
	}
	if _, err := p.Gift(13, []byte("u"), []byte("a")); err != nil {
		t.Fatalf("first gift: %v", err)
	}
	if _, err := p.Gift(14, []byte("u"), []byte("b")); !errors.Is(err, paywall.ErrGiftExhausted) {
		t.Fatalf("exhausted: %v", err)
	}
}

func TestAddArticleRejects(t *testing.T) {
	p := mustNew(t, paywall.Config{N: 1, M: 1000, G: 1, K: 1, E: 100})
	if err := p.AddArticle(nil, false); !errors.Is(err, paywall.ErrInvalidArgument) {
		t.Fatalf("empty id: %v", err)
	}
	if err := p.AddArticle([]byte("a"), false); err != nil {
		t.Fatal(err)
	}
	if err := p.AddArticle([]byte("a"), true); !errors.Is(err, paywall.ErrInvalidArgument) {
		t.Fatalf("redefining free flag: %v", err)
	}
	if err := p.AddArticle([]byte("a"), false); err != nil {
		t.Fatalf("same flag should be idempotent: %v", err)
	}
}

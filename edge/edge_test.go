package edge_test

import (
	"errors"
	"strings"
	"sync"
	"testing"

	"ontology/edge"
	"ontology/purge"
	"ontology/quota"
)

func newCP(t *testing.T, lmax int, limits ...struct {
	name string
	l    quota.Limits
}) (*edge.ControlPlane, *quota.Registry, *purge.Store) {
	t.Helper()
	reg := quota.NewRegistry()
	for _, q := range limits {
		if err := reg.Register(q.name, q.l); err != nil {
			t.Fatal(err)
		}
	}
	rules := purge.NewStore()
	cp := edge.New(reg, rules, lmax)
	if cp == nil {
		t.Fatal("New returned nil")
	}
	return cp, reg, rules
}

func ql(name string, qu, qd, qw int) struct {
	name string
	l    quota.Limits
} {
	return struct {
		name string
		l    quota.Limits
	}{name, quota.Limits{Qu: qu, Qd: qd, Qw: qw}}
}

func TestSpecPurgeExample(t *testing.T) {
	cp, reg, _ := newCP(t, 1000, ql("t", 3, 1, 3))
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(cp.Fill(1, "t", "/img/a.jpg"))
	st, err := cp.Fresh(1, "t", "/img/a.jpg")
	must(err)
	if st != edge.StatusFresh {
		t.Fatalf("fresh before purge = %v", st)
	}

	u, d, err := cp.Purge(1, "t", []string{"/img/", "/img/a.jpg", "/img/a.jpg", "/css/x.css"})
	must(err)
	if u != 1 || d != 1 {
		t.Fatalf("billing u=%d d=%d want 1 1", u, d)
	}
	st, _ = cp.Fresh(1, "t", "/img/a.jpg")
	if st != edge.StatusStale {
		t.Fatalf("after purge = %v want stale", st)
	}

	must(cp.Fill(1, "t", "/img/a.jpg"))
	st, _ = cp.Fresh(1, "t", "/img/a.jpg")
	if st != edge.StatusFresh { // filled 恰等纪元 -> 新鲜
		t.Fatalf("refill = %v want fresh", st)
	}

	_, _, err = cp.Purge(1, "t", []string{"/img/b.jpg", "/js/"})
	if !errors.Is(err, edge.ErrDirQuota) {
		t.Fatalf("dir quota: %v", err)
	}
	usedU, usedD, _, _ := reg.Used("t", 1)
	if usedU != 1 || usedD != 1 {
		t.Fatalf("rejected batch changed usage: %d %d", usedU, usedD)
	}

	u, d, err = cp.Purge(1, "t", []string{"/img/a.jpg", "/img/b.jpg"})
	must(err)
	if u != 2 || d != 0 {
		t.Fatalf("exact fit u=%d d=%d", u, d)
	}
	st, _ = cp.Fresh(1, "t", "/img/a.jpg")
	if st != edge.StatusStale {
		t.Fatalf("epoch 2 should stale: %v", st)
	}

	_, _, err = cp.Purge(1, "t", []string{"/x"})
	if !errors.Is(err, edge.ErrURLQuota) {
		t.Fatalf("url quota: %v", err)
	}

	// 跨日归零（now 纯函数）。
	u, d, w, _ := reg.Used("t", 86400)
	if u != 0 || d != 0 || w != 0 {
		t.Fatalf("next day usage = %d %d %d", u, d, w)
	}
}

func TestPathShapeDistinctions(t *testing.T) {
	cp, _, _ := newCP(t, 1000, ql("t", 100, 100, 100))
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(cp.Fill(1, "t", "/img/a.jpg"))
	must(cp.Fill(1, "t", "/img"))
	must(cp.Fill(1, "t", "/imgs/a"))
	_, _, err := cp.Purge(1, "t", []string{"/img/"})
	must(err)
	check := func(url string, want edge.FreshStatus) {
		t.Helper()
		got, _ := cp.Fresh(1, "t", url)
		if got != want {
			t.Errorf("%s = %v want %v", url, got, want)
		}
	}
	check("/img/a.jpg", edge.StatusStale) // /img/ 覆盖
	check("/img", edge.StatusFresh)       // URL /img 不被 /img/ 覆盖
	check("/imgs/a", edge.StatusFresh)    // 前缀但不同段

	// 根目录刷新
	must(cp.Fill(1, "t", "/any/deep/x"))
	_, _, err = cp.Purge(1, "t", []string{"/"})
	must(err)
	check("/any/deep/x", edge.StatusStale)
	check("/img", edge.StatusStale)
}

func TestRejectOrders(t *testing.T) {
	// Purge: 参数非法 > 时钟回退 > 租户不存在 > URL 配额 > 目录配额
	t.Run("purge", func(t *testing.T) {
		cp, _, _ := newCP(t, 1000, ql("t", 1, 1, 1))
		try := func(now int64, tenant string, items []string, want error) {
			t.Helper()
			_, _, err := cp.Purge(now, tenant, items)
			if !errors.Is(err, want) {
				t.Fatalf("Purge(%v,%q,%v) = %v want %v", now, tenant, items, err, want)
			}
		}
		try(10, "t", []string{"/bad/."}, edge.ErrInvalidArg)
		try(10, "t", nil, edge.ErrInvalidArg)
		mustAdvance(t, cp) // 接受一次操作把 now 推到 10
		try(5, "t", []string{"/a"}, edge.ErrClockBack)
		try(10, "ghost", []string{"/a"}, edge.ErrUnknownTenant)
		_, _, _ = cp.Purge(10, "t", []string{"/a", "/b"}) // u=2 > Qu=1：URL 不足
		_, _, _ = cp.Purge(10, "t", []string{"/d/"})
		// URL 与目录都会超：先报 URL
		try(10, "t", []string{"/x", "/y", "/e/"}, edge.ErrURLQuota)
		// URL 恰好不超、目录超：报目录
		try(10, "t", []string{"/e/"}, edge.ErrDirQuota)
	})

	// Prewarm: 参数非法 > 时钟回退 > 租户不存在 > 预热配额 > 队列已满
	t.Run("prewarm", func(t *testing.T) {
		cp, _, _ := newCP(t, 2, ql("t", 10, 10, 1), ql("u", 10, 10, 10))
		try := func(now int64, tenant string, urls []string, want error) {
			t.Helper()
			_, err := cp.Prewarm(now, tenant, urls)
			if !errors.Is(err, want) {
				t.Fatalf("Prewarm(%v,%q,%v) = %v want %v", now, tenant, urls, err, want)
			}
		}
		try(10, "t", []string{"/dir/"}, edge.ErrInvalidArg)
		try(10, "t", nil, edge.ErrInvalidArg)
		_ = cp.Fill(10, "t", "/x")
		try(5, "t", []string{"/a"}, edge.ErrClockBack)
		try(10, "ghost", []string{"/a"}, edge.ErrUnknownTenant)
		_, err := cp.Prewarm(10, "t", []string{"/a"})
		if err != nil {
			t.Fatal(err)
		}
		// 配额不足先于队列满（队列还能装，但 Qw=1 已满）
		try(10, "t", []string{"/b"}, edge.ErrWarmQuota)
		// 另一租户：配额够、队列将超 Lmax
		try(10, "u", []string{"/b", "/c"}, edge.ErrQueueFull)
	})

	// Fill: 参数非法 > 时钟回退 > 租户不存在
	t.Run("fill", func(t *testing.T) {
		cp, _, _ := newCP(t, 1000, ql("t", 1, 1, 1))
		if err := cp.Fill(10, "t", "noleading"); !errors.Is(err, edge.ErrInvalidArg) {
			t.Fatalf("fill invalid: %v", err)
		}
		_ = cp.Fill(10, "t", "/x")
		if err := cp.Fill(5, "t", "/x"); !errors.Is(err, edge.ErrClockBack) {
			t.Fatalf("fill clockback: %v", err)
		}
		if err := cp.Fill(10, "ghost", "/x"); !errors.Is(err, edge.ErrUnknownTenant) {
			t.Fatalf("fill tenant: %v", err)
		}
	})
}

func mustAdvance(t *testing.T, cp *edge.ControlPlane) {
	t.Helper()
	if err := cp.Fill(10, "t", "/advance"); err != nil {
		t.Fatal(err)
	}
}

func TestRejectedBatchLeavesNoTrace(t *testing.T) {
	cp, reg, rules := newCP(t, 1000, ql("t", 1, 0, 100))
	if err := cp.Fill(1, "t", "/a"); err != nil {
		t.Fatal(err)
	}
	// 目录配额 Qd=0：拒绝
	_, _, err := cp.Purge(1, "t", []string{"/d/"})
	if !errors.Is(err, edge.ErrDirQuota) {
		t.Fatalf("want dir quota, got %v", err)
	}
	st, _ := cp.Fresh(1, "t", "/d/x")
	if st != edge.StatusMissing {
		t.Fatalf("rejected purge created rules: %v", st)
	}
	if epoch, _ := rules.MaxEpoch("t", "/a"); epoch != 0 {
		t.Fatalf("epoch advanced on reject: %d", epoch)
	}
	u, d, _, _ := reg.Used("t", 1)
	if u != 0 || d != 0 {
		t.Fatalf("usage changed: %d %d", u, d)
	}

	// 预热整批拒绝不入队
	_, err = cp.Prewarm(1, "t", []string{"/w1", "/w2", "/w3"}) // Qw=100 ok；再测队列
	if err != nil {
		t.Fatal(err)
	}
	lists, err := cp.Tick(1, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(lists.Filled) != 3 {
		t.Fatalf("tick = %v", lists)
	}
}

func TestTenantIsolation(t *testing.T) {
	cp, _, _ := newCP(t, 1000, ql("a", 10, 10, 10), ql("b", 10, 10, 10))
	if err := cp.Fill(1, "a", "/x"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := cp.Purge(1, "b", []string{"/x"}); err != nil {
		t.Fatal(err)
	}
	st, _ := cp.Fresh(1, "a", "/x")
	if st != edge.StatusFresh {
		t.Fatalf("tenant b purge affected a: %v", st)
	}
	st, _ = cp.Fresh(1, "b", "/x")
	if st != edge.StatusMissing {
		t.Fatalf("b should miss: %v", st)
	}
}

func TestConcurrent(t *testing.T) {
	cp, _, _ := newCP(t, 1_000_000, ql("t", 1_000_000, 1_000_000, 1_000_000))
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				url := "/p/" + itoa(g) + "/" + itoa(i)
				_ = cp.Fill(100, "t", url)
				_, _ = cp.Fresh(100, "t", url)
				if i%7 == 0 {
					_, _, _ = cp.Purge(100, "t", []string{"/p/" + itoa(g) + "/"})
				}
				_, _ = cp.Prewarm(100, "t", []string{url})
			}
		}(g)
	}
	wg.Wait()
	lists, err := cp.Tick(100, 10000)
	if err != nil {
		t.Fatal(err)
	}
	if len(lists.Filled)+len(lists.Skipped) == 0 {
		t.Fatal("expected queue drained")
	}
	// 队列应清空
	again, _ := cp.Tick(100, 10000)
	if len(again.Filled)+len(again.Skipped) != 0 {
		t.Fatalf("queue not empty: %v", again)
	}
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b [20]byte
	pos := len(b)
	for i > 0 {
		pos--
		b[pos] = byte('0' + i%10)
		i /= 10
	}
	return string(b[pos:])
}

var _ = strings.Repeat

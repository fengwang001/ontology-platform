package locrib

import (
	"errors"
	"sync"
	"testing"

	"ontology/policy"
)

func acceptAll() []Term { return []Term{{Action: ActionAccept}} }

func pfx(addr uint32, l uint8) Prefix { return Prefix{Addr: addr, Len: l} }

func addPeer(t *testing.T, e *Engine, id, as, rid uint32) {
	t.Helper()
	if err := e.AddPeer(id, as, rid); err != nil {
		t.Fatalf("AddPeer(%d): %v", id, err)
	}
	if _, err := e.SetPolicy(id, acceptAll()); err != nil {
		t.Fatalf("SetPolicy(%d): %v", id, err)
	}
}

func equalChanges(a, b []Change) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Peer != b[i].Peer || a[i].Prefix != b[i].Prefix || a[i].Withdraw != b[i].Withdraw {
			return false
		}
		if !a[i].Withdraw && !policy.EqualAttrs(a[i].Attrs, b[i].Attrs) {
			return false
		}
	}
	return true
}

func assertChanges(t *testing.T, what string, got, want []Change) {
	t.Helper()
	if !equalChanges(got, want) {
		t.Errorf("%s:\n got=%+v\nwant=%+v", what, got, want)
	}
}

// adv 构造期望的外部通告差量：asPath 前插 localAS，localPref 与 med 置 0。
func adv(peer uint32, p Prefix, path ...uint32) Change {
	return Change{Peer: peer, Prefix: p, Attrs: Attrs{AsPath: path}}
}

func wd(peer uint32, p Prefix) Change { return Change{Peer: peer, Prefix: p, Withdraw: true} }

func exportOf(t *testing.T, e *Engine, peer uint32) map[Prefix]Attrs {
	t.Helper()
	entries, err := e.Export(peer)
	if err != nil {
		t.Fatalf("Export(%d): %v", peer, err)
	}
	out := make(map[Prefix]Attrs, len(entries))
	for _, en := range entries {
		out[en.Prefix] = en.Attrs
	}
	return out
}

// 分组比较与两两比较结果不同；非最优路由撤销改变最优（规格示例）。
func TestGroupVsPairwise(t *testing.T) {
	e := New(65000, 100)
	addPeer(t, e, 1, 65001, 1)
	addPeer(t, e, 2, 65002, 2)
	addPeer(t, e, 3, 65001, 3)
	addPeer(t, e, 4, 65003, 4)
	p := pfx(0x0A000000, 8)

	c, err := e.Update(1, p, Attrs{AsPath: []uint32{65001, 7}, Med: 100})
	if err != nil {
		t.Fatal(err)
	}
	assertChanges(t, "update1", c, []Change{
		adv(2, p, 65000, 65001, 7), adv(3, p, 65000, 65001, 7), adv(4, p, 65000, 65001, 7),
	})

	// 邻居 2 到来后最优仍为邻居 1（组代表 routerID 1 < 2）。
	c, err = e.Update(2, p, Attrs{AsPath: []uint32{65002, 7}, Med: 50})
	if err != nil {
		t.Fatal(err)
	}
	assertChanges(t, "update2", c, nil)

	// 邻居 3 以 med 10 夺得 65001 组代表，最优变为邻居 2（routerID 2 < 3）。
	// 逐条两两比较会误得邻居 3。
	c, err = e.Update(3, p, Attrs{AsPath: []uint32{65001, 9}, Med: 10})
	if err != nil {
		t.Fatal(err)
	}
	assertChanges(t, "update3", c, []Change{
		adv(1, p, 65000, 65002, 7), wd(2, p), adv(3, p, 65000, 65002, 7), adv(4, p, 65000, 65002, 7),
	})
	got := exportOf(t, e, 4)
	if !policy.EqualAttrs(got[p], Attrs{AsPath: []uint32{65000, 65002, 7}}) {
		t.Fatalf("best should be peer2, export=%+v", got[p])
	}

	// 撤销并非最优的邻居 3：65001 组代表变回邻居 1，最优由 2 变为 1。
	c, err = e.Withdraw(3, p)
	if err != nil {
		t.Fatal(err)
	}
	assertChanges(t, "withdraw3", c, []Change{
		wd(1, p), adv(2, p, 65000, 65001, 7), adv(3, p, 65000, 65001, 7), adv(4, p, 65000, 65001, 7),
	})
}

// 策略链示例：隐式撤销、next 修改、Pmax 计入被拒路由（规格示例）。
func TestPolicyChainImplicitWithdraw(t *testing.T) {
	e := New(65000, 2)
	if err := e.AddPeer(1, 65001, 1); err != nil {
		t.Fatal(err)
	}
	lp200 := uint32(200)
	terms := []Term{
		{Action: ActionReject, Match: Match{Community: &[]uint32{0x00010029}[0]}},
		{Action: ActionNext, Match: Match{Prefix: &PrefixCond{Addr: 0x0A000000, Len: 8, Ge: 8, Le: 24}}, Mods: Mods{LocalPref: &lp200}},
		{Action: ActionAccept},
	}
	if _, err := e.SetPolicy(1, terms); err != nil {
		t.Fatal(err)
	}
	addPeer(t, e, 2, 65000, 2) // 内部观察者，属性不变可看到 localPref

	p16 := pfx(0x0A010000, 16) // 10.1.0.0/16
	p25 := pfx(0x0A010180, 25) // 10.1.1.128/25

	// 命中条款②：next + localPref=200。
	if _, err := e.Update(1, p16, Attrs{AsPath: []uint32{65001}}); err != nil {
		t.Fatal(err)
	}
	got := exportOf(t, e, 2)
	if !policy.EqualAttrs(got[p16], Attrs{AsPath: []uint32{65001}, LocalPref: 200}) {
		t.Fatalf("expected localPref 200, got %+v", got[p16])
	}

	// 同前缀再 Update 带团体 0x00010029 被拒：策略后路由消失（隐式撤销），
	// 其他邻居收到 Withdraw，但原始表仍占 1 条。
	c, err := e.Update(1, p16, Attrs{AsPath: []uint32{65001}, Communities: []uint32{0x00010029}})
	if err != nil {
		t.Fatal(err)
	}
	assertChanges(t, "implicit withdraw", c, []Change{wd(2, p16)})
	if got := exportOf(t, e, 2); len(got) != 0 {
		t.Fatalf("expected empty export, got %+v", got)
	}

	// /25 不中条款②（25 > 24），以 localPref 100 接受。
	if _, err := e.Update(1, p25, Attrs{AsPath: []uint32{65001}}); err != nil {
		t.Fatal(err)
	}
	got = exportOf(t, e, 2)
	if !policy.EqualAttrs(got[p25], Attrs{AsPath: []uint32{65001}, LocalPref: 100}) {
		t.Fatalf("expected localPref 100, got %+v", got[p25])
	}

	// 原始表已有 2 条（被拒的也计入），新前缀超限。
	if _, err := e.Update(1, pfx(0x0A030000, 16), Attrs{AsPath: []uint32{65001}}); !errors.Is(err, ErrPrefixLimit) {
		t.Fatalf("expected ErrPrefixLimit, got %v", err)
	}

	// 覆盖已有前缀不受限：去掉团体后重新被接受。
	c, err = e.Update(1, p16, Attrs{AsPath: []uint32{65001}})
	if err != nil {
		t.Fatal(err)
	}
	assertChanges(t, "re-accept", c, []Change{
		{Peer: 2, Prefix: p16, Attrs: Attrs{AsPath: []uint32{65001}, LocalPref: 200}},
	})
}

// 环路路由无策略后路由但计入 Pmax。
func TestLoopCountsPmax(t *testing.T) {
	e := New(65000, 1)
	addPeer(t, e, 1, 65001, 1)
	addPeer(t, e, 2, 65002, 2)
	pa, pb := pfx(0x0A000000, 8), pfx(0x0B000000, 8)

	// asPath 含 localAS 判环路：Update 仍被接受，但无导出。
	c, err := e.Update(1, pa, Attrs{AsPath: []uint32{65000, 7}})
	if err != nil {
		t.Fatal(err)
	}
	assertChanges(t, "loop update", c, nil)
	if got := exportOf(t, e, 2); len(got) != 0 {
		t.Fatalf("loop route should not be exported: %+v", got)
	}

	// 环路路由计入 Pmax：新前缀超限。
	if _, err := e.Update(1, pb, Attrs{AsPath: []uint32{7}}); !errors.Is(err, ErrPrefixLimit) {
		t.Fatalf("expected ErrPrefixLimit, got %v", err)
	}

	// 撤销环路路由后新前缀可进入。
	if _, err := e.Withdraw(1, pa); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Update(1, pb, Attrs{AsPath: []uint32{7}}); err != nil {
		t.Fatal(err)
	}
}

// SetPolicy 整体替换后用原始表重新求值。
func TestSetPolicyReeval(t *testing.T) {
	e := New(65000, 10)
	if err := e.AddPeer(1, 65001, 1); err != nil {
		t.Fatal(err)
	}
	addPeer(t, e, 2, 65000, 2) // 内部观察者
	p := pfx(0x0A000000, 8)

	// 新邻居策略为空（全部拒绝）：路由进入原始表但无导出。
	c, err := e.Update(1, p, Attrs{AsPath: []uint32{65001}, LocalPref: 50})
	if err != nil {
		t.Fatal(err)
	}
	assertChanges(t, "default deny", c, nil)

	// 换成 accept：外部邻居 localPref 先重置为 100。
	c, err = e.SetPolicy(1, acceptAll())
	if err != nil {
		t.Fatal(err)
	}
	assertChanges(t, "accept", c, []Change{
		{Peer: 2, Prefix: p, Attrs: Attrs{AsPath: []uint32{65001}, LocalPref: 100}},
	})

	// 换成 next 置 localPref=300 再 accept：重算基于原始属性。
	lp300 := uint32(300)
	c, err = e.SetPolicy(1, []Term{
		{Action: ActionNext, Mods: Mods{LocalPref: &lp300}},
		{Action: ActionAccept},
	})
	if err != nil {
		t.Fatal(err)
	}
	assertChanges(t, "next lp300", c, []Change{
		{Peer: 2, Prefix: p, Attrs: Attrs{AsPath: []uint32{65001}, LocalPref: 300}},
	})

	// 换成 reject：导出消失。
	c, err = e.SetPolicy(1, []Term{{Action: ActionReject}})
	if err != nil {
		t.Fatal(err)
	}
	assertChanges(t, "reject", c, []Change{wd(2, p)})
}

// NO_EXPORT 不发给外部邻居，NO_ADVERTISE 不发给任何邻居。
func TestWellKnownCommunities(t *testing.T) {
	e := New(65000, 10)
	addPeer(t, e, 1, 65001, 1) // 外部源
	addPeer(t, e, 2, 65002, 2) // 外部观察
	addPeer(t, e, 3, 65000, 3) // 内部观察
	p1, p2 := pfx(0x0A000000, 8), pfx(0x0B000000, 8)

	if _, err := e.Update(1, p1, Attrs{AsPath: []uint32{65001}, Communities: []uint32{policy.NoExport}}); err != nil {
		t.Fatal(err)
	}
	if got := exportOf(t, e, 2); len(got) != 0 {
		t.Fatalf("NO_EXPORT must not go to external peer: %+v", got)
	}
	if got := exportOf(t, e, 3); len(got) != 1 {
		t.Fatalf("NO_EXPORT should go to internal peer: %+v", got)
	}

	if _, err := e.Update(1, p2, Attrs{AsPath: []uint32{65001}, Communities: []uint32{policy.NoAdvertise}}); err != nil {
		t.Fatal(err)
	}
	if got := exportOf(t, e, 2); len(got) != 0 {
		t.Fatalf("NO_ADVERTISE must not go to external peer: %+v", got)
	}
	if got := exportOf(t, e, 3); len(got) != 1 {
		t.Fatalf("NO_ADVERTISE must not go to internal peer either: %+v", got)
	}
}

// 内部邻居之间不转发；外部路由发内部邻居属性不变。
func TestInternalNoTransit(t *testing.T) {
	e := New(65000, 10)
	addPeer(t, e, 1, 65000, 1) // 内部源
	addPeer(t, e, 2, 65000, 2) // 内部观察
	addPeer(t, e, 3, 65003, 3) // 外部观察
	p, q := pfx(0x0A000000, 8), pfx(0x0B000000, 8)

	if _, err := e.Update(1, p, Attrs{AsPath: []uint32{65001}, LocalPref: 150, Med: 30}); err != nil {
		t.Fatal(err)
	}
	if got := exportOf(t, e, 2); len(got) != 0 {
		t.Fatalf("i->i must not be forwarded: %+v", got)
	}
	got := exportOf(t, e, 3)
	if !policy.EqualAttrs(got[p], Attrs{AsPath: []uint32{65000, 65001}}) {
		t.Fatalf("external export should prepend localAS and zero lp/med: %+v", got[p])
	}

	// 外部路由发内部邻居：属性不变（localPref 为导入时重置的 100）。
	if _, err := e.Update(3, q, Attrs{AsPath: []uint32{65003}, Med: 20}); err != nil {
		t.Fatal(err)
	}
	got = exportOf(t, e, 2)
	if !policy.EqualAttrs(got[q], Attrs{AsPath: []uint32{65003}, LocalPref: 100, Med: 20}) {
		t.Fatalf("internal export should keep attrs: %+v", got[q])
	}
}

// 逐字段相同不发差量。
func TestIdenticalNoDiff(t *testing.T) {
	e := New(65000, 10)
	addPeer(t, e, 1, 65001, 1)
	addPeer(t, e, 2, 65000, 2) // 内部观察者：med 变化可见（外部导出 med 置 0）
	p := pfx(0x0A000000, 8)
	a1 := Attrs{AsPath: []uint32{65001, 7}, Med: 10, Communities: []uint32{42}}

	c, err := e.Update(1, p, a1)
	if err != nil {
		t.Fatal(err)
	}
	if len(c) != 1 {
		t.Fatalf("first update should advertise, got %+v", c)
	}
	c, err = e.Update(1, p, a1)
	if err != nil {
		t.Fatal(err)
	}
	assertChanges(t, "identical update", c, nil)

	a2 := Attrs{AsPath: []uint32{65001, 7}, Med: 20, Communities: []uint32{42}}
	c, err = e.Update(1, p, a2)
	if err != nil {
		t.Fatal(err)
	}
	if len(c) != 1 {
		t.Fatalf("changed med should advertise, got %+v", c)
	}
	c, err = e.Update(1, p, a2)
	if err != nil {
		t.Fatal(err)
	}
	assertChanges(t, "identical update again", c, nil)
}

// 拒绝次序：参数非法 > 邻居不存在或已存在 > 路由不存在 > 前缀超限。
func TestRejectOrder(t *testing.T) {
	badPrefix := pfx(0x0A000001, 8) // 主机位非零
	goodPrefix := pfx(0x0A000000, 8)
	badAttrs := Attrs{Communities: []uint32{7, 7}}
	goodAttrs := Attrs{AsPath: []uint32{65001}}
	badTerms := []Term{{Action: Action(9)}}

	cases := []struct {
		name string
		run  func(e *Engine) error
		want error
	}{
		{"update invalid param beats unknown peer", func(e *Engine) error {
			_, err := e.Update(99, badPrefix, goodAttrs)
			return err
		}, ErrInvalidParam},
		{"update invalid attrs beats unknown peer", func(e *Engine) error {
			_, err := e.Update(99, goodPrefix, badAttrs)
			return err
		}, ErrInvalidParam},
		{"update unknown peer", func(e *Engine) error {
			_, err := e.Update(99, goodPrefix, goodAttrs)
			return err
		}, ErrPeerNotFound},
		{"withdraw unknown peer beats missing route", func(e *Engine) error {
			_, err := e.Withdraw(99, goodPrefix)
			return err
		}, ErrPeerNotFound},
		{"withdraw missing route", func(e *Engine) error {
			_, err := e.Withdraw(1, goodPrefix)
			return err
		}, ErrRouteNotFound},
		{"setpolicy invalid terms beats unknown peer", func(e *Engine) error {
			_, err := e.SetPolicy(99, badTerms)
			return err
		}, ErrInvalidParam},
		{"setpolicy unknown peer", func(e *Engine) error {
			_, err := e.SetPolicy(99, acceptAll())
			return err
		}, ErrPeerNotFound},
		{"addpeer invalid id", func(e *Engine) error {
			return e.AddPeer(0, 65001, 1)
		}, ErrInvalidParam},
		{"addpeer id too large", func(e *Engine) error {
			return e.AddPeer(1000001, 65001, 1)
		}, ErrInvalidParam},
		{"addpeer zero router id", func(e *Engine) error {
			return e.AddPeer(7, 65001, 0)
		}, ErrInvalidParam},
		{"addpeer invalid param beats duplicate", func(e *Engine) error {
			return e.AddPeer(1, 65001, 0)
		}, ErrInvalidParam},
		{"addpeer duplicate", func(e *Engine) error {
			return e.AddPeer(1, 65001, 9)
		}, ErrPeerExists},
		{"export unknown peer", func(e *Engine) error {
			_, err := e.Export(99)
			return err
		}, ErrPeerNotFound},
	}
	for _, c := range cases {
		e := New(65000, 1)
		if err := e.AddPeer(1, 65001, 1); err != nil {
			t.Fatal(err)
		}
		if err := c.run(e); !errors.Is(err, c.want) {
			t.Errorf("%s: err=%v want %v", c.name, err, c.want)
		}
	}

	// 前缀超限最后判定，且被拒绝的操作不改任何状态。
	e := New(65000, 1)
	addPeer(t, e, 1, 65001, 1)
	addPeer(t, e, 2, 65002, 2)
	if _, err := e.Update(1, goodPrefix, goodAttrs); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Update(1, pfx(0x0B000000, 8), goodAttrs); !errors.Is(err, ErrPrefixLimit) {
		t.Fatalf("expected ErrPrefixLimit")
	}
	if got := exportOf(t, e, 2); len(got) != 1 {
		t.Fatalf("rejected op must not change state: %+v", got)
	}
	if _, err := e.Withdraw(1, pfx(0x0B000000, 8)); !errors.Is(err, ErrRouteNotFound) {
		t.Fatalf("rejected prefix must not be in raw table: %v", err)
	}
}

// compared 与前缀总数无关：100 与 10000 前缀两档对照。
func TestComparedBound(t *testing.T) {
	for _, n := range []int{100, 10000} {
		e := New(65000, n+10)
		addPeer(t, e, 1, 65001, 1)
		addPeer(t, e, 2, 65002, 2)
		prefixAt := func(i int) Prefix {
			return pfx(0x0A000000|uint32(i)<<8, 24)
		}
		for i := 0; i < n; i++ {
			if _, err := e.Update(1, prefixAt(i), Attrs{AsPath: []uint32{65001}}); err != nil {
				t.Fatal(err)
			}
		}
		x := prefixAt(0)
		if _, err := e.Update(2, x, Attrs{AsPath: []uint32{65002}}); err != nil {
			t.Fatal(err)
		}

		// 两个邻居持有 x：考察条数 <= 2+1。
		before := e.Compared()
		if _, err := e.Update(2, x, Attrs{AsPath: []uint32{65002}, Med: 5}); err != nil {
			t.Fatal(err)
		}
		delta := e.Compared() - before
		if delta > 3 {
			t.Fatalf("n=%d: update compared %d routes, want <= holders(2)+1", n, delta)
		}

		// 撤销后仅邻居 1 持有：考察条数 <= 1+1。
		before = e.Compared()
		if _, err := e.Withdraw(2, x); err != nil {
			t.Fatal(err)
		}
		delta = e.Compared() - before
		if delta > 2 {
			t.Fatalf("n=%d: withdraw compared %d routes, want <= holders(1)+1", n, delta)
		}
		t.Logf("n=%d prefixes: update delta<=3, withdraw delta<=2 (actual %d)", n, delta)
	}
}

// Export 与差量清单的排序。
func TestExportAndChangeOrder(t *testing.T) {
	e := New(65000, 100)
	addPeer(t, e, 1, 65001, 1)
	addPeer(t, e, 2, 65000, 2) // 内部观察者：localPref 变化可见（外部导出 lp 置 0）
	addPeer(t, e, 3, 65000, 3)
	ps := []Prefix{pfx(0xC0000200, 24), pfx(0x0A000000, 8), pfx(0x0A010000, 16)}
	for _, p := range ps {
		if _, err := e.Update(1, p, Attrs{AsPath: []uint32{65001}}); err != nil {
			t.Fatal(err)
		}
	}
	entries, err := e.Export(2)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 3 {
		t.Fatalf("want 3 entries, got %d", len(entries))
	}
	for i := 1; i < len(entries); i++ {
		a, b := entries[i-1].Prefix, entries[i].Prefix
		if a.Addr > b.Addr || (a.Addr == b.Addr && a.Len >= b.Len) {
			t.Fatalf("Export not sorted: %+v", entries)
		}
	}

	// SetPolicy 触发多条差量：按（邻居 id，前缀地址，前缀长度）升序。
	lp := uint32(300)
	c, err := e.SetPolicy(1, []Term{
		{Action: ActionNext, Mods: Mods{LocalPref: &lp}},
		{Action: ActionAccept},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(c) != 6 {
		t.Fatalf("want 6 changes, got %+v", c)
	}
	for i := 1; i < len(c); i++ {
		a, b := c[i-1], c[i]
		less := a.Peer < b.Peer ||
			(a.Peer == b.Peer && a.Prefix.Addr < b.Prefix.Addr) ||
			(a.Peer == b.Peer && a.Prefix.Addr == b.Prefix.Addr && a.Prefix.Len < b.Prefix.Len)
		if !less {
			t.Fatalf("changes not sorted: %+v", c)
		}
	}
}

// 并发调用：不相交前缀的操作可交换，最终结果与串行一致（配合 -race）。
func TestConcurrent(t *testing.T) {
	const sources = 8
	const perPeer = 20
	e := New(65000, 1000)
	for i := 0; i < sources; i++ {
		addPeer(t, e, uint32(i+1), uint32(65100+i), uint32(i+1))
	}
	addPeer(t, e, 100, 65900, 100) // 观察者

	var wg sync.WaitGroup
	for i := 0; i < sources; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			id := uint32(i + 1)
			for j := 0; j < perPeer; j++ {
				p := pfx(0x0A000000|uint32(i+1)<<16|uint32(j)<<8, 24)
				if _, err := e.Update(id, p, Attrs{AsPath: []uint32{uint32(65100 + i)}}); err != nil {
					t.Error(err)
				}
			}
		}(i)
	}
	wg.Wait()

	got := exportOf(t, e, 100)
	if len(got) != sources*perPeer {
		t.Fatalf("observer export entries=%d want %d", len(got), sources*perPeer)
	}
	for i := 0; i < sources; i++ {
		for j := 0; j < perPeer; j++ {
			p := pfx(0x0A000000|uint32(i+1)<<16|uint32(j)<<8, 24)
			want := Attrs{AsPath: []uint32{65000, uint32(65100 + i)}}
			if !policy.EqualAttrs(got[p], want) {
				t.Fatalf("prefix %v: got %+v want %+v", p, got[p], want)
			}
		}
	}
}

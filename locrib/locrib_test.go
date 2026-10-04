package locrib

import (
	"errors"
	"testing"

	"ontology/policy"
)

func ip(a, b, c, d byte) uint32 {
	return uint32(a)<<24 | uint32(b)<<16 | uint32(c)<<8 | uint32(d)
}

func pf(addr uint32, l uint8) policy.Prefix { return policy.Prefix{Addr: addr, Len: l} }

func acceptAll() []policy.Term {
	return []policy.Term{{Action: policy.Action{Kind: policy.ActionAccept}}}
}

func mustAdd(t *testing.T, e *Engine, id int, as, rid uint32) {
	t.Helper()
	if err := e.AddPeer(id, as, rid); err != nil {
		t.Fatalf("AddPeer(%d): %v", id, err)
	}
}

func rattrs(aspath []uint32, lp, med uint32, origin uint8, comms ...uint32) policy.Attrs {
	return policy.Attrs{ASPath: aspath, LocalPref: lp, MED: med, Origin: origin, Communities: comms}
}

func bestPeerOf(t *testing.T, e *Engine, p policy.Prefix) int {
	t.Helper()
	best := selectBest(e.tab.PostRoutes(p), nil)
	if best == nil {
		return -1
	}
	return best.PeerID
}

// TestGroupSelection 复现题目反例：分组结果 ≠ 两两比较；非最优撤销改变最优。
func TestGroupSelection(t *testing.T) {
	e := New(65000, 100)
	mustAdd(t, e, 1, 65001, 1)
	mustAdd(t, e, 2, 65002, 2)
	mustAdd(t, e, 3, 65001, 3)
	for _, id := range []int{1, 2, 3} {
		if ch, err := e.SetPolicy(id, acceptAll()); err != nil || len(ch) != 0 {
			t.Fatalf("SetPolicy %d: ch=%v err=%v", id, ch, err)
		}
	}
	p := pf(ip(10, 0, 0, 0), 24)
	upd := func(id int, a policy.Attrs) {
		t.Helper()
		if _, err := e.Update(id, p, a); err != nil {
			t.Fatalf("Update %d: %v", id, err)
		}
	}
	upd(1, rattrs([]uint32{65001, 7}, 100, 100, 0))
	upd(2, rattrs([]uint32{65002, 7}, 100, 50, 0))
	upd(3, rattrs([]uint32{65001, 9}, 100, 10, 0))

	if got := bestPeerOf(t, e, p); got != 2 {
		t.Fatalf("best must be peer 2 (group-aware), got %d", got)
	}

	ch, err := e.Withdraw(3, p)
	if err != nil {
		t.Fatal(err)
	}
	if got := bestPeerOf(t, e, p); got != 1 {
		t.Fatalf("after non-best withdraw best must be peer 1, got %d", got)
	}
	for _, c := range ch {
		if c.Prefix != p {
			continue
		}
		if c.Peer == 1 {
			if c.Kind != ChangeWithdraw {
				t.Fatalf("peer %d must get withdraw, got %+v", c.Peer, c)
			}
			continue
		}
		// 邻居 3 原为来源（此前不收），最优切到邻居 1 后反而新收到 → Advertise。
		if c.Kind != ChangeAdvertise {
			t.Fatalf("other peers must re-advertise after switch, got %+v", c)
		}
	}
}

// TestImplicitWithdrawAndPolicy 题目策略再例 + 隐式撤销 + 环路计数。
func TestImplicitWithdrawAndPolicy(t *testing.T) {
	e := New(65000, 100)
	mustAdd(t, e, 1, 65001, 1)
	mustAdd(t, e, 2, 65000, 2)
	p16 := pf(ip(10, 1, 0, 0), 16)
	p25 := pf(ip(10, 1, 1, 0), 25)
	terms := []policy.Term{
		{Match: policy.Match{HasCommunity: true, Community: 0x00010029},
			Action: policy.Action{Kind: policy.ActionReject}},
		{Match: policy.Match{HasPrefix: true, Prefix: pf(ip(10, 0, 0, 0), 8), GE: 8, LE: 24},
			Action: policy.Action{Kind: policy.ActionNext, SetLocalPref: true, LocalPref: 200}},
		{Action: policy.Action{Kind: policy.ActionAccept}},
	}
	if _, err := e.SetPolicy(1, terms); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Update(1, p16, rattrs([]uint32{7}, 0, 0, 0)); err != nil {
		t.Fatal(err)
	}
	if r := e.tab.PostRoutes(p16); len(r) != 1 || r[0].Attrs.LocalPref != 200 {
		t.Fatalf("want localpref 200, got %+v", r)
	}

	ch, err := e.Update(1, p16, rattrs([]uint32{7}, 0, 0, 0, 0x00010029))
	if err != nil {
		t.Fatal(err)
	}
	if len(e.tab.PostRoutes(p16)) != 0 {
		t.Fatal("post route must disappear on reject")
	}
	if !e.tab.RawHas(1, p16) || e.tab.RawCount(1) != 1 {
		t.Fatal("raw entry remains and counts toward Pmax")
	}
	foundWd := false
	for _, c := range ch {
		if c.Peer == 2 && c.Prefix == p16 && c.Kind == ChangeWithdraw {
			foundWd = true
		}
	}
	if !foundWd {
		t.Fatalf("peer 2 must receive withdraw, got %+v", ch)
	}

	if _, err := e.Update(1, p25, rattrs([]uint32{7}, 0, 0, 0)); err != nil {
		t.Fatal(err)
	}
	if r := e.tab.PostRoutes(p25); len(r) != 1 || r[0].Attrs.LocalPref != 100 {
		t.Fatalf("/25 localpref 100, got %+v", r)
	}

	p32 := pf(ip(8, 8, 8, 8), 32)
	if _, err := e.Update(1, p32, rattrs([]uint32{65000, 1}, 0, 0, 0)); err != nil {
		t.Fatal(err)
	}
	if len(e.tab.PostRoutes(p32)) != 0 || !e.tab.RawHas(1, p32) {
		t.Fatal("loop rejected at policy but raw stays")
	}
}

// TestPmax 容量：被拒/环路计入；覆盖不受限；满时报错；撤销释放。
func TestPmax(t *testing.T) {
	e := New(65000, 2)
	mustAdd(t, e, 1, 65001, 1)
	rejectComm := []policy.Term{{
		Match:  policy.Match{HasCommunity: true, Community: 1},
		Action: policy.Action{Kind: policy.ActionReject},
	}}
	if _, err := e.SetPolicy(1, rejectComm); err != nil {
		t.Fatal(err)
	}
	p1 := pf(ip(1, 0, 0, 0), 24)
	p2 := pf(ip(2, 0, 0, 0), 24)
	p3 := pf(ip(3, 0, 0, 0), 24)
	if _, err := e.Update(1, p1, rattrs(nil, 0, 0, 0, 1)); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Update(1, p2, rattrs(nil, 0, 0, 0, 1)); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Update(1, p3, rattrs(nil, 0, 0, 0, 1)); !errors.Is(err, policy.ErrPrefixLimit) {
		t.Fatalf("third new prefix must exceed Pmax: %v", err)
	}
	if _, err := e.Update(1, p1, rattrs(nil, 0, 0, 0, 1)); err != nil {
		t.Fatal("overwrite must not hit limit")
	}
	if _, err := e.Withdraw(1, p1); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Update(1, p3, rattrs(nil, 0, 0, 0, 1)); err != nil {
		t.Fatalf("after withdraw slot frees: %v", err)
	}
}

// TestErrorOrder 拒绝次序：参数非法 > 邻居已存在/不存在 > 路由不存在 > 超限。
func TestErrorOrder(t *testing.T) {
	e := New(65000, 0)
	if err := e.AddPeer(0, 65001, 1); !errors.Is(err, policy.ErrInvalidArgument) {
		t.Fatalf("bad id: %v", err)
	}
	if err := e.AddPeer(1, 65001, 0); !errors.Is(err, policy.ErrInvalidArgument) {
		t.Fatalf("zero routerid: %v", err)
	}
	mustAdd(t, e, 1, 65001, 1)
	if err := e.AddPeer(1, 65002, 2); !errors.Is(err, policy.ErrPeerExists) {
		t.Fatalf("dup peer: %v", err)
	}
	p := pf(ip(1, 0, 0, 0), 24)
	if _, err := e.Update(99, pf(1, 8), rattrs(nil, 0, 0, 0)); !errors.Is(err, policy.ErrInvalidArgument) {
		t.Fatalf("invalid arg precedes missing peer: %v", err)
	}
	if _, err := e.Withdraw(99, pf(1, 8)); !errors.Is(err, policy.ErrInvalidArgument) {
		t.Fatalf("withdraw invalid arg precedes missing peer: %v", err)
	}
	if _, err := e.Withdraw(99, p); !errors.Is(err, policy.ErrPeerNotFound) {
		t.Fatalf("missing peer: %v", err)
	}
	if _, err := e.Update(99, p, rattrs(nil, 0, 0, 0)); !errors.Is(err, policy.ErrPeerNotFound) {
		t.Fatalf("update missing peer: %v", err)
	}
	if _, err := e.Withdraw(1, p); !errors.Is(err, policy.ErrRouteNotFound) {
		t.Fatalf("route not found precedes limit: %v", err)
	}
	if _, err := e.Update(1, p, rattrs(nil, 0, 0, 0)); !errors.Is(err, policy.ErrPrefixLimit) {
		t.Fatalf("limit: %v", err)
	}
	if _, err := e.SetPolicy(99, []policy.Term{{Action: policy.Action{Kind: 77}}}); !errors.Is(err, policy.ErrInvalidArgument) {
		t.Fatalf("setpolicy arg order: %v", err)
	}
	if _, err := e.SetPolicy(99, acceptAll()); !errors.Is(err, policy.ErrPeerNotFound) {
		t.Fatalf("setpolicy missing peer: %v", err)
	}
}

// TestExportRules 团体与内外邻接导出规则，含逐字段相同不发差量。
func TestExportRules(t *testing.T) {
	e := New(65000, 100)
	mustAdd(t, e, 1, 65001, 1)
	mustAdd(t, e, 2, 65000, 2)
	mustAdd(t, e, 3, 65000, 3)
	mustAdd(t, e, 4, 65002, 4)
	for _, id := range []int{1, 2, 3, 4} {
		if _, err := e.SetPolicy(id, acceptAll()); err != nil {
			t.Fatal(err)
		}
	}
	p := pf(ip(10, 0, 0, 0), 24)
	base := []uint32{65001, 7}

	ch, err := e.Update(1, p, rattrs(base, 100, 50, 1, 99))
	if err != nil {
		t.Fatal(err)
	}
	adv := map[int]policy.Attrs{}
	for _, c := range ch {
		if c.Kind == ChangeAdvertise {
			adv[c.Peer] = c.Attrs
		}
	}
	if _, ok := adv[1]; ok {
		t.Fatal("source peer must not receive")
	}
	for _, id := range []int{2, 3} {
		a, ok := adv[id]
		if !ok {
			t.Fatalf("internal peer %d should receive eBGP route", id)
		}
		if len(a.ASPath) != 2 || a.LocalPref != 100 || a.MED != 50 {
			t.Fatalf("internal export keeps attrs: %+v", a)
		}
	}
	a4, ok := adv[4]
	if !ok || len(a4.ASPath) != 3 || a4.ASPath[0] != 65000 || a4.LocalPref != 0 || a4.MED != 0 {
		t.Fatalf("external export rewrite: %+v ok=%v", a4, ok)
	}

	// 同属性覆盖：差量为空。
	ch, err = e.Update(1, p, rattrs(append([]uint32{}, base...), 100, 50, 1, 99))
	if err != nil {
		t.Fatal(err)
	}
	if len(ch) != 0 {
		t.Fatalf("identical update yields no delta: %+v", ch)
	}

	// NO_ADVERTISE：谁都不收；此前收到的应得到 withdraw。
	ch, err = e.Update(1, p, rattrs(base, 100, 50, 1, policy.NoAdvertise))
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range ch {
		if c.Kind != ChangeWithdraw {
			t.Fatalf("NO_ADVERTISE must withdraw everyone, got %+v", c)
		}
	}
	for _, id := range []int{2, 3, 4} {
		if got := e.Export(id); len(got) != 0 {
			t.Fatalf("peer %d export must be empty under NO_ADVERTISE: %+v", id, got)
		}
	}

	// NO_EXPORT：内部收，外部不收。
	if _, err := e.Update(1, p, rattrs(base, 100, 50, 1, policy.NoExport)); err != nil {
		t.Fatal(err)
	}
	if got := e.Export(4); len(got) != 0 {
		t.Fatalf("external peer must not get NO_EXPORT: %+v", got)
	}
	if got := e.Export(2); len(got) != 1 {
		t.Fatalf("internal peer keeps NO_EXPORT route: %+v", got)
	}

	// 无特殊团体恢复后，外部重新收到。
	if _, err := e.Update(1, p, rattrs(base, 100, 50, 1)); err != nil {
		t.Fatal(err)
	}
	if got := e.Export(4); len(got) != 1 {
		t.Fatalf("external peer re-advertised: %+v", got)
	}
}

// TestIBGPNoForward 内部邻居之间不转发其学到的路由。
func TestIBGPNoForward(t *testing.T) {
	e := New(65000, 100)
	mustAdd(t, e, 1, 65000, 1)
	mustAdd(t, e, 2, 65000, 2)
	mustAdd(t, e, 3, 65003, 3)
	for _, id := range []int{1, 2, 3} {
		if _, err := e.SetPolicy(id, acceptAll()); err != nil {
			t.Fatal(err)
		}
	}
	p := pf(ip(10, 0, 0, 0), 24)
	ch, err := e.Update(1, p, rattrs([]uint32{100, 200}, 200, 5, 0))
	if err != nil {
		t.Fatal(err)
	}
	recipients := map[int]bool{}
	for _, c := range ch {
		recipients[c.Peer] = true
	}
	if recipients[2] {
		t.Fatal("iBGP-learned route must not be forwarded to another internal peer")
	}
	if !recipients[3] {
		t.Fatal("external peer should receive iBGP-learned route")
	}
}

// TestSetPolicyReevaluate SetPolicy 替换后用原始表重算。
func TestSetPolicyReevaluate(t *testing.T) {
	e := New(65000, 100)
	mustAdd(t, e, 1, 65001, 1)
	mustAdd(t, e, 2, 65002, 2)
	if _, err := e.SetPolicy(2, acceptAll()); err != nil {
		t.Fatal(err)
	}
	p := pf(ip(10, 0, 0, 0), 24)
	// 新邻居策略为空（全部拒绝）。
	if _, err := e.Update(1, p, rattrs([]uint32{7}, 0, 0, 0)); err != nil {
		t.Fatal(err)
	}
	if len(e.tab.PostRoutes(p)) != 0 {
		t.Fatal("default deny for new peer")
	}
	if got := e.Export(2); len(got) != 0 {
		t.Fatalf("nothing exported yet: %+v", got)
	}
	// 放开策略 → 重算 → 邻居 2 收到通告。
	ch, err := e.SetPolicy(1, acceptAll())
	if err != nil {
		t.Fatal(err)
	}
	if len(e.tab.PostRoutes(p)) != 1 {
		t.Fatal("reevaluate must produce post route")
	}
	found := false
	for _, c := range ch {
		if c.Peer == 2 && c.Prefix == p && c.Kind == ChangeAdvertise {
			found = true
		}
	}
	if !found {
		t.Fatalf("SetPolicy must emit advertise delta: %+v", ch)
	}
	// 再次拒绝 → 隐式撤销差量。
	ch, err = e.SetPolicy(1, []policy.Term{{Action: policy.Action{Kind: policy.ActionReject}}})
	if err != nil {
		t.Fatal(err)
	}
	found = false
	for _, c := range ch {
		if c.Peer == 2 && c.Prefix == p && c.Kind == ChangeWithdraw {
			found = true
		}
	}
	if !found {
		t.Fatalf("policy reject must emit withdraw: %+v", ch)
	}
}

// TestDeltaEqualsExport 累计应用全部差量恒等于 Export 全量。
func TestDeltaEqualsExport(t *testing.T) {
	e := New(65000, 10)
	for i, as := range []uint32{65001, 65002, 65000, 65003} {
		id := i + 1
		mustAdd(t, e, id, as, uint32(10+i))
		if _, err := e.SetPolicy(id, acceptAll()); err != nil {
			t.Fatal(err)
		}
	}
	state := map[key]policy.Attrs{}
	var ops int
	apply := func(ch []Change) {
		for _, c := range ch {
			k := key{c.Peer, c.Prefix}
			switch c.Kind {
			case ChangeAdvertise:
				state[k] = c.Attrs
			case ChangeWithdraw:
				delete(state, k)
			}
		}
	}
	allPrefixes := []policy.Prefix{
		pf(ip(10, 0, 0, 0), 24), pf(ip(10, 1, 0, 0), 24),
		pf(ip(10, 2, 0, 0), 24), pf(ip(172, 16, 0, 0), 16),
	}
	for _, p := range allPrefixes {
		for _, id := range []int{1, 2, 3} {
			ch, err := e.Update(id, p, rattrs([]uint32{uint32(70000 + id)}, 100, uint32(id*7), uint8(id%3), policy.NoExport))
			if err != nil {
				t.Fatal(err)
			}
			apply(ch)
			ops++
		}
	}
	ch, err := e.Update(2, allPrefixes[0], rattrs([]uint32{70002}, 150, 1, 0))
	if err != nil {
		t.Fatal(err)
	}
	apply(ch)
	ch, err = e.Withdraw(1, allPrefixes[1])
	if err != nil {
		t.Fatal(err)
	}
	apply(ch)

	full := map[key]policy.Attrs{}
	for _, id := range e.tab.PeerIDs() {
		for _, c := range e.Export(id) {
			full[key{c.Peer, c.Prefix}] = c.Attrs
		}
	}
	if len(state) != len(full) {
		t.Fatalf("delta-accumulated %d != export %d after %d ops", len(state), len(full), ops)
	}
	for k, a := range state {
		b, ok := full[k]
		if !ok || !policy.AttrsEqual(a, b) {
			t.Fatalf("mismatch at %+v: delta=%+v export=%+v ok=%v", k, a, b, ok)
		}
	}
}

type key struct {
	peer int
	p    policy.Prefix
}

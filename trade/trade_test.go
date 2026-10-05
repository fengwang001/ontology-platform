package trade

import (
	"fmt"
	"math/rand"
	"sync"
	"sync/atomic"
	"testing"
)

const noErr = ErrCode(-1)

func code(t *testing.T, err error) ErrCode {
	t.Helper()
	if err == nil {
		return noErr
	}
	c, ok := CodeOf(err)
	if !ok {
		t.Fatalf("error %v is not a trade.Error", err)
	}
	return c
}

func wantCode(t *testing.T, what string, err error, want ErrCode) {
	t.Helper()
	got := code(t, err)
	if got != want {
		t.Fatalf("%s: got %v, want %s", what, err, want)
	}
	t.Logf("%s -> %v（符合预期 %s）", what, err, want)
}

func wantErrPlayer(t *testing.T, what string, err error, want ErrCode, player string) {
	t.Helper()
	wantCode(t, what, err, want)
	ce := err.(*Error)
	if ce.Player != player {
		t.Fatalf("%s: error player = %q, want %q", what, ce.Player, player)
	}
	t.Logf("%s -> 违规主体 %s（符合预期）", what, player)
}

// openWith 注册两名玩家并开启一个会话。
func openWith(t *testing.T, sys *System, now int64, a, b string) int64 {
	t.Helper()
	sid, err := sys.Open(now, a, b)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	return sid
}

func mustgrant(t *testing.T, sys *System, player, item string, qty int64) {
	t.Helper()
	if err := sys.Grant(player, item, qty); err != nil {
		t.Fatalf("grant(%s,%s,%d): %v", player, item, qty, err)
	}
}

func mustGrantGold(t *testing.T, sys *System, player string, g int64) {
	t.Helper()
	if err := sys.GrantGold(player, g); err != nil {
		t.Fatalf("grantGold(%s,%d): %v", player, g, err)
	}
}

func doOffer(t *testing.T, sys *System, now, sid int64, who string, items []ItemQty, gold int64) {
	t.Helper()
	if err := sys.Offer(now, sid, who, items, gold); err != nil {
		t.Fatalf("offer(%s): %v", who, err)
	}
}

func doConfirm(t *testing.T, sys *System, now, sid int64, who string, ver int64) {
	t.Helper()
	if err := sys.Confirm(now, sid, who, ver); err != nil {
		t.Fatalf("confirm(%s): %v", who, err)
	}
}

// TestTaxRounding 覆盖税的向上取整与 r 为 0 和 1000。
func TestTaxRounding(t *testing.T) {
	cases := []struct {
		r, gold, wantTax int64
	}{
		{50, 101, 6}, // ceil(5.05)=6
		{50, 100, 5},
		{50, 1, 1},
		{50, 0, 0},
		{0, 101, 0},
		{1000, 101, 101},
		{1000, 1, 1},
	}
	for _, tc := range cases {
		t.Run("", func(t *testing.T) {
			sys := New(tc.r, 1_000_000, 4, 1000, 4)
			if err := sys.Grant("a", "sword", 1); err != nil {
				t.Fatal(err)
			}
			if err := sys.GrantGold("b", tc.gold); err != nil {
				t.Fatal(err)
			}
			sid := openWith(t, sys, 0, "a", "b")
			doOffer(t, sys, 0, sid, "a", []ItemQty{{"sword", 1}}, 0)
			doOffer(t, sys, 0, sid, "b", nil, tc.gold)
			doConfirm(t, sys, 0, sid, "a", 2)
			doConfirm(t, sys, 0, sid, "b", 2)
			if got := sys.Gold("a"); got != tc.gold-tc.wantTax {
				t.Fatalf("a gold = %d, want %d", got, tc.gold-tc.wantTax)
			}
			if got := sys.Gold("b"); got != 0 {
				t.Fatalf("b gold = %d, want 0", got)
			}
			if got := sys.Burned(); got != tc.wantTax {
				t.Fatalf("burned = %d, want %d", got, tc.wantTax)
			}
			if got := sys.Holding("b", "sword"); got != 1 {
				t.Fatalf("b sword = %d, want 1", got)
			}
			t.Logf("r=%d gold=%d -> 税 %d，a 实得 %d，burned %d",
				tc.r, tc.gold, tc.wantTax, tc.gold-tc.wantTax, tc.wantTax)
		})
	}
}

// TestCapAndSlotsOrder 覆盖金币上限与格数上限的先后及先 a 后 b。
func TestCapAndSlotsOrder(t *testing.T) {
	// 双方种类数都超限：先报 a。
	t.Run("格数先查a", func(t *testing.T) {
		sys := New(0, 1000, 1, 1000, 4)
		mustgrant(t, sys, "a", "x", 2)
		mustgrant(t, sys, "b", "y", 1)
		sid := openWith(t, sys, 0, "a", "b")
		doOffer(t, sys, 0, sid, "a", []ItemQty{{"x", 1}}, 0)
		doOffer(t, sys, 0, sid, "b", []ItemQty{{"y", 1}}, 0)
		doConfirm(t, sys, 0, sid, "a", 2)
		// a 成交后 x,y 两种；b 成交后 y,x 两种；都超限，先查 a。
		wantErrPlayer(t, "confirm(b)", sys.Confirm(0, sid, "b", 2), ErrSlots, "a")
	})
	// 同一玩家金币与格数都超限：先报金币上限。
	t.Run("金币先于格数", func(t *testing.T) {
		sys := New(0, 100, 1, 1000, 4)
		mustgrant(t, sys, "a", "x", 2)
		mustGrantGold(t, sys, "a", 100)
		mustgrant(t, sys, "b", "y", 1)
		mustGrantGold(t, sys, "b", 60)
		sid := openWith(t, sys, 0, "a", "b")
		doOffer(t, sys, 0, sid, "a", []ItemQty{{"x", 1}}, 0)
		doOffer(t, sys, 0, sid, "b", []ItemQty{{"y", 1}}, 60)
		doConfirm(t, sys, 0, sid, "b", 2)
		// a 成交后金币 160 超 CAP，且种类 x,y 超 Slots：先报金币上限。
		wantErrPlayer(t, "confirm(a)", sys.Confirm(0, sid, "a", 2), ErrGoldCap, "a")
	})
	// 仅 b 金币超限：a 先查但通过，报 b。
	t.Run("金币超限报b", func(t *testing.T) {
		sys := New(0, 100, 4, 1000, 4)
		mustGrantGold(t, sys, "a", 60)
		mustGrantGold(t, sys, "b", 100)
		sid := openWith(t, sys, 0, "a", "b")
		doOffer(t, sys, 0, sid, "a", nil, 60)
		doOffer(t, sys, 0, sid, "b", nil, 0)
		doConfirm(t, sys, 0, sid, "a", 2)
		wantErrPlayer(t, "confirm(b)", sys.Confirm(0, sid, "b", 2), ErrGoldCap, "b")
	})
	// 检查失败时本次确认不记，会话其余状态不变。
	t.Run("检查失败不记确认", func(t *testing.T) {
		sys := New(0, 100, 4, 1000, 4)
		mustGrantGold(t, sys, "a", 60)
		mustGrantGold(t, sys, "b", 100)
		sid := openWith(t, sys, 0, "a", "b")
		doOffer(t, sys, 0, sid, "a", nil, 60)
		doOffer(t, sys, 0, sid, "b", nil, 0)
		doConfirm(t, sys, 0, sid, "a", 2)
		wantCode(t, "confirm(b)", sys.Confirm(0, sid, "b", 2), ErrGoldCap)
		// b 的确认未被记录：b 再确认仍触发成交检查而非报已确认。
		wantCode(t, "confirm(b) again", sys.Confirm(0, sid, "b", 2), ErrGoldCap)
		// b 改报金币 60 使双方都不超限后，成交可继续。
		doOffer(t, sys, 1, sid, "b", nil, 60)
		doConfirm(t, sys, 1, sid, "b", 3)
		doConfirm(t, sys, 1, sid, "a", 3)
		if sys.Gold("b") != 100 {
			t.Fatalf("b gold = %d, want 100", sys.Gold("b"))
		}
	})
}

// TestSlotsDeductThenAdd 覆盖种类数按先扣除付出、再加入收到之后的结果计。
func TestSlotsDeductThenAdd(t *testing.T) {
	// a 持有 x×1 与 y×1，付出 x×1、收到 z×1，成交后为 y、z 两种，通过。
	t.Run("全部付出后种类不增", func(t *testing.T) {
		sys := New(0, 1000, 2, 1000, 4)
		mustgrant(t, sys, "a", "x", 1)
		mustgrant(t, sys, "a", "y", 1)
		mustgrant(t, sys, "b", "z", 1)
		sid := openWith(t, sys, 0, "a", "b")
		doOffer(t, sys, 0, sid, "a", []ItemQty{{"x", 1}}, 0)
		doOffer(t, sys, 0, sid, "b", []ItemQty{{"z", 1}}, 0)
		doConfirm(t, sys, 0, sid, "a", 2)
		doConfirm(t, sys, 0, sid, "b", 2)
		if got := sys.Kinds("a"); got != 2 {
			t.Fatalf("a kinds = %d, want 2", got)
		}
		t.Logf("成交后 a 持有 %v（y,z 两种，通过）", sys.Holdings("a"))
	})
	// a 持有 x×2 只付出 1，成交后为 x、y、z 三种，报格数上限。
	t.Run("部分付出导致种类超限", func(t *testing.T) {
		sys := New(0, 1000, 2, 1000, 4)
		mustgrant(t, sys, "a", "x", 2)
		mustgrant(t, sys, "a", "y", 1)
		mustgrant(t, sys, "b", "z", 1)
		sid := openWith(t, sys, 0, "a", "b")
		doOffer(t, sys, 0, sid, "a", []ItemQty{{"x", 1}}, 0)
		doOffer(t, sys, 0, sid, "b", []ItemQty{{"z", 1}}, 0)
		doConfirm(t, sys, 0, sid, "b", 2)
		wantErrPlayer(t, "confirm(a)", sys.Confirm(0, sid, "a", 2), ErrSlots, "a")
	})
}

// TestVersionBumpClearsConfirm 覆盖改价清确认与相同报价也升版本。
func TestVersionBumpClearsConfirm(t *testing.T) {
	sys := New(50, 1000, 4, 1000, 4)
	mustgrant(t, sys, "a", "sword", 1)
	mustGrantGold(t, sys, "b", 200)
	sid := openWith(t, sys, 0, "a", "b") // ver=0
	doOffer(t, sys, 0, sid, "a", []ItemQty{{"sword", 1}}, 0)
	doOffer(t, sys, 0, sid, "b", nil, 100) // ver=2
	doConfirm(t, sys, 0, sid, "b", 2)
	// a 以完全相同的报价再次 Offer：仍升版本并清除 b 的确认。
	doOffer(t, sys, 1, sid, "a", []ItemQty{{"sword", 1}}, 0) // ver=3
	wantCode(t, "confirm(b, ver=2)", sys.Confirm(1, sid, "b", 2), ErrStale)
	t.Logf("相同报价也使 ver 2->3，b 的旧确认被清除，ver=2 确认报版本过期")
	doConfirm(t, sys, 1, sid, "b", 3)
	doConfirm(t, sys, 1, sid, "a", 3)
	if sys.Gold("a") != 95 || sys.Burned() != 5 {
		t.Fatalf("a gold = %d, burned = %d, want 95/5", sys.Gold("a"), sys.Burned())
	}
}

// TestAlreadyConfirmed 覆盖重复确认报已确认。
func TestAlreadyConfirmed(t *testing.T) {
	sys := New(0, 1000, 4, 1000, 4)
	mustgrant(t, sys, "a", "x", 1)
	mustgrant(t, sys, "b", "y", 1)
	sid := openWith(t, sys, 0, "a", "b")
	doOffer(t, sys, 0, sid, "a", []ItemQty{{"x", 1}}, 0)
	doConfirm(t, sys, 0, sid, "a", 1)
	wantCode(t, "confirm(a) again", sys.Confirm(0, sid, "a", 1), ErrConfirmed)
}

// TestEmptyTrade 覆盖双方报价都为空时报空交易。
func TestEmptyTrade(t *testing.T) {
	sys := New(0, 1000, 4, 1000, 4)
	mustgrant(t, sys, "a", "x", 1)
	mustgrant(t, sys, "b", "y", 1)
	sid := openWith(t, sys, 0, "a", "b")
	wantCode(t, "confirm empty", sys.Confirm(0, sid, "a", 0), ErrEmpty)
	// 一方报价后即非空交易。
	doOffer(t, sys, 0, sid, "a", []ItemQty{{"x", 1}}, 0)
	doConfirm(t, sys, 0, sid, "a", 1)
	doConfirm(t, sys, 0, sid, "b", 1)
	if sys.Holding("b", "x") != 1 {
		t.Fatalf("b should hold x")
	}
}

// TestExpiry 覆盖过期取等与锁定释放：Open(0) 后 Offer(400) 把到期刷新为
// 1400，Confirm(1399) 有效，Confirm(1400) 报会话不存在且锁定已释放。
func TestExpiry(t *testing.T) {
	sys := New(0, 1000, 4, 1000, 4)
	mustgrant(t, sys, "a", "sword", 1)
	mustgrant(t, sys, "b", "shield", 1)
	sid := openWith(t, sys, 0, "a", "b")
	doOffer(t, sys, 400, sid, "a", []ItemQty{{"sword", 1}}, 0)
	doOffer(t, sys, 400, sid, "b", []ItemQty{{"shield", 1}}, 0)
	doConfirm(t, sys, 1399, sid, "a", 2)
	wantCode(t, "confirm(b, 1400)", sys.Confirm(1400, sid, "b", 2), ErrNoSession)
	// 被拒绝的操作不落地过期：锁定台账仍含该会话；一个被接受的操作使其落地。
	sid2 := openWith(t, sys, 1400, "a", "b")
	if got := sys.Locked("a", "sword"); got != 0 {
		t.Fatalf("locked sword = %d, want 0（过期会话锁定已释放）", got)
	}
	// a 可以在新会话中重新报出全部 sword。
	doOffer(t, sys, 1400, sid2, "a", []ItemQty{{"sword", 1}}, 0)
	t.Logf("Confirm(1400) 取等过期报会话不存在；过期落地后锁定释放，新会话可全额报价")
}

// TestConfirmDoesNotRefreshExpiry 覆盖 Confirm 不刷新到期时刻。
func TestConfirmDoesNotRefreshExpiry(t *testing.T) {
	sys := New(0, 1000, 4, 1000, 4)
	mustgrant(t, sys, "a", "x", 1)
	mustgrant(t, sys, "b", "y", 1)
	sid := openWith(t, sys, 0, "a", "b")
	doOffer(t, sys, 0, sid, "a", []ItemQty{{"x", 1}}, 0)
	doOffer(t, sys, 0, sid, "b", []ItemQty{{"y", 1}}, 0)
	doConfirm(t, sys, 500, sid, "a", 2)
	wantCode(t, "confirm(b, 1000)", sys.Confirm(1000, sid, "b", 2), ErrNoSession)
	t.Logf("Confirm(500) 不刷新到期时刻，Confirm(1000) 取等过期")
}

// TestQuota 覆盖每名玩家同时开启的会话数上限。
func TestQuota(t *testing.T) {
	sys := New(0, 1000, 4, 1000, 2)
	for _, p := range []string{"a", "b", "c", "d"} {
		mustgrant(t, sys, p, "x", 1)
	}
	openWith(t, sys, 0, "a", "b")
	openWith(t, sys, 0, "a", "c")
	_, err := sys.Open(0, "a", "d")
	wantErrPlayer(t, "open(a,d)", err, ErrQuota, "a")
	// b 的配额单独计算：b 再开两个会话，第二个报 b 超限。
	openWith(t, sys, 0, "b", "c")
	_, err = sys.Open(0, "b", "d")
	wantErrPlayer(t, "open(b,d)", err, ErrQuota, "b")
	// 双方都已达配额时报 a（先查 a）。
	_, err = sys.Open(0, "a", "b")
	wantErrPlayer(t, "open(a,b)", err, ErrQuota, "a")
	// 取消一个会话后配额释放。
	if err := sys.Cancel(1, 1, "a"); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	openWith(t, sys, 1, "a", "d")
}

// TestAvailabilityAcrossSessions 覆盖可用量 = 持有量 − 本人在其他会话中的
// 锁定量，以及成交后其他会话的锁定仍然成立。
func TestAvailabilityAcrossSessions(t *testing.T) {
	sys := New(0, 1000, 8, 1000, 8)
	mustgrant(t, sys, "a", "potion", 5)
	for _, p := range []string{"b", "c", "d"} {
		mustgrant(t, sys, p, "y", 1)
	}
	s1 := openWith(t, sys, 0, "a", "b")
	s2 := openWith(t, sys, 0, "a", "c")
	doOffer(t, sys, 0, s1, "a", []ItemQty{{"potion", 3}}, 0)
	// s2 中可用 5-3=2，报 3 不足。
	wantCode(t, "offer(s2, 3)", sys.Offer(0, s2, "a", []ItemQty{{"potion", 3}}, 0), ErrInsufficient)
	// s1 改报 2 后，s2 可用 3。
	doOffer(t, sys, 0, s1, "a", []ItemQty{{"potion", 2}}, 0)
	doOffer(t, sys, 0, s2, "a", []ItemQty{{"potion", 3}}, 0)
	// s1 成交：a 付出 2，余 3，全部被 s2 锁定。
	doOffer(t, sys, 0, s1, "b", []ItemQty{{"y", 1}}, 0)
	doConfirm(t, sys, 0, s1, "a", 3)
	doConfirm(t, sys, 0, s1, "b", 3)
	if got := sys.Holding("a", "potion"); got != 3 {
		t.Fatalf("a potion = %d, want 3", got)
	}
	// 任何第三个会话中 a 报 potion 均不足。
	s3 := openWith(t, sys, 0, "a", "d")
	wantCode(t, "offer(s3, 1)", sys.Offer(0, s3, "a", []ItemQty{{"potion", 1}}, 0), ErrInsufficient)
	t.Logf("s1 成交后 a 持有 3 且全部被 s2 锁定，s3 中可用量为 0")
}

// TestCancel 覆盖取消的语义与拒绝次序。
func TestCancel(t *testing.T) {
	sys := New(0, 1000, 4, 1000, 4)
	mustgrant(t, sys, "a", "x", 1)
	mustgrant(t, sys, "b", "y", 1)
	sid := openWith(t, sys, 0, "a", "b")
	doOffer(t, sys, 0, sid, "a", []ItemQty{{"x", 1}}, 0)
	wantCode(t, "cancel(outsider)", sys.Cancel(0, sid, "z"), ErrNotMember)
	if err := sys.Cancel(0, sid, "b"); err != nil {
		t.Fatalf("cancel by member: %v", err)
	}
	if got := sys.Locked("a", "x"); got != 0 {
		t.Fatalf("locked x = %d, want 0", got)
	}
	wantCode(t, "offer after cancel", sys.Offer(0, sid, "a", nil, 0), ErrNoSession)
	wantCode(t, "cancel after cancel", sys.Cancel(0, sid, "a"), ErrNoSession)
}

// TestRejectionOrder 覆盖各操作的拒绝次序。
func TestRejectionOrder(t *testing.T) {
	sys := New(0, 1000, 4, 1000, 4)
	mustgrant(t, sys, "a", "x", 2)
	mustgrant(t, sys, "b", "y", 2)
	sid := openWith(t, sys, 100, "a", "b") // maxNow=100
	cases := []struct {
		name string
		err  error
		want ErrCode
	}{
		// 参数非法优先于时钟回退。
		{"open: a==b 且时钟回退", errOf(func() error { _, e := sys.Open(50, "a", "a"); return e }), ErrInvalid},
		{"offer: 物品重复且时钟回退", sys.Offer(50, sid, "a", []ItemQty{{"x", 1}, {"x", 1}}, 0), ErrInvalid},
		{"offer: 数量超界", sys.Offer(100, sid, "a", []ItemQty{{"x", 1_000_000_001}}, 0), ErrInvalid},
		{"offer: 金币超 CAP", sys.Offer(100, sid, "a", nil, 1001), ErrInvalid},
		{"offer: 物品种类超 16", sys.Offer(100, sid, "a", manyItems(17), 0), ErrInvalid},
		{"confirm: ver 为负", sys.Confirm(100, sid, "a", -1), ErrInvalid},
		// 时钟回退优先于会话不存在。
		{"open: 时钟回退优先于玩家不存在", errOf(func() error { _, e := sys.Open(50, "g1", "g2"); return e }), ErrClock},
		{"offer: 时钟回退优先于会话不存在", sys.Offer(50, 999, "a", nil, 0), ErrClock},
		{"confirm: 时钟回退优先于会话不存在", sys.Confirm(50, 999, "a", 0), ErrClock},
		{"cancel: 时钟回退优先于会话不存在", sys.Cancel(50, 999, "a"), ErrClock},
		// 会话不存在优先于非成员。
		{"offer: 会话不存在优先于非成员", sys.Offer(100, 999, "z", nil, 0), ErrNoSession},
		{"confirm: 会话不存在优先于非成员", sys.Confirm(100, 999, "z", 0), ErrNoSession},
		// 非成员优先于不足 / 版本过期。
		{"offer: 非成员优先于不足", sys.Offer(100, sid, "z", []ItemQty{{"x", 999}}, 0), ErrNotMember},
		{"confirm: 非成员优先于版本过期", sys.Confirm(100, sid, "z", 99), ErrNotMember},
		// 版本过期优先于已确认。
		{"confirm: 版本过期", sys.Confirm(100, sid, "a", 99), ErrStale},
	}
	for _, tc := range cases {
		wantCode(t, tc.name, tc.err, tc.want)
	}
	// 玩家不存在：a 先查。
	_, err := sys.Open(100, "ghost", "b")
	wantErrPlayer(t, "open(ghost,b)", err, ErrNoPlayer, "ghost")
	_, err = sys.Open(100, "a", "ghost")
	wantErrPlayer(t, "open(a,ghost)", err, ErrNoPlayer, "ghost")
}

func manyItems(n int) []ItemQty {
	out := make([]ItemQty, n)
	for i := range out {
		out[i] = ItemQty{Item: string(rune('A' + i)), Qty: 1}
	}
	return out
}

func errOf(f func() error) error { return f() }

// ---------------------------------------------------------------------------
// 朴素模拟：每次需要锁定量时都遍历全部会话求和，作为被测系统的对照模型。
// ---------------------------------------------------------------------------

type nOffer struct {
	items map[string]int64
	gold  int64
}

type nSess struct {
	a, b   string
	ver    int64
	exp    int64
	offers [2]nOffer
	conf   [2]bool
	done   bool
}

type naive struct {
	r, cap, slots, ttl, quota int64
	maxNow, lastSID, burned   int64
	players                   map[string]bool
	gold                      map[string]int64
	items                     map[string]map[string]int64
	sess                      map[int64]*nSess
}

func newNaive(r, cap, slots, ttl, quota int64) *naive {
	return &naive{
		r: r, cap: cap, slots: slots, ttl: ttl, quota: quota,
		players: make(map[string]bool),
		gold:    make(map[string]int64),
		items:   make(map[string]map[string]int64),
		sess:    make(map[int64]*nSess),
	}
}

func (n *naive) alive(s *nSess, now int64) bool {
	return s != nil && !s.done && now < s.exp
}

// lockedItem 遍历全部会话求 p 对 item 的锁定量（朴素实现）。
func (n *naive) lockedItem(p, item string, now int64) int64 {
	var sum int64
	for _, s := range n.sess {
		if !n.alive(s, now) {
			continue
		}
		if s.a == p {
			sum += s.offers[0].items[item]
		}
		if s.b == p {
			sum += s.offers[1].items[item]
		}
	}
	return sum
}

func (n *naive) lockedGold(p string, now int64) int64 {
	var sum int64
	for _, s := range n.sess {
		if !n.alive(s, now) {
			continue
		}
		if s.a == p {
			sum += s.offers[0].gold
		}
		if s.b == p {
			sum += s.offers[1].gold
		}
	}
	return sum
}

func (n *naive) openCount(p string, now int64) int64 {
	var c int64
	for _, s := range n.sess {
		if n.alive(s, now) && (s.a == p || s.b == p) {
			c++
		}
	}
	return c
}

func (n *naive) holding(p, item string) int64 { return n.items[p][item] }

func (n *naive) grant(p, item string, qty int64) (ErrCode, string) {
	if p == "" || item == "" || qty < 0 {
		return ErrInvalid, "reject: invalid grant args"
	}
	if qty > 0 && n.items[p][item] == 0 && int64(len(n.items[p])) >= n.slots {
		return ErrSlots, "reject: grant would exceed slots"
	}
	n.players[p] = true
	if qty > 0 {
		if n.items[p] == nil {
			n.items[p] = make(map[string]int64)
		}
		n.items[p][item] += qty
	}
	return noErr, "ok: granted"
}

func (n *naive) grantGold(p string, g int64) (ErrCode, string) {
	if p == "" || g < 0 {
		return ErrInvalid, "reject: invalid grant args"
	}
	if g > n.cap-n.gold[p] {
		return ErrGoldCap, "reject: grant would exceed gold cap"
	}
	n.players[p] = true
	n.gold[p] += g
	return noErr, "ok: granted gold"
}

func (n *naive) open(now int64, a, b string) (ErrCode, int64, string) {
	if now < 0 || a == "" || b == "" || a == b {
		return ErrInvalid, 0, "reject: invalid open args"
	}
	if now < n.maxNow {
		return ErrClock, 0, "reject: clock rollback"
	}
	if !n.players[a] {
		return ErrNoPlayer, 0, "reject: player a not registered"
	}
	if !n.players[b] {
		return ErrNoPlayer, 0, "reject: player b not registered"
	}
	if n.openCount(a, now) >= n.quota {
		return ErrQuota, 0, "reject: a at session quota"
	}
	if n.openCount(b, now) >= n.quota {
		return ErrQuota, 0, "reject: b at session quota"
	}
	n.maxNow = now
	n.lastSID++
	n.sess[n.lastSID] = &nSess{a: a, b: b, exp: now + n.ttl}
	return noErr, n.lastSID, "ok: opened"
}

func nMember(s *nSess, who string) int {
	if who == s.a {
		return 0
	}
	if who == s.b {
		return 1
	}
	return -1
}

func (n *naive) offer(now, sid int64, who string, items []ItemQty, gold int64) (ErrCode, string) {
	newItems := make(map[string]int64, len(items))
	valid := now >= 0 && sid >= 1 && who != "" && gold >= 0 && gold <= n.cap && len(items) <= 16
	if valid {
		for _, it := range items {
			if it.Item == "" || it.Qty < 1 || it.Qty > 1_000_000_000 {
				valid = false
				break
			}
			if _, dup := newItems[it.Item]; dup {
				valid = false
				break
			}
			newItems[it.Item] = it.Qty
		}
	}
	if !valid {
		return ErrInvalid, "reject: invalid offer args"
	}
	if now < n.maxNow {
		return ErrClock, "reject: clock rollback"
	}
	s := n.sess[sid]
	if !n.alive(s, now) {
		return ErrNoSession, "reject: session missing/finished/expired"
	}
	idx := nMember(s, who)
	if idx < 0 {
		return ErrNotMember, "reject: not a member"
	}
	cur := s.offers[idx]
	for item, q := range newItems {
		avail := n.holding(who, item) - (n.lockedItem(who, item, now) - cur.items[item])
		if q > avail {
			return ErrInsufficient, "reject: insufficient item " + item
		}
	}
	if avail := n.gold[who] - (n.lockedGold(who, now) - cur.gold); gold > avail {
		return ErrInsufficient, "reject: insufficient gold"
	}
	n.maxNow = now
	s.offers[idx] = nOffer{items: newItems, gold: gold}
	s.ver++
	s.conf = [2]bool{}
	s.exp = now + n.ttl
	return noErr, "ok: offer replaced, ver bumped, confirms cleared"
}

func nKindsAfter(holdings map[string]int64, remove, add map[string]int64) int64 {
	m := make(map[string]int64, len(holdings))
	for k, v := range holdings {
		m[k] = v
	}
	for k, v := range remove {
		m[k] -= v
		if m[k] <= 0 {
			delete(m, k)
		}
	}
	for k, v := range add {
		m[k] += v
	}
	return int64(len(m))
}

func nApplyItems(holdings map[string]int64, remove, add map[string]int64) {
	for k, v := range remove {
		holdings[k] -= v
		if holdings[k] <= 0 {
			delete(holdings, k)
		}
	}
	for k, v := range add {
		holdings[k] += v
	}
}

func (n *naive) confirm(now, sid int64, who string, ver int64) (ErrCode, string) {
	if now < 0 || sid < 1 || who == "" || ver < 0 {
		return ErrInvalid, "reject: invalid confirm args"
	}
	if now < n.maxNow {
		return ErrClock, "reject: clock rollback"
	}
	s := n.sess[sid]
	if !n.alive(s, now) {
		return ErrNoSession, "reject: session missing/finished/expired"
	}
	idx := nMember(s, who)
	if idx < 0 {
		return ErrNotMember, "reject: not a member"
	}
	if ver != s.ver {
		return ErrStale, "reject: stale ver"
	}
	if s.conf[idx] {
		return ErrConfirmed, "reject: already confirmed"
	}
	if len(s.offers[0].items) == 0 && s.offers[0].gold == 0 &&
		len(s.offers[1].items) == 0 && s.offers[1].gold == 0 {
		return ErrEmpty, "reject: empty trade"
	}
	if !s.conf[1-idx] {
		n.maxNow = now
		s.conf[idx] = true
		return noErr, "ok: confirmation recorded"
	}
	oA, oB := s.offers[0], s.offers[1]
	taxA := (oA.gold*n.r + 999) / 1000
	taxB := (oB.gold*n.r + 999) / 1000
	if after := n.gold[s.a] - oA.gold + oB.gold - taxB; after > n.cap {
		return ErrGoldCap, "reject: a gold cap"
	}
	if after := n.gold[s.b] - oB.gold + oA.gold - taxA; after > n.cap {
		return ErrGoldCap, "reject: b gold cap"
	}
	if kinds := nKindsAfter(n.items[s.a], oA.items, oB.items); kinds > n.slots {
		return ErrSlots, "reject: a slot limit"
	}
	if kinds := nKindsAfter(n.items[s.b], oB.items, oA.items); kinds > n.slots {
		return ErrSlots, "reject: b slot limit"
	}
	n.maxNow = now
	nApplyItems(n.items[s.a], oA.items, oB.items)
	nApplyItems(n.items[s.b], oB.items, oA.items)
	n.gold[s.a] += -oA.gold + oB.gold - taxB
	n.gold[s.b] += -oB.gold + oA.gold - taxA
	n.burned += taxA + taxB
	s.done = true
	return noErr, "ok: trade completed"
}

func (n *naive) cancel(now, sid int64, who string) (ErrCode, string) {
	if now < 0 || sid < 1 || who == "" {
		return ErrInvalid, "reject: invalid cancel args"
	}
	if now < n.maxNow {
		return ErrClock, "reject: clock rollback"
	}
	s := n.sess[sid]
	if !n.alive(s, now) {
		return ErrNoSession, "reject: session missing/finished/expired"
	}
	if nMember(s, who) < 0 {
		return ErrNotMember, "reject: not a member"
	}
	n.maxNow = now
	s.done = true
	return noErr, "ok: cancelled"
}

// ---------------------------------------------------------------------------
// 随机对照：1500 组随机操作序列，逐操作比对被测系统与朴素模拟。
// ---------------------------------------------------------------------------

func pickInt64(rng *rand.Rand, xs []int64) int64 { return xs[rng.Intn(len(xs))] }

func pickStr(rng *rand.Rand, xs []string) string { return xs[rng.Intn(len(xs))] }

func codeStr(c ErrCode) string {
	if c == noErr {
		return "ok"
	}
	return c.String()
}

func TestRandomDifferential(t *testing.T) {
	players := []string{"p0", "p1", "p2", "p3"}
	items := []string{"i0", "i1", "i2", "i3", "i4", "i5"}
	for seed := int64(0); seed < 1500; seed++ {
		rng := rand.New(rand.NewSource(seed))
		r := pickInt64(rng, []int64{0, 1, 50, 333, 1000})
		cap := pickInt64(rng, []int64{10, 100, 1000, 1_000_000, 1_000_000_000_000_000})
		slots := pickInt64(rng, []int64{1, 2, 4, 8})
		ttl := pickInt64(rng, []int64{1, 5, 50, 1000, 1_000_000_000})
		quota := pickInt64(rng, []int64{1, 2, 4, 8})
		sys := New(r, cap, slots, ttl, quota)
		nv := newNaive(r, cap, slots, ttl, quota)
		now := int64(0)
		grantedGold := make(map[string]int64)
		grantedItems := make(map[string]map[string]int64)

		checkState := func(op int, checkLocks bool) {
			t.Helper()
			if sys.Burned() != nv.burned {
				t.Fatalf("seed=%d op=%d: burned sys=%d naive=%d", seed, op, sys.Burned(), nv.burned)
			}
			for _, p := range players {
				if sys.Gold(p) != nv.gold[p] {
					t.Fatalf("seed=%d op=%d: gold[%s] sys=%d naive=%d", seed, op, p, sys.Gold(p), nv.gold[p])
				}
				got := sys.Holdings(p)
				for _, it := range items {
					if got[it] != nv.items[p][it] {
						t.Fatalf("seed=%d op=%d: holding[%s][%s] sys=%d naive=%d",
							seed, op, p, it, got[it], nv.items[p][it])
					}
				}
				if !checkLocks {
					continue
				}
				for _, it := range items {
					if sys.Locked(p, it) != nv.lockedItem(p, it, now) {
						t.Fatalf("seed=%d op=%d: locked[%s][%s] sys=%d naive=%d",
							seed, op, p, it, sys.Locked(p, it), nv.lockedItem(p, it, now))
					}
				}
				if sys.LockedGold(p) != nv.lockedGold(p, now) {
					t.Fatalf("seed=%d op=%d: lockedGold[%s] sys=%d naive=%d",
						seed, op, p, sys.LockedGold(p), nv.lockedGold(p, now))
				}
			}
		}

		for op := 0; op < 40; op++ {
			// 推进时钟：多数前进，偶尔原地，偶尔回退一格制造时钟回退。
			switch x := rng.Intn(100); {
			case x < 70:
				now += rng.Int63n(ttl + 2)
			case x < 75 && now > 0:
				now--
			}
			kind := rng.Intn(100)
			var sysErr error
			var nCode ErrCode
			var desc, reason string
			switch {
			case kind < 20: // Grant
				p, it := pickStr(rng, players), pickStr(rng, items)
				qty := rng.Int63n(9)
				sysErr = sys.Grant(p, it, qty)
				nCode, reason = nv.grant(p, it, qty)
				desc = fmt.Sprintf("Grant(%s,%s,%d)@%d", p, it, qty, now)
				if nCode == noErr {
					if grantedItems[p] == nil {
						grantedItems[p] = make(map[string]int64)
					}
					grantedItems[p][it] += qty
				}
			case kind < 32: // GrantGold
				p := pickStr(rng, players)
				g := rng.Int63n(2*cap + 1)
				sysErr = sys.GrantGold(p, g)
				nCode, reason = nv.grantGold(p, g)
				desc = fmt.Sprintf("GrantGold(%s,%d)@%d", p, g, now)
				if nCode == noErr {
					grantedGold[p] += g
				}
			case kind < 46: // Open
				a, b := pickStr(rng, players), pickStr(rng, players)
				sid, err := sys.Open(now, a, b)
				sysErr = err
				var nSID int64
				nCode, nSID, reason = nv.open(now, a, b)
				desc = fmt.Sprintf("Open(%s,%s)@%d", a, b, now)
				if err == nil && sid != nSID {
					t.Fatalf("seed=%d op=%d: %s sid sys=%d naive=%d", seed, op, desc, sid, nSID)
				}
			case kind < 74: // Offer
				sid := int64(1 + rng.Intn(int(nv.lastSID)+2))
				who := pickStr(rng, players)
				if rng.Intn(20) == 0 {
					who = "ghost"
				}
				var ofs []ItemQty
				if rng.Intn(20) == 0 { // 偶发非法报价
					ofs = []ItemQty{{"i0", 0}}
				} else {
					k := rng.Intn(4)
					perm := rng.Perm(len(items))
					for i := 0; i < k; i++ {
						ofs = append(ofs, ItemQty{items[perm[i]], 1 + rng.Int63n(6)})
					}
				}
				g := rng.Int63n(cap + 2)
				sysErr = sys.Offer(now, sid, who, ofs, g)
				nCode, reason = nv.offer(now, sid, who, ofs, g)
				desc = fmt.Sprintf("Offer(sid=%d,%s,%v,gold=%d)@%d", sid, who, ofs, g, now)
			case kind < 90: // Confirm
				sid := int64(1 + rng.Intn(int(nv.lastSID)+2))
				who := pickStr(rng, players)
				ver := rng.Int63n(4)
				if s := nv.sess[sid]; nv.alive(s, now) && rng.Intn(2) == 0 {
					ver = s.ver // 一半概率用当前版本
				}
				sysErr = sys.Confirm(now, sid, who, ver)
				nCode, reason = nv.confirm(now, sid, who, ver)
				desc = fmt.Sprintf("Confirm(sid=%d,%s,ver=%d)@%d", sid, who, ver, now)
			default: // Cancel
				sid := int64(1 + rng.Intn(int(nv.lastSID)+2))
				who := pickStr(rng, players)
				sysErr = sys.Cancel(now, sid, who)
				nCode, reason = nv.cancel(now, sid, who)
				desc = fmt.Sprintf("Cancel(sid=%d,%s)@%d", sid, who, now)
			}
			got := noErr
			if sysErr != nil {
				c, ok := CodeOf(sysErr)
				if !ok {
					t.Fatalf("seed=%d op=%d: %s: non-trade error %v", seed, op, desc, sysErr)
				}
				got = c
			}
			t.Logf("seed=%d op=%02d cfg(r=%d,cap=%d,slots=%d,ttl=%d,q=%d) %s -> sys=%s naive=%s | 依据: %s",
				seed, op, r, cap, slots, ttl, quota, desc, codeStr(got), codeStr(nCode), reason)
			if got != nCode {
				t.Fatalf("seed=%d op=%d: %s: sys=%s naive=%s (%s)", seed, op, desc, got, nCode, reason)
			}
			checkState(op, got == noErr && kind >= 46)
		}

		// 收尾：用一个被接受的 Open 落地全部过期会话，再比对锁定量与守恒量。
		mustgrant(t, sys, "finA", "z", 1)
		mustgrant(t, sys, "finB", "z", 1)
		nv.grant("finA", "z", 1)
		nv.grant("finB", "z", 1)
		if _, err := sys.Open(nv.maxNow, "finA", "finB"); err != nil {
			t.Fatalf("seed=%d: final open: %v", seed, err)
		}
		if _, _, reason := nv.open(nv.maxNow, "finA", "finB"); reason != "ok: opened" {
			t.Fatalf("seed=%d: naive final open: %s", seed, reason)
		}
		var totalGold, totalBurned int64
		for _, p := range players {
			totalGold += sys.Gold(p)
			for _, it := range items {
				if sys.Locked(p, it) != nv.lockedItem(p, it, nv.maxNow) {
					t.Fatalf("seed=%d final: locked[%s][%s] sys=%d naive=%d",
						seed, p, it, sys.Locked(p, it), nv.lockedItem(p, it, nv.maxNow))
				}
				if nv.lockedItem(p, it, nv.maxNow) > sys.Holding(p, it) {
					t.Fatalf("seed=%d final: locked exceeds holding for %s/%s", seed, p, it)
				}
			}
			if sys.LockedGold(p) != nv.lockedGold(p, nv.maxNow) {
				t.Fatalf("seed=%d final: lockedGold[%s] sys=%d naive=%d",
					seed, p, sys.LockedGold(p), nv.lockedGold(p, nv.maxNow))
			}
		}
		totalBurned = sys.Burned()
		var grantedTotal int64
		for _, g := range grantedGold {
			grantedTotal += g
		}
		if totalGold+totalBurned != grantedTotal {
			t.Fatalf("seed=%d final: gold %d + burned %d != granted %d",
				seed, totalGold, totalBurned, grantedTotal)
		}
		for _, it := range items {
			var held, granted int64
			for _, p := range players {
				held += sys.Holding(p, it)
				granted += grantedItems[p][it]
			}
			if held != granted {
				t.Fatalf("seed=%d final: item %s held %d != granted %d", seed, it, held, granted)
			}
		}
	}
}

// TestConcurrent 并发调用下不变式仍成立：锁定不超持有、物品守恒、
// 金币加 burned 守恒（同一物品不会因并发被两笔交易同时付出）。
func TestConcurrent(t *testing.T) {
	const nPlayers = 8
	sys := New(50, 1_000_000, 16, 1_000_000_000, 64)
	players := make([]string, nPlayers)
	var grantedGold, grantedItems int64
	for i := range players {
		p := fmt.Sprintf("p%d", i)
		players[i] = p
		mustgrant(t, sys, p, fmt.Sprintf("i%d", i), 100)
		mustgrant(t, sys, p, "common", 100)
		mustGrantGold(t, sys, p, 1000)
		grantedGold += 1000
		grantedItems += 200
	}
	var now atomic.Int64
	var wg sync.WaitGroup
	for w := 0; w < 16; w++ {
		wg.Add(1)
		go func(seed int64) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(seed))
			for i := 0; i < 300; i++ {
				n := now.Add(1)
				a := players[rng.Intn(nPlayers)]
				b := players[rng.Intn(nPlayers)]
				if a == b {
					continue
				}
				sid, err := sys.Open(n, a, b)
				if err != nil {
					continue
				}
				var ver int64
				if err := sys.Offer(now.Add(1), sid, a,
					[]ItemQty{{fmt.Sprintf("i%d", rng.Intn(nPlayers)), int64(1 + rng.Intn(3))}},
					rng.Int63n(50)); err == nil {
					ver++
				}
				if err := sys.Offer(now.Add(1), sid, b,
					[]ItemQty{{"common", int64(1 + rng.Intn(3))}}, rng.Int63n(50)); err == nil {
					ver++
				}
				_ = sys.Confirm(now.Add(1), sid, a, ver)
				_ = sys.Confirm(now.Add(1), sid, b, ver)
				_ = sys.Cancel(now.Add(1), sid, a)
			}
		}(int64(w))
	}
	wg.Wait()
	for _, p := range players {
		for _, it := range []string{"common", fmt.Sprintf("i%d", 0), "i1", "i2", "i3", "i4", "i5", "i6", "i7"} {
			if sys.Locked(p, it) > sys.Holding(p, it) {
				t.Fatalf("locked exceeds holding: %s %s locked=%d holding=%d",
					p, it, sys.Locked(p, it), sys.Holding(p, it))
			}
		}
		if sys.LockedGold(p) > sys.Gold(p) {
			t.Fatalf("locked gold exceeds holding: %s", p)
		}
	}
	var totalGold, totalItems int64
	for _, p := range players {
		totalGold += sys.Gold(p)
		for _, q := range sys.Holdings(p) {
			totalItems += q
		}
	}
	if totalGold+sys.Burned() != grantedGold {
		t.Fatalf("gold %d + burned %d != granted %d", totalGold, sys.Burned(), grantedGold)
	}
	if totalItems != grantedItems {
		t.Fatalf("items %d != granted %d", totalItems, grantedItems)
	}
	t.Logf("并发后: gold=%d burned=%d items=%d（均守恒）", totalGold, sys.Burned(), totalItems)
}

// TestOfferTouchedIndependentOfOtherSessions 在系统层面证明：一次 Offer
// 读写的锁定记录数不超过新旧报价条目数之和加 2，与该玩家其他会话数量无关。
func TestOfferTouchedIndependentOfOtherSessions(t *testing.T) {
	for _, others := range []int{1, 1000} {
		sys := New(0, 1_000_000, 8, 1_000_000_000, 2000)
		mustgrant(t, sys, "p", "k", int64(others+10))
		mustgrant(t, sys, "p", "x", 10)
		mustgrant(t, sys, "p", "y", 10)
		mustgrant(t, sys, "p", "z", 10)
		mustGrantGold(t, sys, "p", 100)
		// p 在 others 个其他会话中各有锁定。
		for i := 0; i < others; i++ {
			partner := fmt.Sprintf("q%d", i)
			mustgrant(t, sys, partner, "w", 1)
			sid := openWith(t, sys, int64(i), "p", partner)
			doOffer(t, sys, int64(i), sid, "p", []ItemQty{{"k", 1}}, 0)
		}
		partner := "qf"
		mustgrant(t, sys, partner, "w", 1)
		sid := openWith(t, sys, int64(others), "p", partner)
		doOffer(t, sys, int64(others), sid, "p", []ItemQty{{"x", 1}, {"y", 1}}, 5) // 旧报价：2 物品 + 金币
		before := sys.Touched()
		doOffer(t, sys, int64(others), sid, "p", []ItemQty{{"z", 1}}, 7) // 新报价：1 物品 + 金币
		got := sys.Touched() - before
		want := int64(2 + 1 + 2)
		if got != want {
			t.Fatalf("others=%d: touched = %d, want %d", others, got, want)
		}
		t.Logf("其他会话 %d 个: 一次 Offer 触碰锁定记录 %d 条（旧 2 + 新 1 + 金币 2）", others, got)
	}
}

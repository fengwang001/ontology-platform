package ontology

import "testing"

func errKind(err error) ErrorKind {
	if err == nil {
		return 0
	}
	return err.(*OpError).Kind
}

func mustReg(t *testing.T, e *Engine, id string, bid, q, budget int64) {
	t.Helper()
	if err := e.Register(id, bid, q, budget); err != nil {
		t.Fatalf("register %s: %v", id, err)
	}
}

// TestSpecExample 复现题目给出的示例。
func TestSpecExample(t *testing.T) {
	e := NewEngine()
	mustReg(t, e, "A", 50, 20, 1_000_000)
	mustReg(t, e, "B", 40, 30, 1_000_000)
	mustReg(t, e, "C", 45, 10, 1_000_000)
	mustReg(t, e, "D", 8, 50, 1_000_000) // bid < P
	mustReg(t, e, "E", 60, 5, 50)        // a = 50 < bid 60

	ws, err := e.Auction(2, 10)
	if err != nil {
		t.Fatalf("auction: %v", err)
	}
	if len(ws) != 2 || ws[0].ID != "B" || ws[0].P != 34 || ws[1].ID != "A" || ws[1].P != 23 {
		t.Fatalf("winners = %+v", ws)
	}

	// K=3 时最后一名 C 无下一名，p_C = P = 10。
	ws3, err := e.Auction(3, 10)
	if err != nil {
		t.Fatalf("auction3: %v", err)
	}
	if len(ws3) != 3 || ws3[2].ID != "C" || ws3[2].P != 10 {
		t.Fatalf("winners3 = %+v", ws3)
	}

	if err := e.Resolve(1, []string{"B"}); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	b, _ := e.GetBidder("B")
	a, _ := e.GetBidder("A")
	if b.Budget != 1_000_000-34 || b.Q != 151 || b.F != 0 || b.H != 34 {
		t.Fatalf("B after resolve = %+v", b)
	}
	// A 仍在未结算的 2 号拍卖中标，故保留占用 23；预算未扣。
	if a.Q != 18 || a.F != 1 || a.H != 23 || a.Budget != 1_000_000 {
		t.Fatalf("A after resolve = %+v", a)
	}
	qeB := effectiveQ(b.Q, b.F)
	qeA := effectiveQ(a.Q, a.F)
	if qeB != 151 || qeA != 18 || b.Bid*qeB != 6040 {
		t.Fatalf("effective scores mismatch qeB=%d qeA=%d", qeB, qeA)
	}
}

// TestBidFloorBoundary：bid 恰等于 P 参拍，差 1 不参拍。
func TestBidFloorBoundary(t *testing.T) {
	e := NewEngine()
	mustReg(t, e, "at", 100, 100, 100000)
	mustReg(t, e, "below", 99, 100, 100000)
	ws, err := e.Auction(10, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(ws) != 1 || ws[0].ID != "at" {
		t.Fatalf("winners = %+v", ws)
	}
	_, err = e.Auction(1, 101)
	if errKind(err) != ErrNoParticipants {
		t.Fatalf("want NoParticipants, got %v", err)
	}
}

// TestBudgetBoundary：a 恰等于 bid 参拍，差 1 不参拍。
func TestBudgetBoundary(t *testing.T) {
	e := NewEngine()
	mustReg(t, e, "tight", 100, 100, 100) // a = 100 == bid
	mustReg(t, e, "short", 100, 100, 99)  // a = 99 < bid
	ws, err := e.Auction(5, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(ws) != 1 || ws[0].ID != "tight" || ws[0].P != 1 {
		t.Fatalf("winners = %+v", ws)
	}
	b, _ := e.GetBidder("tight")
	if b.H != 1 {
		t.Fatalf("H = %d, want 1", b.H)
	}
}

// TestTieBySeq：同分按登记序号小者在前。
func TestTieBySeq(t *testing.T) {
	e := NewEngine()
	mustReg(t, e, "x", 20, 10, 1_000_000_000) // s = 200
	mustReg(t, e, "y", 10, 20, 1_000_000_000) // s = 200
	mustReg(t, e, "z", 40, 5, 1_000_000_000)  // s = 200
	ws, err := e.Auction(3, 1)
	if err != nil {
		t.Fatal(err)
	}
	if ws[0].ID != "x" || ws[1].ID != "y" || ws[2].ID != "z" {
		t.Fatalf("order = %s %s %s", ws[0].ID, ws[1].ID, ws[2].ID)
	}
}

// TestPriceCappedByBid：floor(s_next/qe_j)+1 超过 bid 时被 bid 封顶，且仍满足 p*qe>=s_next。
func TestPriceCappedByBid(t *testing.T) {
	e := NewEngine()
	mustReg(t, e, "first", 20, 10, 1_000_000_000)  // s=200，seq 小
	mustReg(t, e, "second", 10, 20, 1_000_000_000) // s=200，同分 seq 大
	ws, _ := e.Auction(2, 1)
	// raw = floor(200/10)+1 = 21 > bid 20 -> 封顶 20
	if ws[0].ID != "first" || ws[0].P != 20 {
		t.Fatalf("first = %+v, want p=20", ws[0])
	}
	if ws[0].P*ws[0].Qe < 200 {
		t.Fatalf("invariant violated: %d*%d < 200", ws[0].P, ws[0].Qe)
	}
}

// TestPriceFloorSupport：s_next 很小时计价被底价 P 托底。
func TestPriceFloorSupport(t *testing.T) {
	e := NewEngine()
	mustReg(t, e, "hi", 1000, 1000, 1_000_000_000) // s = 1_000_000
	mustReg(t, e, "lo", 50, 1, 1_000_000_000)      // s = 50，bid>=P
	ws, _ := e.Auction(2, 50)
	// floor(1/1000)+1 = 1 < P 50 -> 托底 50
	if ws[0].P != 50 {
		t.Fatalf("p_hi = %d, want floor 50", ws[0].P)
	}
	if ws[1].P != 50 { // 最后一名无下一名取 P
		t.Fatalf("p_lo = %d, want P 50", ws[1].P)
	}
}

// TestDivisiblePlusOne：s_next 能被 qe_j 整除时仍 +1。
func TestDivisiblePlusOne(t *testing.T) {
	e := NewEngine()
	mustReg(t, e, "hi", 100, 20, 1_000_000_000) // s = 2000
	mustReg(t, e, "lo", 20, 50, 1_000_000_000)  // s = 1000，1000/20 = 50 整除
	ws, _ := e.Auction(2, 1)
	if ws[0].P != 51 {
		t.Fatalf("p = %d, want 51", ws[0].P)
	}
}

// TestRunnerUpBeyondK：第 K 名之后的参拍者决定第 K 名计价；最后一名无下一名取 P。
func TestRunnerUpBeyondK(t *testing.T) {
	e := NewEngine()
	mustReg(t, e, "a", 100, 100, 1_000_000_000) // s=10000
	mustReg(t, e, "b", 90, 100, 1_000_000_000)  // s=9000
	mustReg(t, e, "c", 80, 100, 1_000_000_000)  // s=8000，排在 K=2 之后
	ws, _ := e.Auction(2, 10)
	if len(ws) != 2 {
		t.Fatalf("len = %d", len(ws))
	}
	if ws[0].P != 91 { // floor(9000/100)+1
		t.Fatalf("a p = %d, want 91", ws[0].P)
	}
	if ws[1].ID != "b" || ws[1].P != 81 { // floor(8000/100)+1，c 在 K 之后仍计价
		t.Fatalf("K-th winner = %+v, want b p=81", ws[1])
	}
	ws3, _ := e.Auction(3, 10)
	if ws3[2].P != 10 {
		t.Fatalf("last p = %d, want P=10", ws3[2].P)
	}
}

// TestHoldAccumulation：同一竞价者重复中标时占用累加，最终因 a<bid 退出。
func TestHoldAccumulation(t *testing.T) {
	e := NewEngine()
	mustReg(t, e, "solo", 10, 100, 100) // 唯一参拍者，每价 P=1
	n := 0
	for {
		ws, err := e.Auction(1, 1)
		if err != nil {
			if errKind(err) != ErrNoParticipants {
				t.Fatalf("auction %d: %v", n+1, err)
			}
			break
		}
		if ws[0].P != 1 {
			t.Fatalf("price = %d, want 1", ws[0].P)
		}
		n++
	}
	// 第 i 次（1 起）参拍前 h=i-1，条件 100-(i-1) >= 10 -> i<=91。
	if n != 91 {
		t.Fatalf("won %d auctions, want 91", n)
	}
	b, _ := e.GetBidder("solo")
	if b.H != 91 || b.Available() != 9 {
		t.Fatalf("state = %+v", b)
	}
}

// TestFDiscount：f=2 无折扣，f=3 折扣 90%，f>=7 封顶 50%；q=1 时 qe 下限 1。
func TestFDiscount(t *testing.T) {
	cases := []struct {
		q, f, want int64
	}{
		{100, 0, 100},
		{100, 1, 100},
		{100, 2, 100}, // f 恰为 2 无折扣
		{100, 3, 90},  // f 恰为 3 折扣 90%
		{100, 6, 60},
		{100, 7, 50}, // f>=7 封顶 50%
		{100, 100, 50},
		{1, 3, 1}, // q=1 且折扣时 qe 取下限 1
	}
	for _, c := range cases {
		if got := effectiveQ(c.q, c.f); got != c.want {
			t.Errorf("effectiveQ(%d,%d) = %d, want %d", c.q, c.f, got, c.want)
		}
	}
}

// TestFThroughAuctions：未点击中标使 f 增长，并在后续拍卖影响冻结的 qe。
func TestFThroughAuctions(t *testing.T) {
	e := NewEngine()
	mustReg(t, e, "solo", 100, 100, 1_000_000_000)

	ws1, _ := e.Auction(1, 10)
	if ws1[0].Qe != 100 {
		t.Fatalf("qe = %d, want 100", ws1[0].Qe)
	}
	if err := e.Resolve(1, nil); err != nil { // 空点击 -> f=1
		t.Fatal(err)
	}
	b, _ := e.GetBidder("solo")
	if b.F != 1 || b.H != 0 || b.Budget != 1_000_000_000 {
		t.Fatalf("after empty resolve state=%+v", b)
	}

	ws2, _ := e.Auction(1, 10)
	// resolve1 后 q: 100-ceil(100/16)=93，f=1 无折扣。
	if ws2[0].Qe != 93 {
		t.Fatalf("qe = %d, want 93", ws2[0].Qe)
	}
	if err := e.Resolve(2, nil); err != nil { // q=93-6=87, f=2
		t.Fatal(err)
	}
	ws3, _ := e.Auction(1, 10)
	if ws3[0].Qe != 87 { // f 恰为 2 无折扣
		t.Fatalf("qe = %d, want 87", ws3[0].Qe)
	}
	if err := e.Resolve(3, nil); err != nil { // q=87-6=81, f=3
		t.Fatal(err)
	}
	ws4, _ := e.Auction(1, 10)
	if ws4[0].Qe != 72 { // f 恰为 3：floor(81*90/100)=72
		t.Fatalf("qe = %d, want 72", ws4[0].Qe)
	}
	// f 由 4 增长到 7，逐步验证折扣档：60%、70%、80%、封顶 50%。
	// resolve4: q=75,f=4；auction5: floor(75*60/100)=45... 实算 75-5=75? 见下逐档。
	if err := e.Resolve(4, nil); err != nil {
		t.Fatal(err)
	}
	ws5, _ := e.Auction(1, 10)
	if ws5[0].Qe != 60 { // q=75,f=4,floor(75*80/100)=60
		t.Fatalf("qe at f=4 = %d, want 60", ws5[0].Qe)
	}
	if err := e.Resolve(5, nil); err != nil { // q=70,f=5
		t.Fatal(err)
	}
	ws6, _ := e.Auction(1, 10)
	if ws6[0].Qe != 49 { // floor(70*70/100)=49
		t.Fatalf("qe at f=5 = %d, want 49", ws6[0].Qe)
	}
	if err := e.Resolve(6, nil); err != nil { // q=65,f=6
		t.Fatal(err)
	}
	ws7, _ := e.Auction(1, 10)
	if ws7[0].Qe != 39 { // floor(65*60/100)=39
		t.Fatalf("qe at f=6 = %d, want 39", ws7[0].Qe)
	}
	if err := e.Resolve(7, nil); err != nil { // q=60,f=7
		t.Fatal(err)
	}
	ws8, _ := e.Auction(1, 10)
	if ws8[0].Qe != 30 { // f=7 封顶 50%，floor(60*50/100)=30
		t.Fatalf("qe = %d, want 30", ws8[0].Qe)
	}
	// 点击使 f 清零，且 qe 恢复。
	if err := e.Resolve(8, []string{"solo"}); err != nil {
		t.Fatal(err)
	}
	b, _ = e.GetBidder("solo")
	if b.F != 0 {
		t.Fatalf("f after click = %d, want 0", b.F)
	}
}

// TestQeFrozenAtAuction：qe 在 Auction 时刻冻结，Resolve 不改变已出价格。
func TestQeFrozenAtAuction(t *testing.T) {
	e := NewEngine()
	// 两名赢家，另设第三名决定第二名胜价。
	mustReg(t, e, "a", 100, 100, 1_000_000_000) // s=10000
	mustReg(t, e, "b", 80, 100, 1_000_000_000)  // s=8000
	mustReg(t, e, "c", 70, 100, 1_000_000_000)  // s=7000
	ws, _ := e.Auction(3, 10)
	want := []int64{81, 71, 10}
	for i := range want {
		if ws[i].P != want[i] || ws[i].Qe != 100 {
			t.Fatalf("winner %d = %+v, want p=%d qe=100", i, ws[i], want[i])
		}
	}
	// 结算前先再办一场让 q 发生变化也不影响 1 号拍卖记录。
	if err := e.Resolve(1, []string{"a"}); err != nil {
		t.Fatal(err)
	}
	a, _ := e.GetBidder("a")
	_ = a
	st, ok := e.GetAuction(1)
	if !ok || !st.Resolved || len(st.Winners) != 3 {
		t.Fatalf("auction1 state = %+v ok=%v", st, ok)
	}
	for i, w := range st.Winners {
		if w.P != want[i] {
			t.Fatalf("recorded price of winner %d = %d, want %d", i, w.P, want[i])
		}
		if w.Qe != 100 {
			t.Fatalf("recorded qe of winner %d = %d, want 100", i, w.Qe)
		}
	}
}

// TestQualityFeedbackRounding：点击/未点击的取整方向，含 q=1000 与 q=1 边界。
func TestQualityFeedbackRounding(t *testing.T) {
	// 点击：q -> min(1000, q + floor((1000-q)/8))。
	click := func(q int64) int64 { return min64(1000, q+(1000-q)/8) }
	// 未点击：q -> max(1, q - ceil(q/16))。
	skip := func(q int64) int64 { return max64(1, q-ceilDiv(q, 16)) }

	if click(30) != 151 { // 30 + floor(970/8) = 30+121
		t.Fatalf("click(30) = %d, want 151", click(30))
	}
	if click(20) != 142 { // floor 例子取整方向：20 + floor(980/8)=20+122
		t.Fatalf("click(20) = %d, want 142", click(20))
	}
	if click(999) != 999 { // 999 + floor(1/8) = 999
		t.Fatalf("click(999) = %d, want 999", click(999))
	}
	if click(1000) != 1000 { // q=1000 封顶
		t.Fatalf("click(1000) = %d, want 1000", click(1000))
	}
	if skip(20) != 18 { // 20 - ceil(20/16) = 20-2
		t.Fatalf("skip(20) = %d, want 18", skip(20))
	}
	if skip(17) != 15 { // 17 - ceil(17/16)=17-2，向上取整一例
		t.Fatalf("skip(17) = %d, want 15", skip(17))
	}
	if skip(1) != 1 { // q=1 时扣 ceil(1/16)=1 后托底 1
		t.Fatalf("skip(1) = %d, want 1", skip(1))
	}
	if skip(16) != 15 { // 整除时恰好扣 1
		t.Fatalf("skip(16) = %d, want 15", skip(16))
	}

	// 端到端验证一次点击与一次未点击结算后的 q。
	e := NewEngine()
	mustReg(t, e, "x", 30, 999, 1_000_000_000)
	mustReg(t, e, "y", 20, 17, 1_000_000_000)
	ws, _ := e.Auction(2, 1)
	if err := e.Resolve(1, []string{"x"}); err != nil {
		t.Fatal(err)
	}
	x, _ := e.GetBidder("x")
	y, _ := e.GetBidder("y")
	if x.Q != 999 { // floor((1000-999)/8)=0
		t.Fatalf("x q = %d, want 999", x.Q)
	}
	if y.Q != 15 { // ceil(17/16)=2
		t.Fatalf("y q = %d, want 15", y.Q)
	}
	_ = ws
}

// TestResolveClicksEmptyAndAll：空点击与全部点击。
func TestResolveClicksEmptyAndAll(t *testing.T) {
	e := NewEngine()
	mustReg(t, e, "a", 100, 100, 1_000_000)
	mustReg(t, e, "b", 90, 100, 1_000_000)
	_, _ = e.Auction(2, 10)

	if err := e.Resolve(1, nil); err != nil { // 空点击合法
		t.Fatalf("empty clicks: %v", err)
	}
	a, _ := e.GetBidder("a")
	b, _ := e.GetBidder("b")
	if a.Budget != 1_000_000 || b.Budget != 1_000_000 { // 均未真实扣费
		t.Fatalf("budgets changed: a=%d b=%d", a.Budget, b.Budget)
	}
	if a.H != 0 || b.H != 0 || a.F != 1 || b.F != 1 {
		t.Fatalf("state after empty resolve a=%+v b=%+v", a, b)
	}

	// 全部点击（新一场拍卖）。
	ws2, _ := e.Auction(2, 10)
	if err := e.Resolve(2, []string{"a", "b"}); err != nil {
		t.Fatal(err)
	}
	a, _ = e.GetBidder("a")
	b, _ = e.GetBidder("b")
	if a.Budget != 1_000_000-ws2[0].P || b.Budget != 1_000_000-ws2[1].P {
		t.Fatalf("budgets after all click a=%d b=%d", a.Budget, b.Budget)
	}
	if a.F != 0 || b.F != 0 {
		t.Fatalf("f after clicks a=%d b=%d", a.F, b.F)
	}
}

// TestResolveRejections：重复结算、非赢家、不存在拍卖、重复点击 id 均被拒且不改状态。
func TestResolveRejections(t *testing.T) {
	e := NewEngine()
	mustReg(t, e, "a", 100, 100, 1_000_000)
	ws, _ := e.Auction(1, 10)
	p := ws[0].P

	before, _ := e.GetBidder("a")
	nAucBefore := e.NumAuctions()

	// 点击集合含重复 id：参数非法优先。
	if err := e.Resolve(1, []string{"a", "a"}); errKind(err) != ErrInvalidArgument {
		t.Fatalf("dup click id: %v", err)
	}
	// 非赢家。
	if err := e.Resolve(1, []string{"ghost"}); errKind(err) != ErrClickNotWinner {
		t.Fatalf("non-winner: %v", err)
	}
	// 拍卖不存在优先于已结算等其他判断。
	if err := e.Resolve(999, []string{"a"}); errKind(err) != ErrAuctionNotFound {
		t.Fatalf("missing auction: %v", err)
	}
	if n := e.NumAuctions(); n != nAucBefore {
		t.Fatalf("auction count changed %d -> %d", nAucBefore, n)
	}

	// 正常结算后重复结算被拒。
	if err := e.Resolve(1, []string{"a"}); err != nil {
		t.Fatalf("first resolve: %v", err)
	}
	if err := e.Resolve(1, []string{"a"}); errKind(err) != ErrAuctionResolved {
		t.Fatalf("double resolve: %v", err)
	}
	// 重复结算不得二次扣费或改 f/q。
	after, _ := e.GetBidder("a")
	if after.Budget != before.Budget-p || after.F != 0 || after.H != 0 {
		t.Fatalf("state changed by rejected resolve: before=%+v after=%+v", before, after)
	}
	st, _ := e.GetAuction(1)
	if !st.Resolved {
		t.Fatalf("auction should be resolved")
	}
}

// TestRegisterRejections 与参数非法优先级。
func TestRegisterRejections(t *testing.T) {
	e := NewEngine()
	bad := []struct {
		id             string
		bid, q, budget int64
	}{
		{"", 10, 10, 10},
		{"x", 0, 10, 10},
		{"x", 1_000_001, 10, 10},
		{"x", 10, 0, 10},
		{"x", 10, 1001, 10},
		{"x", 10, 10, 0},
		{"x", 10, 10, 1_000_000_000_001},
	}
	for _, c := range bad {
		if err := e.Register(c.id, c.bid, c.q, c.budget); errKind(err) != ErrInvalidArgument {
			t.Fatalf("Register(%q,%d,%d,%d) = %v", c.id, c.bid, c.q, c.budget, err)
		}
	}
	if e.NumBidders() != 0 {
		t.Fatalf("rejected registers changed state: %d bidders", e.NumBidders())
	}
	if err := e.Register("dup", 10, 10, 10); err != nil {
		t.Fatal(err)
	}
	if err := e.Register("dup", 10, 10, 10); errKind(err) != ErrBidderExists {
		t.Fatalf("dup register: %v", err)
	}
	if e.NumBidders() != 1 {
		t.Fatalf("num bidders = %d, want 1", e.NumBidders())
	}

	// Auction 参数非法不产生拍卖号。
	if _, err := e.Auction(0, 10); errKind(err) != ErrInvalidArgument {
		t.Fatalf("K=0: %v", err)
	}
	if _, err := e.Auction(101, 10); errKind(err) != ErrInvalidArgument {
		t.Fatalf("K=101: %v", err)
	}
	if _, err := e.Auction(1, 0); errKind(err) != ErrInvalidArgument {
		t.Fatalf("P=0: %v", err)
	}
	if _, err := e.Auction(1, 1_000_001); errKind(err) != ErrInvalidArgument {
		t.Fatalf("P big: %v", err)
	}
	if _, err := e.Auction(1, 11); errKind(err) != ErrNoParticipants {
		t.Fatalf("want NoParticipants, got %v", err)
	}
	if e.NumAuctions() != 0 {
		t.Fatalf("rejected auctions created ids: %d", e.NumAuctions())
	}
}

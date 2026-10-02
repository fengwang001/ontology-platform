package booking

import (
	"errors"
	"math/rand"
	"sync"
	"sync/atomic"
	"testing"
)

func mustBook(t *testing.T, b *Book, id, owner, slot, size, tier int64) {
	t.Helper()
	if err := b.Book(id, owner, slot, size, tier); err != nil {
		t.Fatalf("Book(id=%d owner=%d slot=%d size=%d tier=%d) 意外被拒: %v", id, owner, slot, size, tier, err)
	}
}

func mustBookErr(t *testing.T, b *Book, id, owner, slot, size, tier int64, want error) {
	t.Helper()
	if err := b.Book(id, owner, slot, size, tier); !errors.Is(err, want) {
		t.Fatalf("Book(id=%d owner=%d slot=%d size=%d tier=%d) 错误=%v, 期望 %v", id, owner, slot, size, tier, err, want)
	}
}

func mustSettle(t *testing.T, b *Book, slot int64, arrivals ...Arrival) *SettleResult {
	t.Helper()
	res, err := b.Settle(slot, arrivals)
	if err != nil {
		t.Fatalf("Settle(slot=%d arrivals=%v) 意外被拒: %v", slot, arrivals, err)
	}
	return res
}

func mustSettleErr(t *testing.T, b *Book, slot int64, want error, arrivals ...Arrival) {
	t.Helper()
	if _, err := b.Settle(slot, arrivals); !errors.Is(err, want) {
		t.Fatalf("Settle(slot=%d arrivals=%v) 错误=%v, 期望 %v", slot, arrivals, err, want)
	}
}

func evictOf(t *testing.T, res *SettleResult, id int64) Eviction {
	t.Helper()
	for _, ev := range res.Evictions {
		if ev.ID == id {
			return ev
		}
	}
	t.Fatalf("SettleResult 中缺少 id=%d 的记录", id)
	return Eviction{}
}

func checkEviction(t *testing.T, res *SettleResult, id, arrived, served, evicted, comp int64) {
	t.Helper()
	ev := evictOf(t, res, id)
	if ev.Arrived != arrived || ev.Served != served || ev.Evicted != evicted || ev.Comp != comp {
		t.Fatalf("id=%d 实到/服务/挤出/补偿 = %d/%d/%d/%d, 期望 %d/%d/%d/%d",
			id, ev.Arrived, ev.Served, ev.Evicted, ev.Comp, arrived, served, evicted, comp)
	}
}

func checkState(t *testing.T, b *Book, o, limit, lastSettled, seq int64, windowLen int) {
	t.Helper()
	if got := b.CurrentO(); got != o {
		t.Fatalf("CurrentO=%d, 期望 %d", got, o)
	}
	if got := b.Limit(); got != limit {
		t.Fatalf("Limit=%d, 期望 %d", got, limit)
	}
	if got := b.LastSettled(); got != lastSettled {
		t.Fatalf("LastSettled=%d, 期望 %d", got, lastSettled)
	}
	if got := b.Seq(); got != seq {
		t.Fatalf("Seq=%d, 期望 %d", got, seq)
	}
	if got := b.WindowLen(); got != windowLen {
		t.Fatalf("WindowLen=%d, 期望 %d", got, windowLen)
	}
}

// TestWorkedExample 逐步复现题目给出的示例。
func TestWorkedExample(t *testing.T) {
	b, err := New(10, 2, 3000, 50)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	checkState(t, b, 0, 10, -1, 0, 0)

	mustBook(t, b, 1, 10, 1, 6, 1) // a, X
	mustBook(t, b, 2, 11, 1, 4, 0) // b, Y：6+4 恰等于上限 10
	mustBookErr(t, b, 3, 12, 1, 1, 0, ErrOverLimit)

	res := mustSettle(t, b, 1, Arrival{1, 3}, Arrival{2, 4})
	if res.Arrived != 7 || res.TotalComp != 0 {
		t.Fatalf("Settle(1): Arrived=%d TotalComp=%d, 期望 7/0", res.Arrived, res.TotalComp)
	}
	checkEviction(t, res, 1, 3, 3, 0, 0)
	checkEviction(t, res, 2, 4, 4, 0, 0)
	// 窗口 [(10,7)]，O=min(3000, floor(3*10000/10))=3000，上限 13。
	checkState(t, b, 3000, 13, 1, 2, 1)

	mustBook(t, b, 4, 12, 2, 7, 1) // d, Z
	mustBook(t, b, 5, 10, 2, 6, 0) // e, X：7+6=13 恰等于上限
	mustBookErr(t, b, 6, 12, 2, 1, 0, ErrOverLimit)

	res = mustSettle(t, b, 2, Arrival{4, 7}, Arrival{5, 6})
	// A=13>10，excess=3，d(tier 1) 先挤 t=min(7,3)=3，补偿 3*50*(1+0)=150。
	if res.Arrived != 13 || res.TotalComp != 150 {
		t.Fatalf("Settle(2): Arrived=%d TotalComp=%d, 期望 13/150", res.Arrived, res.TotalComp)
	}
	checkEviction(t, res, 4, 7, 4, 3, 150)
	checkEviction(t, res, 5, 6, 6, 0, 0)
	if got := b.K(12); got != 1 {
		t.Fatalf("K(Z)=%d, 期望 1", got)
	}
	// 窗口 [(10,7),(13,13)]，O=floor(3*10000/23)=1304，上限 11。
	checkState(t, b, 1304, 11, 2, 4, 2)

	mustBook(t, b, 7, 12, 3, 11, 1) // g, Z

	res = mustSettle(t, b, 3, Arrival{7, 11})
	// A=11，excess=1，挤出 g 的 1 个单位，补偿 1*50*(1+min(1,3))=100。
	if res.TotalComp != 100 {
		t.Fatalf("Settle(3): TotalComp=%d, 期望 100", res.TotalComp)
	}
	checkEviction(t, res, 7, 11, 10, 1, 100)
	if got := b.K(12); got != 2 {
		t.Fatalf("K(Z)=%d, 期望 2", got)
	}
	// 窗口滑出时段 1，变为 [(13,13),(11,11)]，O=0。
	checkState(t, b, 0, 10, 3, 5, 2)
}

// TestConfigValidation 构造参数越界时整体拒绝。
func TestConfigValidation(t *testing.T) {
	bad := [][4]int64{
		{0, 1, 0, 0}, {1_000_001, 1, 0, 0}, // C 越界
		{1, 0, 0, 0}, {1, 65, 0, 0}, // W 越界
		{1, 1, -1, 0}, {1, 1, 10_001, 0}, // Omax 越界
		{1, 1, 0, -1}, {1, 1, 0, 1_000_001}, // R 越界
	}
	for _, cfg := range bad {
		if _, err := New(cfg[0], cfg[1], cfg[2], cfg[3]); !errors.Is(err, ErrInvalidConfig) {
			t.Fatalf("New%v 错误=%v, 期望 ErrInvalidConfig", cfg, err)
		}
	}
	good := [][4]int64{{1, 1, 0, 0}, {1_000_000, 64, 10_000, 1_000_000}}
	for _, cfg := range good {
		if _, err := New(cfg[0], cfg[1], cfg[2], cfg[3]); err != nil {
			t.Fatalf("New%v 意外被拒: %v", cfg, err)
		}
	}
}

// TestOversellCap 超卖系数取 min(Omax, ...) 封顶。
func TestOversellCap(t *testing.T) {
	b, _ := New(10, 4, 1000, 0)
	mustBook(t, b, 1, 1, 1, 10, 0)
	mustSettle(t, b, 1) // A=0，缺席率 100%，但 O 封顶为 Omax=1000
	checkState(t, b, 1000, 11, 1, 1, 1)
}

// TestOversellFloor 窗口内缺席率向下取整。
func TestOversellFloor(t *testing.T) {
	b, _ := New(10, 4, 10_000, 0)
	mustBook(t, b, 1, 1, 1, 3, 0)
	mustSettle(t, b, 1, Arrival{1, 1})
	// O = floor((3-1)*10000/3) = floor(6666.67) = 6666
	checkState(t, b, 6666, 16, 1, 1, 1)
}

// TestEmptySlotOccupiesWindow 没有预订的时段也追加 (0,0) 占窗口位置，稀释窗口。
func TestEmptySlotOccupiesWindow(t *testing.T) {
	b, _ := New(10, 2, 10_000, 0)
	mustBook(t, b, 1, 1, 1, 6, 0)
	mustSettle(t, b, 1, Arrival{1, 2})
	checkState(t, b, 6666, 16, 1, 1, 1) // 窗口 [(6,2)]
	mustSettle(t, b, 2)                 // 无预订时段，追加 (0,0)
	checkState(t, b, 6666, 16, 2, 1, 2) // 窗口 [(6,2),(0,0)]
	mustSettle(t, b, 3)                 // 再追加 (0,0)，(6,2) 被挤出窗口
	checkState(t, b, 0, 10, 3, 1, 2)    // 窗口 [(0,0),(0,0)]
}

// TestWindowSlideOut 窗口滑出最旧记录使 O 回落。
func TestWindowSlideOut(t *testing.T) {
	b, _ := New(10, 1, 10_000, 0)
	mustBook(t, b, 1, 1, 1, 6, 0)
	mustSettle(t, b, 1, Arrival{1, 2})
	checkState(t, b, 6666, 16, 1, 1, 1) // 窗口 [(6,2)]
	mustBook(t, b, 2, 1, 2, 16, 0)      // 上限 16，恰好订满
	mustSettle(t, b, 2, Arrival{2, 16})
	checkState(t, b, 0, 10, 2, 2, 1) // 窗口滑为 [(16,16)]，O 回落到 0
}

// TestNonRetroactive O 的变化不追溯已有预订。
func TestNonRetroactive(t *testing.T) {
	b, _ := New(10, 4, 5000, 0)
	mustBook(t, b, 1, 1, 1, 10, 0) // O=0，上限 10
	mustSettle(t, b, 1, Arrival{1, 5})
	checkState(t, b, 5000, 15, 1, 1, 1) // O=5000，上限 15
	mustBook(t, b, 2, 1, 2, 15, 0)      // 按当时上限 15 订满
	mustSettle(t, b, 2, Arrival{2, 15})
	// 窗口 [(10,5),(15,15)]，O=floor(5*10000/25)=2000，上限降为 12。
	checkState(t, b, 2000, 12, 2, 2, 2)
	if got := b.Booked(2); got != 15 {
		t.Fatalf("Booked(2)=%d, 期望 15（不追溯）", got)
	}
	mustBook(t, b, 3, 1, 3, 12, 0) // 新预订按新上限 12
	mustBookErr(t, b, 4, 1, 3, 1, 0, ErrOverLimit)
}

// TestSkippedSlots 跳过未结算的时段，其不进入窗口。
func TestSkippedSlots(t *testing.T) {
	b, _ := New(10, 4, 0, 0)
	mustBook(t, b, 1, 1, 5, 10, 0) // 直接预订时段 5
	mustSettle(t, b, 5, Arrival{1, 10})
	checkState(t, b, 0, 10, 5, 1, 1) // 只有时段 5 进入窗口
	mustBookErr(t, b, 2, 1, 3, 1, 0, ErrSlotSettled)
	mustBookErr(t, b, 3, 1, 5, 1, 0, ErrSlotSettled)
	mustBook(t, b, 4, 1, 6, 1, 0) // 时段 6 仍可预订
}

// TestNoEvictionUnderCapacity 到场量不超过 C 时无挤出。
func TestNoEvictionUnderCapacity(t *testing.T) {
	b, _ := New(10, 2, 0, 50)
	mustBook(t, b, 1, 1, 1, 6, 1)
	mustBook(t, b, 2, 2, 1, 4, 0)
	res := mustSettle(t, b, 1, Arrival{1, 3}, Arrival{2, 4}) // A=7 ≤ C=10
	if res.TotalComp != 0 {
		t.Fatalf("TotalComp=%d, 期望 0", res.TotalComp)
	}
	checkEviction(t, res, 1, 3, 3, 0, 0)
	checkEviction(t, res, 2, 4, 4, 0, 0)
	if b.K(1) != 0 || b.K(2) != 0 {
		t.Fatalf("k 不应变化: K(1)=%d K(2)=%d", b.K(1), b.K(2))
	}
}

// TestEvictionOrder 挤出先看 tier（数值大者先挤）再看预订序号（后预订先挤），含部分挤出。
func TestEvictionOrder(t *testing.T) {
	b, _ := New(5, 4, 10_000, 10)
	mustBook(t, b, 1, 20, 1, 5, 0)
	mustSettle(t, b, 1)            // 窗口 [(5,0)]，O=10000，上限 10
	mustBook(t, b, 2, 21, 2, 4, 0) // tier 0，序号小
	mustBook(t, b, 3, 22, 2, 4, 2) // tier 2，序号中
	mustBook(t, b, 4, 23, 2, 2, 2) // tier 2，序号大（后预订）
	res := mustSettle(t, b, 2, Arrival{2, 4}, Arrival{3, 4}, Arrival{4, 2})
	// A=10，excess=5。顺序：id4(tier2,后订) → id3(tier2,先订) → id2(tier0)。
	// id4 挤 2，id3 挤 3（部分挤出），id2 不挤。
	if res.Arrived != 10 || res.TotalComp != 50 {
		t.Fatalf("Arrived=%d TotalComp=%d, 期望 10/50", res.Arrived, res.TotalComp)
	}
	checkEviction(t, res, 4, 2, 0, 2, 20)
	checkEviction(t, res, 3, 4, 1, 3, 30)
	checkEviction(t, res, 2, 4, 4, 0, 0)
}

// TestSameOwnerMultiEviction 同一租户一次 Settle 被挤出多个预订：
// k 只加一，且所有补偿都用本次 Settle 开始前的 k。
func TestSameOwnerMultiEviction(t *testing.T) {
	b, _ := New(4, 8, 10_000, 10)
	mustBook(t, b, 1, 20, 1, 4, 0)
	mustSettle(t, b, 1) // 窗口 [(4,0)]，O=10000，上限 8
	mustBook(t, b, 2, 21, 2, 3, 1)
	mustBook(t, b, 3, 21, 2, 3, 1)
	mustBook(t, b, 4, 21, 2, 2, 1)
	res := mustSettle(t, b, 2, Arrival{2, 3}, Arrival{3, 3}, Arrival{4, 2})
	// A=8，excess=4。同 tier 按序号降序：id4 挤 2、id3 挤 2、id2 不挤。
	// 三个预订同属租户 21，补偿均用 Settle 前的 k=0。
	if res.TotalComp != 40 {
		t.Fatalf("TotalComp=%d, 期望 40", res.TotalComp)
	}
	checkEviction(t, res, 4, 2, 0, 2, 20)
	checkEviction(t, res, 3, 3, 1, 2, 20)
	checkEviction(t, res, 2, 3, 3, 0, 0)
	if got := b.K(21); got != 1 {
		t.Fatalf("K(21)=%d, 期望 1（一次 Settle 至多加一）", got)
	}
	// 窗口 [(4,0),(8,8)]，O=3333，上限 5。再次挤出时补偿用 k=1。
	mustBook(t, b, 5, 21, 3, 5, 1)
	res = mustSettle(t, b, 3, Arrival{5, 5})
	checkEviction(t, res, 5, 5, 4, 1, 20) // 1*10*(1+min(1,3))=20
	if got := b.K(21); got != 2 {
		t.Fatalf("K(21)=%d, 期望 2", got)
	}
}

// TestCompMultiplierCapped k 超过 3 后补偿倍数封顶为 4。
func TestCompMultiplierCapped(t *testing.T) {
	b, _ := New(2, 64, 10_000, 10)
	const x, y = 21, 20
	mustBook(t, b, 1, y, 1, 2, 0)
	mustSettle(t, b, 1) // (2,0)：O=10000，上限 4
	mustBook(t, b, 2, y, 2, 4, 0)
	mustSettle(t, b, 2) // (4,0)：O=10000，上限 4

	// 之后每两个时段一组：一个空到场时段抬高超卖系数，一个 X 全到场时段触发挤出。
	// 各次 X 被挤的 t 与期望补偿（倍数 1+min(k,3)，k 为 Settle 前的值）。
	type step struct {
		bookID   int64
		slot     int64
		size     int64
		arrive   int64
		wantComp int64
		wantK    int64
	}
	steps := []step{
		{3, 3, 4, 4, 20, 1},   // t=2，k=0 → 2*10*1
		{5, 5, 3, 3, 20, 2},   // t=1，k=1 → 1*10*2
		{7, 7, 3, 3, 30, 3},   // t=1，k=2 → 1*10*3
		{9, 9, 3, 3, 40, 4},   // t=1，k=3 → 1*10*4
		{11, 11, 3, 3, 40, 5}, // t=1，k=4 → 倍数封顶 4
	}
	emptyID, emptySlot := int64(4), int64(4)
	for i, s := range steps {
		if i > 0 { // 在每个挤出时段前插入一个 Y 预订但零到场的时段
			mustBook(t, b, emptyID, y, emptySlot, 3, 0)
			mustSettle(t, b, emptySlot)
			emptyID += 2
			emptySlot += 2
		}
		mustBook(t, b, s.bookID, x, s.slot, s.size, 1)
		res := mustSettle(t, b, s.slot, Arrival{s.bookID, s.arrive})
		if res.TotalComp != s.wantComp {
			t.Fatalf("Settle(%d): TotalComp=%d, 期望 %d", s.slot, res.TotalComp, s.wantComp)
		}
		if got := b.K(x); got != s.wantK {
			t.Fatalf("Settle(%d) 后 K(X)=%d, 期望 %d", s.slot, got, s.wantK)
		}
	}
}

// TestEvictedCountedInArrivals 被挤出的单位仍计入到场量 A。
func TestEvictedCountedInArrivals(t *testing.T) {
	b, _ := New(5, 2, 10_000, 0)
	mustBook(t, b, 1, 20, 1, 5, 0)
	mustSettle(t, b, 1) // (5,0)：O=10000，上限 10
	mustBook(t, b, 2, 21, 2, 10, 0)
	res := mustSettle(t, b, 2, Arrival{2, 10})
	// A=10（含被挤出的 5 个单位），excess=5。
	if res.Arrived != 10 {
		t.Fatalf("Arrived=%d, 期望 10（含被挤出单位）", res.Arrived)
	}
	checkEviction(t, res, 2, 10, 5, 5, 0)
	// 窗口 [(5,0),(10,10)]：O=floor(5*10000/15)=3333，A 按含挤出单位计。
	checkState(t, b, 3333, 6, 2, 2, 2)
}

// TestRejectionKeepsState 被拒绝的操作不改变任何状态，且不消耗序号；
// 拒绝原因按固定顺序只报第一个。
func TestRejectionKeepsState(t *testing.T) {
	b, _ := New(10, 2, 1000, 50)
	mustBook(t, b, 1, 1, 1, 10, 0)
	mustBook(t, b, 2, 2, 2, 5, 0)
	mustSettle(t, b, 1, Arrival{1, 5})
	// 窗口 [(10,5)]，O=min(1000,5000)=1000，上限 11。
	snapshot := func() [7]int64 {
		return [7]int64{b.CurrentO(), b.Limit(), b.LastSettled(), b.Seq(),
			int64(b.WindowLen()), b.Booked(2), b.K(1)}
	}
	before := snapshot()

	// Book：参数非法
	mustBookErr(t, b, 3, 1, 2, 0, 0, ErrInvalidArgument)         // size=0
	mustBookErr(t, b, 3, 1, 2, 1, 3, ErrInvalidArgument)         // tier=3
	mustBookErr(t, b, -1, 1, 2, 1, 0, ErrInvalidArgument)        // id<0
	mustBookErr(t, b, 3, 1, 1_000_001, 1, 0, ErrInvalidArgument) // slot 越界
	mustBookErr(t, b, 3, 1, 2, 1_000_001, 0, ErrInvalidArgument) // size 越界
	// Book：id 重复（优先于时段已结算）
	mustBookErr(t, b, 1, 1, 2, 1, 0, ErrDuplicateID)
	mustBookErr(t, b, 1, 1, 1, 1, 0, ErrDuplicateID)
	// Book：时段已结算
	mustBookErr(t, b, 3, 1, 1, 1, 0, ErrSlotSettled)
	mustBookErr(t, b, 3, 1, 0, 1, 0, ErrSlotSettled)
	// Book：超出上限（已订 5，上限 11，再订 7 超出）
	mustBookErr(t, b, 3, 1, 2, 7, 0, ErrOverLimit)

	// Settle：参数非法（优先于时段回退）
	mustSettleErr(t, b, -1, ErrInvalidArgument)
	mustSettleErr(t, b, 2, ErrInvalidArgument, Arrival{999, 1})              // id 不属于该时段
	mustSettleErr(t, b, 2, ErrInvalidArgument, Arrival{1, 1})                // id 属于时段 1
	mustSettleErr(t, b, 2, ErrInvalidArgument, Arrival{2, 6})                // 到场量越界
	mustSettleErr(t, b, 2, ErrInvalidArgument, Arrival{2, -1})               // 到场量越界
	mustSettleErr(t, b, 2, ErrInvalidArgument, Arrival{2, 3}, Arrival{2, 1}) // id 重复
	mustSettleErr(t, b, 1, ErrInvalidArgument, Arrival{999, 1})              // 参数非法优先于回退
	// Settle：时段回退
	mustSettleErr(t, b, 1, ErrSlotRollback, Arrival{1, 1})
	mustSettleErr(t, b, 0, ErrSlotRollback)

	if after := snapshot(); after != before {
		t.Fatalf("被拒绝的操作改变了状态: 前=%v 后=%v", before, after)
	}
}

// TestConcurrent 并发调用下结果等价于某个串行顺序，且关键不变量保持。
func TestConcurrent(t *testing.T) {
	b, _ := New(50, 8, 5000, 10)
	var wg sync.WaitGroup
	var settleSlot atomic.Int64

	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(int64(g)))
			for i := 0; i < 200; i++ {
				id := int64(g*1000 + i)
				_ = b.Book(id, int64(g), rng.Int63n(40), 1+rng.Int63n(5), rng.Int63n(3))
			}
		}(g)
	}
	for g := 0; g < 2; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 30; i++ {
				slot := settleSlot.Add(1) - 1
				_, _ = b.Settle(slot, nil) // 可能被回退拒绝，属正常竞争
			}
		}()
	}
	for g := 0; g < 4; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				_ = b.CurrentO()
				_ = b.Limit()
				_ = b.LastSettled()
				_ = b.WindowLen()
				_ = b.K(int64(i % 8))
				_ = b.Booked(int64(i % 40))
				_ = b.Seq()
			}
		}()
	}
	wg.Wait()

	if got := b.WindowLen(); got > 8 {
		t.Fatalf("WindowLen=%d 超过 W=8", got)
	}
	if got := b.Seq(); got != int64(len(b.byID)) {
		t.Fatalf("Seq=%d 与成功预订数 %d 不一致", got, len(b.byID))
	}
	for slot, rs := range b.bySlot {
		var sum int64
		for _, r := range rs {
			sum += r.size
		}
		if b.bookedBy[slot] != sum {
			t.Fatalf("时段 %d 预订量台账 %d 与明细 %d 不一致", slot, b.bookedBy[slot], sum)
		}
		if slot <= b.lastSettled {
			var arrived, evicted int64
			for _, r := range rs {
				arrived += r.arrived
				evicted += r.evicted
			}
			if want := max(arrived-b.capC, 0); evicted != want {
				t.Fatalf("时段 %d 挤出总量 %d, 期望 max(0,A-C)=%d", slot, evicted, want)
			}
			if arrived-evicted > b.capC {
				t.Fatalf("时段 %d 实到服务量 %d 超过 C=%d", slot, arrived-evicted, b.capC)
			}
		}
	}
}

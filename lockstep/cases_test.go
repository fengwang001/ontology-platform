package lockstep

import (
	"errors"
	"sync"
	"testing"

	"ontology/turn"
)

func TestRejectOrder(t *testing.T) {
	e := New(2, 100, 1, 0, 2, 0)

	if err := e.Submit(5, 0, 1, nil); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("empty cmd err=%v", err)
	}
	if err := e.Submit(5, 0, 1, make([]byte, 65)); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("65B cmd err=%v", err)
	}
	if err := e.Submit(5, -1, 1, []byte("z")); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("bad p err=%v", err)
	}
	if err := e.Submit(5, 2, 1, []byte("z")); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("p>=N err=%v", err)
	}
	if err := e.Submit(5, 0, 0, []byte("z")); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("bad k err=%v", err)
	}

	mustSubmit(t, e, op{now: 50, p: 0, k: 1, cmd: []byte("a")}, nil)
	if err := e.Submit(49, 0, 1, []byte("a")); !errors.Is(err, ErrClockRewind) {
		t.Fatalf("rewind err=%v", err)
	}

	// 迟到判定发生在入口处理之后：now=150 先在 100 超时结算回合1，随后 k=1 迟到。
	if err := e.Submit(150, 0, 1, []byte("a")); !errors.Is(err, ErrLate) {
		t.Fatalf("late err=%v", err)
	}
	if e.Cur() != 2 || e.Deadline() != 200 {
		t.Fatalf("cur=%d dl=%d, want 2/200（回合1于100结算）", e.Cur(), e.Deadline())
	}

	// 超前优先于重复。
	if err := e.Submit(150, 1, 99, []byte("z")); !errors.Is(err, ErrAhead) {
		t.Fatalf("ahead err=%v", err)
	}

	// 窗口两端取等：k=cur=2 与 k=cur+A=3 均合法。
	mustSubmit(t, e, op{now: 150, p: 0, k: 2, cmd: []byte("b")}, nil)
	mustSubmit(t, e, op{now: 150, p: 0, k: 3, cmd: []byte("c")}, nil)
	if err := e.Submit(150, 0, 4, []byte("d")); !errors.Is(err, ErrAhead) {
		t.Fatalf("ahead+1 err=%v", err)
	}

	// 重复以首次为准。
	if err := e.Submit(150, 0, 2, []byte("B2")); !errors.Is(err, ErrDuplicate) {
		t.Fatalf("dup err=%v", err)
	}

	// 被拒操作不推进时钟：200 之后 200 仍合法（非回退）。
	// p1 交2、3：回合2、3 先后在 ts=150 到齐并连锁结算（p0 已提前交3）。
	mustSubmit(t, e, op{now: 150, p: 1, k: 2, cmd: []byte("q")}, nil)
	if e.Cur() != 3 {
		t.Fatalf("cur=%d want 3", e.Cur())
	}
	mustSubmit(t, e, op{now: 150, p: 1, k: 3, cmd: []byte("r")}, nil)
	if e.Cur() != 4 {
		t.Fatalf("cur=%d want 4 (回合3提前交齐，连锁)", e.Cur())
	}
	r2 := e.Log(2)[0]
	if r2.TS != 150 {
		t.Fatalf("r2 ts=%d want 150", r2.TS)
	}
}

// 超时取等：now==dl 即以 ts=dl 结算；一次 Advance 可追赶多个回合。
func TestTimeoutEqualityAndMultiCatchup(t *testing.T) {
	e := New(1, 10, 0, 0, 1, 100)
	recs, err := e.Advance(110)
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 1 || recs[0].TS != 110 || recs[0].Turn != 1 {
		t.Fatalf("recs=%+v", recs)
	}
	if e.Deadline() != 120 {
		t.Fatalf("dl=%d want 120", e.Deadline())
	}

	recs, err = e.Advance(145)
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 3 {
		t.Fatalf("got %d recs, want 3", len(recs))
	}
	for i, r := range recs {
		if r.Turn != i+2 || r.TS != int64(120+i*10) {
			t.Fatalf("rec[%d]=%+v", i, r)
		}
		assertSlot(t, r, 0, turn.Blank, "")
	}
	if e.Cur() != 5 || e.Deadline() != 150 {
		t.Fatalf("cur=%d dl=%d", e.Cur(), e.Deadline())
	}
}

// 提前提交引起连锁结算：每次到齐后已提前交齐的后续回合以同一 ts 连锁。
func TestAheadChainSettlement(t *testing.T) {
	e := New(2, 100, 3, 0, 10, 0)
	mustSubmit(t, e, op{now: 1, p: 0, k: 1, cmd: []byte("a1")}, nil)
	mustSubmit(t, e, op{now: 1, p: 0, k: 2, cmd: []byte("a2")}, nil)
	mustSubmit(t, e, op{now: 1, p: 0, k: 3, cmd: []byte("a3")}, nil)
	mustSubmit(t, e, op{now: 2, p: 1, k: 1, cmd: []byte("b1")}, nil)
	if e.Cur() != 2 {
		t.Fatalf("cur=%d want 2", e.Cur())
	}
	mustSubmit(t, e, op{now: 3, p: 1, k: 2, cmd: []byte("b2")}, nil)
	if e.Cur() != 3 {
		t.Fatalf("cur=%d want 3", e.Cur())
	}
	mustSubmit(t, e, op{now: 4, p: 1, k: 3, cmd: []byte("b3")}, nil)
	if e.Cur() != 4 {
		t.Fatalf("cur=%d want 4 (回合3以ts=4连锁)", e.Cur())
	}
	mustSubmit(t, e, op{now: 5, p: 0, k: 4, cmd: []byte("a4")}, nil)
	mustSubmit(t, e, op{now: 6, p: 1, k: 4, cmd: []byte("b4")}, nil)
	recs := e.Log(1)
	if len(recs) != 4 {
		t.Fatalf("settled %d turns, want 4: %+v", len(recs), recs)
	}
	wantTS := []int64{2, 3, 4, 6}
	for i, r := range recs {
		if r.TS != wantTS[i] {
			t.Fatalf("turn %d ts=%d want %d", r.Turn, r.TS, wantTS[i])
		}
		assertSlot(t, r, 0, turn.Live, []string{"a1", "a2", "a3", "a4"}[i])
		assertSlot(t, r, 1, turn.Live, []string{"b1", "b2", "b3", "b4"}[i])
	}
}

// m 恰等于 R 仍重复；R+1 空填。从未实交者即使 m≤R 也只能空填。
func TestRepeatBoundaryAndNeverLive(t *testing.T) {
	e := New(2, 10, 0, 2, 100, 0)
	mustSubmit(t, e, op{now: 1, p: 0, k: 1, cmd: []byte("a")}, nil)
	mustSubmit(t, e, op{now: 1, p: 1, k: 1, cmd: []byte("b")}, nil)
	if _, err := e.Advance(50); err != nil {
		t.Fatal(err)
	}
	recs := e.Log(1)
	if len(recs) != 5 {
		t.Fatalf("got %d recs", len(recs))
	}
	assertSlot(t, recs[1], 0, turn.Repeat, "a") // m=1
	assertSlot(t, recs[2], 0, turn.Repeat, "a") // m=2==R
	assertSlot(t, recs[3], 0, turn.Blank, "")   // m=3>R
	assertSlot(t, recs[4], 0, turn.Blank, "")

	e2 := New(1, 10, 0, 5, 100, 0)
	if _, err := e2.Advance(10); err != nil {
		t.Fatal(err)
	}
	rr := e2.Log(1)[0]
	assertSlot(t, rr, 0, turn.Blank, "") // 无最近实交，R 再大也空填
}

// Kd<=R：达到掉线阈值的当回合仍可能得到重复填充。
func TestDropBeforeRepeatLimit(t *testing.T) {
	e := New(2, 10, 0, 5, 2, 0)
	mustSubmit(t, e, op{now: 1, p: 0, k: 1, cmd: []byte("a")}, nil)
	mustSubmit(t, e, op{now: 1, p: 1, k: 1, cmd: []byte("b")}, nil)
	if _, err := e.Advance(30); err != nil {
		t.Fatal(err)
	}
	recs := e.Log(1)
	if len(recs) != 3 {
		t.Fatalf("got %d", len(recs))
	}
	assertSlot(t, recs[1], 1, turn.Repeat, "b") // m=1
	assertSlot(t, recs[2], 1, turn.Repeat, "b") // m=2==Kd 且 <=R
	if e.Active(1) {
		t.Fatal("p1 should be dropped at m==Kd=2 while still receiving repeat fill")
	}
}

// 全员非活跃时空集不算到齐，只能靠超时推进；恢复后已存输入按实交。
func TestAllInactiveNeverReady(t *testing.T) {
	e := New(1, 10, 2, 0, 1, 0)
	if _, err := e.Advance(10); err != nil {
		t.Fatal(err)
	}
	if e.Active(0) {
		t.Fatal("p0 should be inactive")
	}
	r1 := e.Log(1)[0]
	assertSlot(t, r1, 0, turn.Blank, "")

	// 非活跃玩家提交：恢复活跃但 m 不变；他交了回合2，活跃集(仅他)到齐应立即结算。
	mustSubmit(t, e, op{now: 11, p: 0, k: 2, cmd: []byte("z")}, nil)
	if !e.Active(0) {
		t.Fatal("submit should reactivate")
	}
	if e.Cur() != 3 {
		t.Fatalf("cur=%d want 3 (reactivated submit makes sole active ready)", e.Cur())
	}
	r2 := e.Log(2)[0]
	if r2.TS != 11 {
		t.Fatalf("r2 ts=%d want 11", r2.TS)
	}
	assertSlot(t, r2, 0, turn.Live, "z")
	if e.Miss(0) != 0 {
		t.Fatalf("m=%d want 0", e.Miss(0))
	}
}

// 恢复活跃后因旧回合缺失再次掉线。
func TestReactivateAndRedrop(t *testing.T) {
	// 按文档续例参数：T=100 A=2 R=1 Kd=3。
	e := New(2, 100, 2, 1, 3, 0)
	mustSubmit(t, e, op{now: 10, p: 0, k: 1, cmd: []byte("a")}, nil)
	mustSubmit(t, e, op{now: 20, p: 1, k: 1, cmd: []byte("b")}, nil) // 回合1@20, dl=120
	if _, err := e.Advance(320); err != nil {                        // 回合2@120 m1=1重复;3@220 m1=2空;4@320 m1=3空且掉线
		t.Fatal(err)
	}
	if e.Active(1) || e.Miss(1) != 3 {
		t.Fatalf("p1 active=%v m=%d, want inactive/3", e.Active(1), e.Miss(1))
	}
	mustSubmit(t, e, op{now: 330, p: 1, k: 7, cmd: []byte("y")}, nil) // 窗口[5,7]，恢复活跃 m 仍 3
	if !e.Active(1) || e.Miss(1) != 3 {
		t.Fatalf("reactivate active=%v m=%d", e.Active(1), e.Miss(1))
	}
	mustSubmit(t, e, op{now: 340, p: 0, k: 5, cmd: []byte("e")}, nil) // p1 未交5，不到齐
	if e.Cur() != 5 {
		t.Fatalf("cur=%d want 5", e.Cur())
	}
	if _, err := e.Advance(420); err != nil { // 回合5@420：p1 m=4 空填，再次掉线
		t.Fatal(err)
	}
	if e.Active(1) {
		t.Fatal("p1 redrop expected")
	}
	r5 := e.Log(5)[0]
	assertSlot(t, r5, 1, turn.Blank, "")
	if e.Miss(1) != 4 {
		t.Fatalf("m=%d want 4", e.Miss(1))
	}
	mustSubmit(t, e, op{now: 430, p: 0, k: 6, cmd: []byte("f")}, nil) // 活跃仅p0，回合6@430
	mustSubmit(t, e, op{now: 440, p: 0, k: 7, cmd: []byte("g")}, nil) // 回合7@440；p1 的 y 实交
	r7 := e.Log(7)[0]
	assertSlot(t, r7, 0, turn.Live, "g")
	assertSlot(t, r7, 1, turn.Live, "y")
	if e.Active(1) || e.Miss(1) != 0 {
		t.Fatalf("p1 active=%v m=%d, want inactive/m=0", e.Active(1), e.Miss(1))
	}
}

// touched：未触发结算的 Submit 触碰槽位 ≤ A+2，且 N=4 与 N=4096 同档。
func TestTouchedBoundIndependentOfN(t *testing.T) {
	const a = 8
	for _, n := range []int{4, 4096} {
		e := New(n, 1_000_000, a, 0, 100, 0)
		e.ResetTouched()
		// p0 提前交满 cur..cur+A（共 A+1 回合），均不到齐（其他活跃玩家未交）。
		for k := 1; k <= a+1; k++ {
			if err := e.Submit(1, 0, k, []byte{byte(k)}); err != nil {
				t.Fatalf("n=%d submit k=%d: %v", n, k, err)
			}
		}
		if e.Cur() != 1 {
			t.Fatalf("n=%d cur=%d want 1 (不应结算)", n, e.Cur())
		}
		touched := e.Touched()
		if touched > a+2 {
			t.Fatalf("n=%d touched=%d > A+2=%d", n, touched, a+2)
		}
		t.Logf("输入：N=%d A=%d 下 p0 提交 %d 个提前回合；输出：touched=%d；判定依据：到齐靠 ready 计数，触碰数不随 N 增长",
			n, a, a+1, touched)

		// 单次未触发结算的 Submit 只触碰 1 个槽位。
		e2 := New(n, 1_000_000, a, 0, 100, 0)
		e2.ResetTouched()
		if err := e2.Submit(1, 0, 5, []byte("z")); err != nil {
			t.Fatal(err)
		}
		if e2.Touched() != 1 {
			t.Fatalf("n=%d single submit touched=%d want 1", n, e2.Touched())
		}
	}
}

// 并发调用：结果等价于某串行顺序（无数据竞争，无崩溃，记录连续完整）。
func TestConcurrentAccess(t *testing.T) {
	e := New(4, 5, 4, 1, 3, 0)
	var wg sync.WaitGroup
	for p := 0; p < 4; p++ {
		wg.Add(1)
		go func(p int) {
			defer wg.Done()
			for k := 1; k <= 60; k++ {
				now := int64(k * 6)
				_ = e.Submit(now, p, k, []byte{byte(p), byte(k)})
			}
			_, _ = e.Advance(int64(p)*1000 + 100000)
		}(p)
	}
	wg.Wait()
	recs := e.Log(1)
	for i, r := range recs {
		if r.Turn != i+1 || len(r.Slots) != 4 {
			t.Fatalf("bad record %+v at index %d", r, i)
		}
	}
	if len(recs) < 1 {
		t.Fatal("no turns settled")
	}
}

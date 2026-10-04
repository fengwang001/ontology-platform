package booking

import (
	"sync"
	"testing"
)

func runTouchedScale(t *testing.T, capV int) {
	t.Helper()
	// R=0：所有号在 start 前都可被线上订满；G=10。
	b := New(0, 30, 10, 120, 3, 1_000_000)
	// 单槽号数上限 1000：100000 档拆为两个槽，验证计数器与总预约数无关。
	per := 1000
	nslots := (capV + per - 1) / per
	start := int64(1_000_000)
	for i := 0; i < nslots; i++ {
		cn := per
		if i == nslots-1 {
			cn = capV - per*(nslots-1)
		}
		name := slotName(i)
		if err := b.AddSlot(0, name, start, cn, cn); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < capV; i++ {
		name := slotName(i / per)
		if _, err := b.Book(1, patID(i/per, i%per), name, Online); err != nil {
			t.Fatalf("book %d: %v", i, err)
		}
	}
	// 禁约判定读取记录不超过 K 条。
	victim := patID(0, 0)
	for j := 0; j < capV; j++ {
		b.cred.Add(victim, int64(2+j))
	}
	if !b.Banned(start, victim) {
		t.Fatal("应有 K 条以上窗口内记录")
	}
	if n := b.CreditTouched(); n != 3 {
		t.Fatalf("cap=%d 档禁约读取条数应=K=3, got %d", capV, n)
	}
	// 全部预约在 start+10 到期。先在到期前做一次操作：窥视到未到期堆顶但不落地。
	if err := b.AddSlot(start, "x", start*2, 1, 1); err != nil {
		t.Fatal(err)
	}
	if n := b.DueTouched(); n != 1 {
		t.Fatalf("到期前一次操作：落地0+窥视1, got %d", n)
	}
	// start+11 对未开诊槽 x 的订号是被接受操作，触发一次全部落地；
	// x 本身不产生到期条目，但落地后堆空不再窥视，故 touched = 实际落地数 capV。
	if _, err := b.Book(start+11, []byte("xx"), "x", Online); err != nil {
		t.Fatal(err)
	}
	touched := b.DueTouched()
	b.mu.Lock()
	dueLeft := len(b.due)
	leftDueAt := int64(-1)
	if dueLeft > 0 {
		leftDueAt = b.due[0].dueAt
	}
	b.mu.Unlock()
	t.Logf("cap=%d dueLeft=%d touched=%d", capV, dueLeft, touched)
	// 不变量：取出条目数 ≤ 实际落地数 + 1；多出的 1 只能是未到期堆顶。
	if touched > capV+1 || touched < capV {
		t.Fatalf("cap=%d touched=%d 超出 [落地数, 落地数+1]=[%d,%d]",
			capV, touched, capV, capV+1)
	}
	if dueLeft > 0 {
		if touched != capV+1 {
			t.Fatalf("存在未到期堆顶时 touched 应=落地数+1")
		}
		if leftDueAt < start+11 {
			t.Fatalf("残留堆顶应未到期, dueAt=%d", leftDueAt)
		}
	}
	for i := 0; i < nslots; i++ {
		name := slotName(i)
		if _, _, _, uo, us, _ := b.pool.Get(name); uo+us > per {
			t.Fatalf("%s 计数越界 uo+us=%d", name, uo+us)
		}
	}
}

func slotName(i int) string {
	return "s" + string(rune('a'+i))
}

func patID(slotIdx, idx int) []byte {
	return []byte{
		byte(slotIdx),
		byte(idx & 0xff),
		byte((idx >> 8) & 0xff),
	}
}

func TestTouchedScale1000(t *testing.T) {
	runTouchedScale(t, 1000)
}

func TestTouchedScale100000(t *testing.T) {
	if testing.Short() {
		t.Skip("100000 档在 -short 下跳过")
	}
	runTouchedScale(t, 100000)
}

func TestTouchedLandedPlusOne(t *testing.T) {
	// 堆里同时存在到期与未到期条目：touched 应为 落地数+1。
	b := New(0, 0, 100, 0, 2, 1_000_000)
	if err := b.AddSlot(0, "late", 10_000, 50, 50); err != nil {
		t.Fatal(err)
	}
	if err := b.AddSlot(0, "early", 1000, 50, 50); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 50; i++ {
		p := []byte{byte(i), 'e'}
		if _, err := b.Book(1, p, "early", Online); err != nil {
			t.Fatal(err)
		}
		q := []byte{byte(i), 'l'}
		if _, err := b.Book(1, q, "late", Online); err != nil {
			t.Fatal(err)
		}
	}
	// now=1101：early(1100) 50 条到期，late(10100) 未到期，窥视 +1。
	if err := b.AddSlot(1101, "z", 20_000, 1, 1); err != nil {
		t.Fatal(err)
	}
	if n := b.DueTouched(); n != 51 {
		t.Fatalf("touched 应=落地50+窥视1=51, got %d", n)
	}
}

func TestConcurrentSerialEquivalence(t *testing.T) {
	// 多 goroutine 并发对不同患者订号；结果应等价于某一串行顺序：
	// 全部成功、序号互不重复且恰好覆盖 1..n，计数守恒。
	for _, capV := range []int{1, 7, 64} {
		b := New(60, 30, 10, 120, 2, 10000)
		if err := b.AddSlot(0, "s", 100000, capV, capV); err != nil {
			t.Fatal(err)
		}
		var wg sync.WaitGroup
		ids := make(chan int64, capV)
		for i := 0; i < capV; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				id, err := b.Book(1, []byte{byte(i), byte(i >> 8)}, "s", Online)
				if err == nil {
					ids <- id
				}
			}(i)
		}
		wg.Wait()
		close(ids)
		seen := map[int64]bool{}
		n := 0
		for id := range ids {
			if seen[id] {
				t.Fatalf("重复序号 %d", id)
			}
			seen[id] = true
			n++
		}
		if n != capV {
			t.Fatalf("成功数 %d != cap %d", n, capV)
		}
		if _, _, _, uo, us, _ := b.pool.Get("s"); uo+us != capV {
			t.Fatalf("计数不守恒 uo=%d us=%d", uo, us)
		}
	}
}

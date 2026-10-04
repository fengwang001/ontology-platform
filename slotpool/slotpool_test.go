package slotpool

import "testing"

func TestPoolQuotaReleaseAndBorrow(t *testing.T) {
	// R=60，start=600：释放点 540；cap=3，on=2（线上2，现场1）。
	p := New(60)
	if err := p.Add("s", 600, 3, 2); err != nil {
		t.Fatal(err)
	}
	// 题例顺序：t=100 线上 A、B，现场 D 满员。
	mustBook(t, p, "s", 100, Online) // A
	mustBook(t, p, "s", 100, Online) // B
	mustBook(t, p, "s", 100, Onsite) // D
	if p.CanBook("s", 100, Online) {
		t.Fatal("线上配额已满")
	}
	// t=480 A 免责退号（由调用方决定免责），线上计数减 1。
	p.Release("s", Online)
	if _, _, _, uo, us, _ := p.Get("s"); uo != 1 || us != 1 {
		t.Fatalf("A 退号后 uo=%d us=%d", uo, us)
	}
	if p.CanBook("s", 539, Onsite) {
		t.Fatal("释放点之前现场不得借用，t=539 无余号")
	}
	if !p.CanBook("s", 539, Online) {
		t.Fatal("t=539 线上退下的名额仍只归线上")
	}
	// 恰等释放点 540：并入公共池，现场可借用，us 允许超过 cap-on=1。
	if !p.CanBook("s", 540, Onsite) {
		t.Fatal("t=540 恰等释放点，现场应可借用")
	}
	mustBook(t, p, "s", 540, Onsite) // F，us=2
	if _, _, _, uo, us, _ := p.Get("s"); uo != 1 || us != 2 {
		t.Fatalf("借用态计数 uo=%d us=%d（us 可超现场配额）", uo, us)
	}
	if p.CanBook("s", 540, Online) || p.CanBook("s", 540, Onsite) {
		t.Fatal("公共池应满")
	}
	// 借用后退号按所属渠道减计数：退现场 F 只减 us。
	p.Release("s", Onsite)
	if _, _, _, uo, us, _ := p.Get("s"); uo != 1 || us != 1 {
		t.Fatalf("现场退号只应减 us: uo=%d us=%d", uo, us)
	}
}

func TestPoolDuplicateAndMissing(t *testing.T) {
	p := New(0)
	if err := p.Add("s", 10, 1, 0); err != nil {
		t.Fatal(err)
	}
	if err := p.Add("s", 10, 1, 0); err != ErrSlotExists {
		t.Fatalf("重复时段应报 ErrSlotExists, got %v", err)
	}
	if err := p.Book("nope", 1, Online); err != ErrSlotNotFound {
		t.Fatalf("不存在时段应报 ErrSlotNotFound, got %v", err)
	}
}

func mustBook(t *testing.T, p *Pool, slot string, now int64, ch Channel) {
	t.Helper()
	if err := p.Book(slot, now, ch); err != nil {
		t.Fatalf("Book 失败: %v", err)
	}
}

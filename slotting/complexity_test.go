package slotting

import "testing"

// TestExaminedSublinear 以可验证方式证明：
// 自动上架实际逐位考察的货位数不随货位总数线性增长，
// 也不随与该商品无关的在位托盘数增长。
func TestExaminedSublinear(t *testing.T) {
	build := func(nLocs int) (*System, []LocationID) {
		s := NewSystem(nil)
		ids := make([]LocationID, 0, nLocs)
		// 全部为普通品类、容量 1；目标托盘重 100 高 100，
		// 前面绝大多数货位承重 50（不可行），在足够靠后的位置放一个可行货位。
		for i := 1; i <= nLocs; i++ {
			id := LocationID{1, 1, i}
			w := 50
			if i == nLocs {
				w = 1000
			}
			mustLoc(t, s, locOf(id, w, 1000, 1, false, CatGeneral))
			ids = append(ids, id)
		}
		return s, ids
	}

	s100, _ := build(100)
	mustPal(t, s100, pallet("t", "NEW", "b", CatGeneral, 100, 100))
	if _, err := s100.AutoPlace("t"); err != nil {
		t.Fatal(err)
	}
	examined100 := s100.ExaminedCount()

	s400, _ := build(400)
	mustPal(t, s400, pallet("t", "NEW", "b", CatGeneral, 100, 100))
	if _, err := s400.AutoPlace("t"); err != nil {
		t.Fatal(err)
	}
	examined400 := s400.ExaminedCount()

	t.Logf("考察货位数: N=100 -> %d, N=400 -> %d", examined100, examined400)
	if examined100 >= 100 || examined400 >= 400 {
		t.Fatalf("考察货位数不得线性: %d/%d", examined100, examined400)
	}
	// N 扩大 4 倍，考察数增长应远小于 4 倍（sqrt 分块，期望约 2 倍且受块长上限约束）。
	if examined400 > examined100*3 {
		t.Fatalf("考察数近似线性增长: %d -> %d", examined100, examined400)
	}

	// 无关在位托盘不增加考察数：向 s400 的复制环境中先放满大量“别的商品”托盘。
	s400b, _ := build(400)
	for i := 1; i <= 400; i++ {
		// 绝大多数货位承重 50，放 1 重的托盘可行；只在 i<nLocs 的货位放。
		if i == 400 {
			continue // 保留目标货位为空且可行
		}
		pid := "noise" + itoa(i)
		mustPal(t, s400b, pallet(pid, "OTHER", "bx", CatGeneral, 1, 1))
	}
	// 逐一把噪声托盘上架（每个新商品候选为空，走空索引；最后一个空货位留下）。
	for i := 1; i <= 399; i++ {
		if _, err := s400b.AutoPlace("noise" + itoa(i)); err != nil {
			t.Fatalf("noise %d: %v", i, err)
		}
	}
	mustPal(t, s400b, pallet("t2", "NEW", "b", CatGeneral, 100, 100))
	if _, err := s400b.AutoPlace("t2"); err != nil {
		t.Fatal(err)
	}
	examinedWithNoise := s400b.ExaminedCount()
	t.Logf("399 个无关在位托盘后考察货位数: %d", examinedWithNoise)
	// 目标商品 NEW 没有任何在位货位，第一类候选为空；考察数与无噪声时同阶。
	if examinedWithNoise > examined400+5 {
		t.Fatalf("考察数随无关在位托盘增长: 无噪声=%d 有噪声=%d",
			examined400, examinedWithNoise)
	}
}

// TestReplayDeterminism 相同操作序列重放得到完全相同分配。
func TestReplayDeterminism(t *testing.T) {
	run := func() []LocationID {
		s := NewSystem(nil)
		for a := 1; a <= 2; a++ {
			for i := 1; i <= 8; i++ {
				mustLoc(t, s, locOf(LocationID{a, 1, i}, 500, 500, 1, false, CatGeneral))
			}
		}
		got := []LocationID{}
		for i := 0; i < 12; i++ {
			pid := "p" + itoa(i)
			mustPal(t, s, pallet(pid, "P", "b", CatGeneral, 1, 1))
			id, err := s.AutoPlace(pid)
			if err != nil {
				t.Fatal(err)
			}
			got = append(got, id)
		}
		return got
	}
	first := run()
	second := run()
	for i := range first {
		if first[i] != second[i] {
			t.Fatalf("重放不确定: %v != %v", first, second)
		}
	}
}

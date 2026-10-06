package slotting

import (
	"fmt"
	"sync"
	"testing"
)

// examinedFor 直接使用带计数的内部选位函数（持锁），返回自动上架考察的货位数。
func examinedFor(svc *Service, p Pallet) int {
	s := svc.store
	s.lock()
	defer s.unlock()
	_, _, n := svc.autoPickCountLocked(p)
	return n
}

// TestExaminedIndependentOfTotalScale 证明自动上架考察的货位数：
//  1. 不随货位总数线性增长（大量货位装着"别的商品"或保持非候选状态）；
//  2. 不随与该托盘商品无关的在位托盘数增长。
func TestExaminedIndependentOfTotalScale(t *testing.T) {
	for _, nUnrelated := range []int{100, 1000, 5000} {
		t.Run(fmt.Sprintf("unrelated=%d", nUnrelated), func(t *testing.T) {
			svc := NewService(NewStore())
			st := svc.Store()
			// 目标托盘 TARGET 走同商品合并：唯一候选是 (1,1,1)。
			if err := st.AddLocation(locCfg(1, 1, 1, 5000, 500, true,
				cats(CategoryNormal, CategoryFood, CategoryFlammable), true)); err != nil {
				t.Fatal(err)
			}
			// 大量"别的商品"货位，每个放 1 个托盘且容量为1：
			// 它们既非空，也不是 TARGET 商品的同商品候选。
			for i := 0; i < nUnrelated; i++ {
				c := Coord{Aisle: 2 + i/1000, Level: 1 + (i/100)%10, Position: 1 + i%100}
				cfg := locCfg(c.Aisle, c.Level, c.Position, 5000, 500, false,
					cats(CategoryNormal, CategoryFood, CategoryFlammable), true)
				if err := st.AddLocation(cfg); err != nil {
					t.Fatal(err)
				}
				if err := svc.PutawayTo(pallet(fmt.Sprintf("U%d", i),
					fmt.Sprintf("OTHER%d", i%50), "B", CategoryNormal, 10, 10), c); err != nil {
					t.Fatal(err)
				}
			}
			// 先在目标货位放一个 TARGET 商品托盘。
			if err := svc.PutawayTo(pallet("T0", "TARGET", "B", CategoryNormal, 10, 10),
				Coord{1, 1, 1}); err != nil {
				t.Fatal(err)
			}
			target := pallet("T1", "TARGET", "B", CategoryNormal, 10, 10)
			n := examinedFor(svc, target)
			// 同商品候选集合中只有 1 个货位，命中即停；不触碰任何无关货位。
			if n != 1 {
				t.Fatalf("examined=%d want 1 regardless of %d unrelated locations/pallets",
					n, nUnrelated)
			}
		})
	}
}

// TestExaminedEmptySearchBounded 证明走"空货位"分支时，
// 考察量只与空货位（候选）数量相关，与被占用货位数量无关。
func TestExaminedEmptySearchBounded(t *testing.T) {
	svc := NewService(NewStore())
	st := svc.Store()
	// 1 个空货位 + 大量被别的商品占满的货位。
	if err := st.AddLocation(locCfg(1, 1, 1, 5000, 500, false,
		cats(CategoryNormal), true)); err != nil {
		t.Fatal(err)
	}
	const n = 2000
	for i := 0; i < n; i++ {
		c := Coord{Aisle: 5 + i/100, Level: 1 + (i/10)%10, Position: 1 + i%10}
		if err := st.AddLocation(locCfg(c.Aisle, c.Level, c.Position, 5000, 500, false,
			cats(CategoryNormal), true)); err != nil {
			t.Fatal(err)
		}
		if err := svc.PutawayTo(pallet(fmt.Sprintf("O%d", i),
			fmt.Sprintf("P%d", i), "B", CategoryNormal, 1, 1), c); err != nil {
			t.Fatal(err)
		}
	}
	got := examinedFor(svc, pallet("NEW", "BRANDNEW", "B", CategoryNormal, 1, 1))
	if got != 1 {
		t.Fatalf("empty search examined=%d want 1 (only empty candidate)", got)
	}
}

// TestDeterministicReplay 相同配置与操作序列两次重放，分配结果完全一致。
func TestDeterministicReplay(t *testing.T) {
	build := func() (map[string]Coord, map[Coord][]string) {
		svc := newSvcWith(
			locCfg(1, 1, 1, 300, 100, true, cats(CategoryNormal), true),
			locCfg(1, 1, 2, 100, 50, true, cats(CategoryNormal, CategoryFood), false),
			locCfg(2, 1, 1, 500, 200, false, cats(CategoryNormal, CategoryFood, CategoryFlammable), true),
		)
		ps := []Pallet{
			pallet("R1", "G", "B1", CategoryNormal, 80, 40),
			pallet("R2", "G", "B1", CategoryNormal, 80, 40),
			pallet("R3", "G", "B2", CategoryFood, 10, 10),
			pallet("R4", "K", "B1", CategoryNormal, 10, 10),
		}
		where := map[string]Coord{}
		for _, p := range ps {
			c, err := svc.AutoPutaway(p)
			if err != nil {
				t.Fatal(err)
			}
			where[p.ID] = c
		}
		occ := map[Coord][]string{}
		for _, c := range where {
			v, _ := svc.Location(c)
			occ[c] = v.PalletIDs
		}
		return where, occ
	}
	w1, o1 := build()
	w2, o2 := build()
	for id, c := range w1 {
		if got := w2[id]; got != c {
			t.Fatalf("replay mismatch for %s: %v vs %v", id, c, got)
		}
	}
	for c, ids := range o1 {
		if fmt.Sprint(ids) != fmt.Sprint(o2[c]) {
			t.Fatalf("replay occupancy mismatch at %v", c)
		}
	}
}

// TestConcurrentSafety 高并发混合读写，使用 -race 验证无数据竞争，
// 并在结束后校验不变量：无超容量/超承重、托盘唯一归属、无同货位违规。
func TestConcurrentSafety(t *testing.T) {
	svc := newSvcWith(
		locCfg(1, 1, 1, 500, 200, true, cats(CategoryNormal, CategoryFood, CategoryFlammable), true),
		locCfg(1, 1, 2, 500, 200, true, cats(CategoryNormal, CategoryFood, CategoryFlammable), true),
		locCfg(1, 1, 3, 500, 200, false, cats(CategoryNormal), true),
	)
	var wg sync.WaitGroup
	const writers = 8
	const each = 60
	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < each; i++ {
				id := fmt.Sprintf("W%d-%d", w, i)
				p := pallet(id, fmt.Sprintf("G%d", (w+i)%3), "B",
					Category((w+i)%3), 10+(i%5)*20, 10+(i%4)*10)
				c, err := svc.AutoPutaway(p)
				if err == nil {
					// 三分之一概率立即取出，制造高翻转。
					if i%3 == 0 {
						_ = svc.Retrieve(id)
					} else if i%5 == 0 {
						_ = svc.Move(id, Coord{1, 1, 3})
					}
					_ = c
				}
			}
		}(w)
	}
	// 并发只读查询。
	for r := 0; r < 4; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				_, _ = svc.Location(Coord{1, 1, 1})
				_ = svc.ProductLocations("G0")
			}
		}()
	}
	wg.Wait()

	// 串行校验最终不变量。
	st := svc.store
	st.lock()
	defer st.unlock()
	counts := map[string]int{}
	for c, loc := range st.locs {
		if len(loc.pallets) > loc.config.Capacity {
			t.Fatalf("capacity exceeded at %v", c)
		}
		if loc.weight > loc.config.WeightLimit {
			t.Fatalf("weight exceeded at %v", c)
		}
		product, batch := "", ""
		for _, p := range loc.pallets {
			counts[p.ID]++
			if product == "" {
				product, batch = p.Product, p.Batch
			}
			if p.Product != product || (!loc.config.AllowMixedBatch && p.Batch != batch) {
				t.Fatalf("mix violation at %v", c)
			}
			if !loc.config.Allowed[p.Category] {
				t.Fatalf("category violation at %v", c)
			}
		}
		for _, nc := range neighborKeys(c) {
			nb, ok := st.locs[nc]
			if !ok {
				continue
			}
			for _, p := range loc.pallets {
				for _, q := range nb.pallets {
					if (p.Category == CategoryFlammable && q.Category == CategoryFood) ||
						(p.Category == CategoryFood && q.Category == CategoryFlammable) {
						t.Fatalf("adjacency violation %v/%v", c, nc)
					}
				}
			}
		}
	}
	for id, n := range counts {
		if n != 1 {
			t.Fatalf("pallet %s occupies %d slots", id, n)
		}
	}
	if len(counts) != len(st.where) {
		t.Fatalf("pallet index inconsistent: %d vs %d", len(counts), len(st.where))
	}
}

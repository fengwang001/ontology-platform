package query

import (
	"math/rand/v2"
	"sync"
	"sync/atomic"
	"testing"

	"ontology/geom"
	"ontology/grid"
)

func newGrid(cap int, pts ...geom.Point) *grid.Grid {
	g := grid.New(geom.Rect{X0: 0, Y0: 0, X1: 100, Y1: 100}, cap)
	for _, p := range pts {
		g.Insert(p)
	}
	return g
}

// 查询矩形边界命中语义与退化/相离查询。
func TestRangeBoundary(t *testing.T) {
	g := newGrid(4, geom.Point{10, 50}, geom.Point{20, 50},
		geom.Point{50, 10}, geom.Point{50, 20}, geom.Point{50, 50})
	cases := []struct {
		name string
		r    geom.Rect
		want int
	}{
		{"左边界命中", geom.Rect{X0: 10, Y0: 40, X1: 60, Y1: 60}, 3},
		{"右边界不命中", geom.Rect{X0: 0, Y0: 40, X1: 10, Y1: 60}, 0},
		{"下边界命中", geom.Rect{X0: 40, Y0: 10, X1: 60, Y1: 40}, 2},
		{"上边界不命中", geom.Rect{X0: 40, Y0: 0, X1: 60, Y1: 10}, 0},
		{"退化成线", geom.Rect{X0: 50, Y0: 0, X1: 50, Y1: 100}, 0},
		{"退化成点", geom.Rect{X0: 50, Y0: 50, X1: 50, Y1: 50}, 0},
		{"完全相离", geom.Rect{X0: 200, Y0: 200, X1: 300, Y1: 300}, 0},
	}
	for _, c := range cases {
		if got := len(Range(g, c.r, nil)); got != c.want {
			t.Errorf("%s: 命中 %d 点 want %d", c.name, got, c.want)
		}
	}
}

// 10 万均匀点、1% 面积查询：逐点判定与访问格数必须低于上界。
func TestPruningBounds(t *testing.T) {
	g := grid.New(geom.Rect{X0: 0, Y0: 0, X1: 1000, Y1: 1000}, 16)
	rng := rand.New(rand.NewPCG(1, 2))
	const n = 100000
	for i := 0; i < n; i++ {
		g.Insert(geom.Point{X: rng.Float64() * 1000, Y: rng.Float64() * 1000})
	}
	st := &Stats{}
	out := Range(g, geom.Rect{X0: 0, Y0: 0, X1: 100, Y1: 100}, st)
	if len(out) == 0 {
		t.Fatal("1% 面积查询应命中约 1000 点")
	}
	t.Logf("命中=%d 逐点判定=%d 访问格=%d 总格=%d",
		len(out), st.PointsChecked(), st.CellsVisited(), g.CellCount())
	if pc := st.PointsChecked(); pc > n/20 {
		t.Errorf("逐点判定 %d 超过总点数 5%% (%d)", pc, n/20)
	}
	if cv, total := st.CellsVisited(), g.CellCount(); cv > total/10 {
		t.Errorf("访问格数 %d 超过总格数 10%% (%d)", cv, total/10)
	}
}

// 完全包含的格整取：逐点判定为 0。
func TestWholeTake(t *testing.T) {
	g := grid.New(geom.Rect{X0: 0, Y0: 0, X1: 100, Y1: 100}, 4)
	rng := rand.New(rand.NewPCG(3, 4))
	for i := 0; i < 500; i++ {
		g.Insert(geom.Point{X: rng.Float64() * 100, Y: rng.Float64() * 100})
	}
	st := &Stats{}
	out := Range(g, geom.Rect{X0: 0, Y0: 0, X1: 100, Y1: 100}, st)
	if st.PointsChecked() != 0 {
		t.Errorf("整格包含应零逐点判定, got %d", st.PointsChecked())
	}
	if len(out) != g.Total() {
		t.Errorf("整取返回 %d 点 want %d", len(out), g.Total())
	}
}

// 并发插入+查询：结果是已提交点集的一致子集，已提交且在矩形内的点不漏。
func TestConcurrentInsertQuery(t *testing.T) {
	g := grid.New(geom.Rect{X0: 0, Y0: 0, X1: 100, Y1: 100}, 4)
	const inserters, per = 4, 250
	all := make(map[geom.Point]bool, inserters*per)
	lists := make([][]geom.Point, inserters)
	for i := range lists {
		rng := rand.New(rand.NewPCG(uint64(i), 9))
		for j := 0; j < per; j++ {
			p := geom.Point{X: rng.Float64() * 100, Y: rng.Float64() * 100}
			lists[i] = append(lists[i], p)
			all[p] = true
		}
	}
	var mu sync.Mutex
	committed := map[geom.Point]bool{}
	var wg sync.WaitGroup
	for i := range lists {
		wg.Add(1)
		go func(ps []geom.Point) {
			defer wg.Done()
			for _, p := range ps {
				g.Insert(p)
				mu.Lock()
				committed[p] = true
				mu.Unlock()
			}
		}(lists[i])
	}
	qr := geom.Rect{X0: 20, Y0: 20, X1: 80, Y1: 80}
	var done atomic.Bool
	var failed atomic.Bool
	var qwg sync.WaitGroup
	for q := 0; q < 4; q++ {
		qwg.Add(1)
		go func() {
			defer qwg.Done()
			for !done.Load() && !failed.Load() {
				mu.Lock()
				snap := make(map[geom.Point]bool, len(committed))
				for p := range committed {
					snap[p] = true
				}
				mu.Unlock()
				got := map[geom.Point]bool{}
				for _, p := range Range(g, qr, &Stats{}) {
					if !qr.Contains(p) || !all[p] {
						failed.Store(true)
						return
					}
					got[p] = true
				}
				for p := range snap {
					if qr.Contains(p) && !got[p] {
						failed.Store(true)
						return
					}
				}
			}
		}()
	}
	wg.Wait()
	done.Store(true)
	qwg.Wait()
	if failed.Load() {
		t.Fatal("并发查询结果不一致：漏点或出现未提交/越界点")
	}
}

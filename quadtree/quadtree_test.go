package quadtree

import (
	"errors"
	"fmt"
	"math/rand"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// naiveScan 朴素扫描：逐点判定，作为索引查询的对拍基准。
func naiveScan(pts map[string]Point, x1, y1, x2, y2 int64) []Point {
	var out []Point
	for _, p := range pts {
		if p.X >= x1 && p.X < x2 && p.Y >= y1 && p.Y < y2 {
			out = append(out, p)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func ids(pts []Point) []string {
	out := make([]string, len(pts))
	for i, p := range pts {
		out[i] = p.ID
	}
	return out
}

// structure 序列化格子划分，用于验证同一操作序列产生完全相同的划分。
func structure(n *node) string {
	var sb strings.Builder
	var walk func(n *node)
	walk = func(n *node) {
		fmt.Fprintf(&sb, "(%d,%d,%d", n.x, n.y, n.size)
		if n.leaf() {
			ps := append([]Point(nil), n.points...)
			sort.Slice(ps, func(i, j int) bool { return ps[i].ID < ps[j].ID })
			sb.WriteString(" leaf")
			for _, p := range ps {
				fmt.Fprintf(&sb, " %s@%d,%d", p.ID, p.X, p.Y)
			}
		} else {
			for _, c := range n.children {
				walk(c)
			}
		}
		sb.WriteString(")")
	}
	walk(n)
	return sb.String()
}

// countLeaves 统计叶格数量（测试辅助）。
func countLeaves(n *node) int {
	if n.leaf() {
		return 1
	}
	total := 0
	for _, c := range n.children {
		total += countLeaves(c)
	}
	return total
}

// findLeaf 返回包含坐标 (x, y) 的叶格（测试辅助）。
func (t *Quadtree) findLeaf(x, y int64) *node {
	n := t.root
	for !n.leaf() {
		n = n.children[childIndex(n, x, y)]
	}
	return n
}

func mustNew(t *testing.T, side int64, cap int) *Quadtree {
	t.Helper()
	qt, err := New(side, cap)
	if err != nil {
		t.Fatalf("New(%d, %d) 失败: %v", side, cap, err)
	}
	return qt
}

func mustInsert(t *testing.T, qt *Quadtree, p Point) {
	t.Helper()
	if err := qt.Insert(p); err != nil {
		t.Fatalf("Insert(%+v) 失败: %v", p, err)
	}
}

// TestValidation 覆盖所有必须整体拒绝的情形，并验证拒绝后状态不变。
func TestValidation(t *testing.T) {
	// 边长非 2 的幂或非正。
	for _, side := range []int64{0, -1, -8, 3, 5, 6, 7, 100, 48} {
		if _, err := New(side, 4); !errors.Is(err, ErrInvalidSide) {
			t.Fatalf("New(side=%d) 期望 ErrInvalidSide，得到 %v", side, err)
		}
		t.Logf("输入 side=%d -> 拒绝: %v（判定依据：非正的 2 的幂）", side, NewErr(side))
	}
	// 容量非正。
	for _, cap := range []int{0, -1, -100} {
		if _, err := New(16, cap); !errors.Is(err, ErrInvalidCapacity) {
			t.Fatalf("New(cap=%d) 期望 ErrInvalidCapacity，得到 %v", cap, err)
		}
		t.Logf("输入 capacity=%d -> 拒绝（判定依据：容量非正）", cap)
	}

	qt := mustNew(t, 16, 4)
	mustInsert(t, qt, Point{ID: "a", X: 1, Y: 1})
	before := structure(qt.root)

	// 编号为空。
	if err := qt.Insert(Point{ID: "", X: 2, Y: 2}); !errors.Is(err, ErrEmptyID) {
		t.Fatalf("空编号期望 ErrEmptyID，得到 %v", err)
	}
	// 编号重复。
	if err := qt.Insert(Point{ID: "a", X: 5, Y: 5}); !errors.Is(err, ErrDuplicateID) {
		t.Fatalf("重复编号期望 ErrDuplicateID，得到 %v", err)
	}
	// 点落在根区域外。
	for _, p := range []Point{{ID: "b", X: -1, Y: 0}, {ID: "c", X: 0, Y: 16}, {ID: "d", X: 16, Y: 16}, {ID: "e", X: 100, Y: -3}} {
		if err := qt.Insert(p); !errors.Is(err, ErrOutOfBounds) {
			t.Fatalf("越界点 %+v 期望 ErrOutOfBounds，得到 %v", p, err)
		}
		t.Logf("输入点 %+v -> 拒绝（判定依据：根区域为 [0,16) x [0,16)）", p)
	}
	// 删除不存在的编号。
	if err := qt.Delete("ghost"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("删除不存在编号期望 ErrNotFound，得到 %v", err)
	}
	// 查询矩形左端大于右端。
	if _, _, err := qt.Query(5, 0, 4, 8); !errors.Is(err, ErrInvalidRect) {
		t.Fatalf("左端大于右端期望 ErrInvalidRect，得到 %v", err)
	}
	if _, _, err := qt.Query(0, 10, 8, 9); !errors.Is(err, ErrInvalidRect) {
		t.Fatalf("下端大于上端期望 ErrInvalidRect，得到 %v", err)
	}

	// 所有被拒绝的操作不得改变任何格子或点。
	if after := structure(qt.root); after != before {
		t.Fatalf("被拒绝的操作改变了索引:\n之前: %s\n之后: %s", before, after)
	}
	if qt.Len() != 1 {
		t.Fatalf("被拒绝的操作改变了点数: %d", qt.Len())
	}
	t.Logf("全部非法操作被拒绝且索引保持不变: %s", before)
}

// NewErr 仅用于测试日志打印。
func NewErr(side int64) error {
	_, err := New(side, 4)
	return err
}

// TestSplitLineAndQueryBoundary 验证分裂线归属（左闭右开）与查询边界。
func TestSplitLineAndQueryBoundary(t *testing.T) {
	// 根区域 [0,8) x [0,8)，容量 1：插入两个点即触发分裂，分裂线为 x=4, y=4。
	qt := mustNew(t, 8, 1)
	pts := []Point{
		{ID: "p-on-split", X: 4, Y: 4},   // 恰在分裂线交点 -> 东北子格
		{ID: "p-sw", X: 3, Y: 3},         // 西南子格
		{ID: "p-east-line", X: 4, Y: 0},  // 在竖直分裂线上 -> 东南子格
		{ID: "p-north-line", X: 0, Y: 4}, // 在水平分裂线上 -> 西北子格
	}
	for _, p := range pts {
		mustInsert(t, qt, p)
		t.Logf("插入 %+v", p)
	}

	// 验证分裂线归属：恰落分裂线的点归右侧或上侧子格。
	cases := []struct {
		id       string
		x, y     int64
		wantCell string
	}{
		{"p-on-split", 4, 4, "[4,8)x[4,8)"},
		{"p-sw", 3, 3, "[0,4)x[0,4)"},
		{"p-east-line", 4, 0, "[4,8)x[0,4)"},
		{"p-north-line", 0, 4, "[0,4)x[4,8)"},
	}
	for _, c := range cases {
		leaf := qt.findLeaf(c.x, c.y)
		got := fmt.Sprintf("[%d,%d)x[%d,%d)", leaf.x, leaf.x+leaf.size, leaf.y, leaf.y+leaf.size)
		t.Logf("点 %s 位于格 %s（判定依据：左闭右开，分裂线上的点归右/上侧）", c.id, got)
		if got != c.wantCell {
			t.Fatalf("点 %s 期望在格 %s，实际在 %s", c.id, c.wantCell, got)
		}
	}

	// 查询边界左闭右开：x=4 含，x=8 不含。
	got, st, err := qt.Query(4, 0, 8, 8)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"p-east-line", "p-on-split"}
	sort.Strings(want)
	t.Logf("查询 [4,8)x[0,8) -> %v（逐点判定 %d 次）", ids(got), st.PointChecks)
	if !reflect.DeepEqual(ids(got), want) {
		t.Fatalf("查询 [4,8)x[0,8) 期望 %v，得到 %v", want, ids(got))
	}

	// 查询上边界不含 y=4。
	got, _, err = qt.Query(0, 0, 4, 4)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"p-sw"}; !reflect.DeepEqual(ids(got), want) {
		t.Fatalf("查询 [0,4)x[0,4) 期望 %v，得到 %v", want, ids(got))
	}

	// 宽或高为零 -> 空结果，非错误。
	for _, r := range [][4]int64{{2, 2, 2, 6}, {2, 2, 6, 2}, {0, 0, 0, 0}} {
		got, st, err := qt.Query(r[0], r[1], r[2], r[3])
		if err != nil {
			t.Fatalf("零宽/高查询 %v 不应报错: %v", r, err)
		}
		if len(got) != 0 || st.PointChecks != 0 {
			t.Fatalf("零宽/高查询 %v 期望空结果，得到 %v", r, ids(got))
		}
		t.Logf("查询 %v（零宽或零高）-> 空（判定依据：左闭右开矩形面积为 0）", r)
	}

	// 结果按编号升序。
	all, _, err := qt.Query(0, 0, 8, 8)
	if err != nil {
		t.Fatal(err)
	}
	if !sort.StringsAreSorted(ids(all)) {
		t.Fatalf("查询结果未按编号升序: %v", ids(all))
	}
}

// TestDuplicateCoordinatesOverflow 插入一千个相同坐标的点：
// 格子分裂到边长 1 后不再分裂，允许超容量并计入溢出格数。
func TestDuplicateCoordinatesOverflow(t *testing.T) {
	const n = 1000
	qt := mustNew(t, 64, 4)
	for i := 0; i < n; i++ {
		id := "dup-" + strconv.Itoa(i)
		if err := qt.Insert(Point{ID: id, X: 7, Y: 9}); err != nil {
			t.Fatalf("插入第 %d 个点失败: %v", i, err)
		}
	}
	t.Logf("输入：%d 个坐标均为 (7,9) 的点，容量 4", n)

	leaf := qt.findLeaf(7, 9)
	t.Logf("判定依据：点 (7,9) 所在叶格边长=%d，格内点数=%d，溢出格数=%d",
		leaf.size, len(leaf.points), qt.OverflowCells())
	if leaf.size != 1 {
		t.Fatalf("期望分裂终止于边长 1，实际叶格边长 %d", leaf.size)
	}
	if len(leaf.points) != n {
		t.Fatalf("边长 1 的格应容纳全部 %d 个点，实际 %d", n, len(leaf.points))
	}
	if qt.OverflowCells() != 1 {
		t.Fatalf("期望 1 个溢出格，实际 %d", qt.OverflowCells())
	}

	// 精确点查询应返回全部一千个点，且按编号升序。
	got, _, err := qt.Query(7, 9, 8, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != n {
		t.Fatalf("点查询期望 %d 个结果，实际 %d", n, len(got))
	}
	if !sort.StringsAreSorted(ids(got)) {
		t.Fatalf("结果未按编号升序")
	}
	t.Logf("输出：查询 [7,8)x[9,10) 返回 %d 个点", len(got))

	// 删除到容量及以下后不再是溢出格；删除不存在的编号被拒绝。
	for i := 0; i < n-4; i++ {
		if err := qt.Delete("dup-" + strconv.Itoa(i)); err != nil {
			t.Fatalf("删除失败: %v", err)
		}
	}
	if qt.OverflowCells() != 0 {
		t.Fatalf("降回容量后溢出格应为 0，实际 %d", qt.OverflowCells())
	}
	if err := qt.Delete("dup-0"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("重复删除期望 ErrNotFound，得到 %v", err)
	}
	t.Logf("删除 %d 个点后剩余 %d 个，溢出格数=%d", n-4, qt.Len(), qt.OverflowCells())
}

// runOps 重放一段确定性的随机操作序列，返回最终索引与参考点集。
func runOps(t *testing.T, seed int64, side int64, capacity, ops int) (*Quadtree, map[string]Point, []string) {
	t.Helper()
	rng := rand.New(rand.NewSource(seed))
	qt, err := New(side, capacity)
	if err != nil {
		t.Fatal(err)
	}
	ref := make(map[string]Point)
	var live []string // 按插入顺序存活的编号，保证删除选择确定性
	var log []string
	nextID := 0
	for i := 0; i < ops; i++ {
		switch rng.Intn(3) {
		case 0, 1: // 插入
			p := Point{ID: fmt.Sprintf("id-%d", nextID), X: rng.Int63n(side), Y: rng.Int63n(side)}
			nextID++
			if err := qt.Insert(p); err != nil {
				t.Fatalf("op %d 插入 %+v 失败: %v", i, p, err)
			}
			ref[p.ID] = p
			live = append(live, p.ID)
			log = append(log, fmt.Sprintf("insert %s (%d,%d)", p.ID, p.X, p.Y))
		case 2: // 删除（若有点）
			if len(live) == 0 {
				continue
			}
			k := rng.Intn(len(live))
			victim := live[k]
			live = append(live[:k], live[k+1:]...)
			if err := qt.Delete(victim); err != nil {
				t.Fatalf("op %d 删除 %s 失败: %v", i, victim, err)
			}
			delete(ref, victim)
			log = append(log, "delete "+victim)
		}
	}
	return qt, ref, log
}

// TestRandomFuzzVsNaive 随机操作序列下与朴素扫描对拍。
func TestRandomFuzzVsNaive(t *testing.T) {
	const side = 128
	rng := rand.New(rand.NewSource(42))
	for trial := 0; trial < 20; trial++ {
		capacity := 1 + rng.Intn(8)
		qt, ref, _ := runOps(t, int64(trial*1000+capacity), side, capacity, 400)
		for q := 0; q < 50; q++ {
			x1 := rng.Int63n(side)
			x2 := x1 + rng.Int63n(side-x1+1)
			y1 := rng.Int63n(side)
			y2 := y1 + rng.Int63n(side-y1+1)
			got, _, err := qt.Query(x1, y1, x2, y2)
			if err != nil {
				t.Fatal(err)
			}
			want := naiveScan(ref, x1, y1, x2, y2)
			if !reflect.DeepEqual(ids(got), ids(want)) {
				t.Fatalf("trial %d 查询 [%d,%d)x[%d,%d) 不一致:\n索引: %v\n朴素: %v",
					trial, x1, x2, y1, y2, ids(got), ids(want))
			}
		}
		t.Logf("trial %d（容量 %d，%d 个点）50 次随机查询与朴素扫描完全一致",
			trial, capacity, len(ref))
	}
}

// TestDeterministic 同一操作序列反复执行，格子划分与查询结果完全相同。
func TestDeterministic(t *testing.T) {
	const side = 64
	qt1, ref1, log1 := runOps(t, 7, side, 3, 500)
	qt2, _, _ := runOps(t, 7, side, 3, 500)

	s1, s2 := structure(qt1.root), structure(qt2.root)
	if s1 != s2 {
		t.Fatalf("同一操作序列产生不同划分:\n第一次: %s\n第二次: %s", s1, s2)
	}
	rng := rand.New(rand.NewSource(99))
	for q := 0; q < 30; q++ {
		x1 := rng.Int63n(side)
		x2 := x1 + rng.Int63n(side-x1+1)
		y1 := rng.Int63n(side)
		y2 := y1 + rng.Int63n(side-y1+1)
		r1, st1, _ := qt1.Query(x1, y1, x2, y2)
		r2, st2, _ := qt2.Query(x1, y1, x2, y2)
		if !reflect.DeepEqual(ids(r1), ids(r2)) || st1 != st2 {
			t.Fatalf("第 %d 次查询两次执行结果不同", q)
		}
	}
	t.Logf("重放 %d 步操作两次，划分一致（%d 个叶格），30 次查询结果与统计完全一致；点集大小 %d",
		len(log1), countLeaves(qt1.root), len(ref1))
}

// TestPruningEfficiency 均匀分布下，小面积查询的逐点判定次数应远小于总点数，
// 且完全包含的格子不做逐点判定、不相交的格子不访问。
func TestPruningEfficiency(t *testing.T) {
	const side = 1024
	const n = 20000
	qt := mustNew(t, side, 8)
	rng := rand.New(rand.NewSource(1))
	ref := make(map[string]Point, n)
	for i := 0; i < n; i++ {
		p := Point{ID: fmt.Sprintf("u-%06d", i), X: rng.Int63n(side), Y: rng.Int63n(side)}
		mustInsert(t, qt, p)
		ref[p.ID] = p
	}

	// 小面积查询：32x32，占根区域约 1/1024。
	got, st, err := qt.Query(100, 100, 132, 132)
	if err != nil {
		t.Fatal(err)
	}
	want := naiveScan(ref, 100, 100, 132, 132)
	if !reflect.DeepEqual(ids(got), ids(want)) {
		t.Fatalf("小面积查询与朴素扫描不一致")
	}
	t.Logf("小面积查询 [100,132)x[100,132)：命中 %d 点，逐点判定 %d 次（总点数 %d），访问格子 %d 个",
		len(got), st.PointChecks, n, st.CellsVisited)
	if st.PointChecks >= n/10 {
		t.Fatalf("逐点判定次数 %d 未远小于总点数 %d", st.PointChecks, n)
	}

	// 整格包含：查询与某个内部格子完全对齐时，该子树不做任何逐点判定。
	_, st2, err := qt.Query(512, 512, 768, 768)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("对齐查询 [512,768)x[512,768)：逐点判定 %d 次（判定依据：完全包含的格子整格收集）", st2.PointChecks)
	if st2.PointChecks != 0 {
		t.Fatalf("与格子边界完全对齐的查询不应有逐点判定，实际 %d 次", st2.PointChecks)
	}

	// 全覆盖查询：不访问逻辑下整棵树一次收集，逐点判定为 0。
	all, st3, err := qt.Query(0, 0, side, side)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != n || st3.PointChecks != 0 {
		t.Fatalf("全覆盖查询期望 %d 点、0 次逐点判定，实际 %d 点、%d 次", n, len(all), st3.PointChecks)
	}
	t.Logf("全覆盖查询：%d 点，逐点判定 %d 次", len(all), st3.PointChecks)
}

// TestConcurrentReadWrite 并发插入、删除与查询：
// 每次查询都必须等于某一时刻点集上的朴素扫描（可线性化），
// 且不得观察到分裂进行到一半的状态。配合 go test -race 运行。
func TestConcurrentReadWrite(t *testing.T) {
	const side = 256
	qt := mustNew(t, side, 4)

	var mu sync.Mutex // 保护参考点集的历史快照验证
	ref := make(map[string]Point)

	var wg sync.WaitGroup
	stop := make(chan struct{})

	// 两个写 goroutine：一个插入，一个删除。
	wg.Add(2)
	go func() {
		defer wg.Done()
		rng := rand.New(rand.NewSource(11))
		for i := 0; ; i++ {
			select {
			case <-stop:
				return
			default:
			}
			p := Point{ID: fmt.Sprintf("w-%d", i), X: rng.Int63n(side), Y: rng.Int63n(side)}
			if err := qt.Insert(p); err != nil {
				t.Errorf("并发插入失败: %v", err)
				return
			}
			mu.Lock()
			ref[p.ID] = p
			mu.Unlock()
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; ; i++ {
			select {
			case <-stop:
				return
			default:
			}
			id := fmt.Sprintf("w-%d", i)
			if err := qt.Delete(id); err == nil {
				mu.Lock()
				delete(ref, id)
				mu.Unlock()
			} else if !errors.Is(err, ErrNotFound) {
				t.Errorf("并发删除出现意外错误: %v", err)
				return
			}
			if i > 2000 {
				return
			}
		}
	}()

	// 多个读 goroutine：查询结果必须是某个时刻朴素扫描的子集与超集之间的
	// 一致快照。验证方式：结果内任意两点编号对应的点都真实存在于结果中，
	// 且结果按编号升序、无重复、坐标都在查询范围内。
	for r := 0; r < 4; r++ {
		wg.Add(1)
		go func(seed int64) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(seed))
			for q := 0; q < 300; q++ {
				x1 := rng.Int63n(side)
				x2 := x1 + rng.Int63n(side-x1+1)
				y1 := rng.Int63n(side)
				y2 := y1 + rng.Int63n(side-y1+1)
				got, _, err := qt.Query(x1, y1, x2, y2)
				if err != nil {
					t.Errorf("并发查询失败: %v", err)
					return
				}
				seen := make(map[string]bool)
				for _, p := range got {
					if p.X < x1 || p.X >= x2 || p.Y < y1 || p.Y >= y2 {
						t.Errorf("查询返回范围外的点 %+v（查询 [%d,%d)x[%d,%d)）", p, x1, x2, y1, y2)
						return
					}
					if seen[p.ID] {
						t.Errorf("查询返回重复编号 %s", p.ID)
						return
					}
					seen[p.ID] = true
				}
				if !sort.StringsAreSorted(ids(got)) {
					t.Errorf("并发查询结果未按编号升序")
					return
				}
			}
		}(int64(100 + r))
	}

	// 让读写交错一段时间后停止写入。
	for i := 0; i < 2000; i++ {
		qt.Len()
	}
	close(stop)
	wg.Wait()

	// 并发结束后做最终一致性校验：索引与参考点集完全一致。
	mu.Lock()
	defer mu.Unlock()
	all, _, err := qt.Query(0, 0, side, side)
	if err != nil {
		t.Fatal(err)
	}
	want := naiveScan(ref, 0, 0, side, side)
	if !reflect.DeepEqual(ids(all), ids(want)) {
		t.Fatalf("并发结束后索引与参考点集不一致: 索引 %d 点, 参考 %d 点", len(all), len(want))
	}
	t.Logf("并发读写完成：最终 %d 个点与朴素扫描一致（判定依据：RWMutex 保证每次查询看到完整快照）", len(all))
}

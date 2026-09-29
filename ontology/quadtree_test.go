package ontology

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"reflect"
	"testing"
)

func testLogger(level slog.Level) *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: level}))
}

var discardLogger = slog.New(slog.NewTextHandler(discardWriter{}, nil))

type discardWriter struct{}

func (discardWriter) Write(p []byte) (int, error) { return len(p), nil }

func mustNew(t *testing.T, originX, originY, side int64, capacity int, logger *slog.Logger) *Index {
	t.Helper()
	if logger == nil {
		logger = testLogger(slog.LevelInfo)
	}
	idx, err := New(originX, originY, side, capacity, WithLogger(logger))
	if err != nil {
		t.Fatalf("New: unexpected error: %v", err)
	}
	return idx
}

func TestConstructionErrors(t *testing.T) {
	cases := []struct {
		name     string
		side     int64
		capacity int
		want     error
	}{
		{"zero side", 0, 4, ErrInvalidSize},
		{"negative side", -8, 4, ErrInvalidSize},
		{"side three", 3, 4, ErrInvalidSize},
		{"side six", 6, 4, ErrInvalidSize},
		{"zero capacity", 16, 0, ErrInvalidCapacity},
		{"negative capacity", 16, -2, ErrInvalidCapacity},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := New(0, 0, tc.side, tc.capacity, WithLogger(discardLogger))
			if !errors.Is(err, tc.want) {
				t.Fatalf("want error %v, got %v", tc.want, err)
			}
			var ie *IndexError
			if !errors.As(err, &ie) {
				t.Fatalf("error must be *IndexError, got %T", err)
			}
			t.Logf("拒绝原因可区分: kind=%q message=%q", ie.Kind(), ie.Error())
		})
	}
}

func TestInsertValidationIsAtomic(t *testing.T) {
	idx := mustNew(t, 0, 0, 4, 2, nil)
	if err := idx.Insert([]Point{{ID: "a", X: 0, Y: 0}}); err != nil {
		t.Fatal(err)
	}

	good := Point{ID: "good", X: 1, Y: 1}
	cases := []struct {
		name string
		list []Point
		want error
	}{
		{"out of range high", []Point{good, {ID: "bad", X: 4, Y: 0}}, ErrPointOutOfRange},
		{"out of range low", []Point{good, {ID: "bad", X: -1, Y: 0}}, ErrPointOutOfRange},
		{"empty id", []Point{good, {ID: "", X: 0, Y: 0}}, ErrEmptyID},
		{"duplicate existing", []Point{good, {ID: "a", X: 2, Y: 2}}, ErrDuplicateID},
		{"duplicate in batch", []Point{{ID: "z", X: 0, Y: 0}, {ID: "z", X: 1, Y: 1}}, ErrDuplicateID},
	}
	before := idx.Stats()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := idx.Insert(tc.list)
			if !errors.Is(err, tc.want) {
				t.Fatalf("want %v, got %v", tc.want, err)
			}
			after := idx.Stats()
			if after != before {
				t.Fatalf("rejected insert changed structure: before=%+v after=%+v", before, after)
			}
			t.Logf("输入=%v => 拒绝(%v)，结构不变 stats=%+v", tc.list, err, after)
		})
	}

	res, err := idx.Query(context.Background(), Rect{0, 0, 4, 4})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(res.IDs, []string{"a"}) {
		t.Fatalf("only a should remain, got %v", res.IDs)
	}
}

func TestSplitLinesAndQueryBoundaries(t *testing.T) {
	// 容量 1：根 [0,8)^2，插入落在两条一级分裂线 (4,*) 与 (*,4) 上的点。
	idx := mustNew(t, 0, 0, 8, 1, nil)
	pts := []Point{
		{ID: "onX", X: 4, Y: 0}, // x 恰在分裂线 -> 右子格
		{ID: "onY", X: 0, Y: 4}, // y 恰在分裂线 -> 上子格
		{ID: "onBoth", X: 4, Y: 4},
		{ID: "ll", X: 0, Y: 0},
		{ID: "inQ", X: 5, Y: 5},
	}
	if err := idx.Insert(pts); err != nil {
		t.Fatal(err)
	}
	t.Logf("插入后 stats=%+v", idx.Stats())

	// 查询左闭右开：边界上的点只在“包含该坐标为左/下端”时命中。
	cases := []struct {
		name string
		r    Rect
		want []string
	}{
		{"left edge inclusive", Rect{4, 0, 8, 8}, []string{"inQ", "onBoth", "onX"}},
		{"right edge exclusive", Rect{0, 0, 4, 8}, []string{"ll", "onY"}},
		{"bottom edge inclusive", Rect{0, 4, 8, 8}, []string{"inQ", "onBoth", "onY"}},
		{"top edge exclusive", Rect{0, 0, 8, 4}, []string{"ll", "onX"}},
		{"point rect on split", Rect{4, 4, 5, 5}, []string{"onBoth"}},
		{"zero width", Rect{4, 0, 4, 8}, []string{}},
		{"zero height", Rect{0, 4, 8, 4}, []string{}},
		{"inner small", Rect{5, 5, 6, 6}, []string{"inQ"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res, err := idx.Query(context.Background(), tc.r)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(res.IDs, tc.want) {
				t.Fatalf("rect %v: want %v, got %v (stats=%+v)",
					tc.r, tc.want, res.IDs, res.Stats)
			}
			naive := idx.naiveQuery(tc.r)
			if !reflect.DeepEqual(res.IDs, naive) {
				t.Fatalf("mismatch vs naive scan: tree=%v naive=%v", res.IDs, naive)
			}
			t.Logf("输入=%v 输出=%v 判定: pointChecks=%d fullLeaves=%d pruned=%d tested=%d",
				tc.r, res.IDs, res.Stats.PointChecks, res.Stats.FullyContainedLeaves,
				res.Stats.PrunedNodes, res.Stats.BoundTestedNodes)
		})
	}

	// 非法查询矩形：左端大于右端（或下端大于上端）必须整体拒绝。
	for _, bad := range []Rect{{5, 0, 4, 8}, {0, 5, 8, 4}} {
		_, err := idx.Query(context.Background(), bad)
		if !errors.Is(err, ErrInvalidQueryRange) {
			t.Fatalf("rect %v: want ErrInvalidQueryRange, got %v", bad, err)
		}
	}
}

func TestThousandSameCoordinate(t *testing.T) {
	idx := mustNew(t, 0, 0, 1024, 4, testLogger(slog.LevelWarn))
	const n = 1000
	pts := make([]Point, n)
	for i := range pts {
		pts[i] = Point{ID: "p" + itoa(i), X: 7, Y: 3}
	}
	if err := idx.Insert(pts); err != nil {
		t.Fatal(err)
	}
	st := idx.Stats()
	if st.PointCount != n {
		t.Fatalf("point count = %d, want %d", st.PointCount, n)
	}
	if st.OverflowCells != 1 {
		t.Fatalf("overflow cells = %d, want exactly 1 (side-1 cell)", st.OverflowCells)
	}
	t.Logf("1000 个同坐标点: maxDepth=%d leafCells=%d overflowCells=%d",
		st.MaxDepth, st.LeafCount, st.OverflowCells)

	// 单点格被整格包含，不应发生逐点判定。
	res, err := idx.Query(context.Background(), Rect{7, 3, 8, 4})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.IDs) != n {
		t.Fatalf("hits = %d, want %d", len(res.IDs), n)
	}
	if res.Stats.PointChecks != 0 {
		t.Fatalf("fully contained cell must not point-check, got %d", res.Stats.PointChecks)
	}
	if res.Stats.FullyContainedLeaves != 1 {
		t.Fatalf("want 1 fully contained leaf, got %d", res.Stats.FullyContainedLeaves)
	}
	t.Logf("整格命中: hits=%d pointChecks=%d fullyContainedLeaves=%d",
		len(res.IDs), res.Stats.PointChecks, res.Stats.FullyContainedLeaves)

	// 越界但仍完整包含该单位格的查询：依旧整格收录、零逐点判定。
	res2, err := idx.Query(context.Background(), Rect{6, 2, 8, 4})
	if err != nil {
		t.Fatal(err)
	}
	if len(res2.IDs) != n || res2.Stats.PointChecks != 0 {
		t.Fatalf("containing query: hits=%d checks=%d, want %d/0",
			len(res2.IDs), res2.Stats.PointChecks, n)
	}
	if !reflect.DeepEqual(res2.IDs, idx.naiveQuery(Rect{6, 2, 8, 4})) {
		t.Fatal("containing query mismatch vs naive scan")
	}
	// 完全不相交：不访问任何点，也不访问与矩形不相交的子格。
	disjoint, err := idx.Query(context.Background(), Rect{0, 0, 7, 3})
	if err != nil {
		t.Fatal(err)
	}
	if len(disjoint.IDs) != 0 || disjoint.Stats.PointChecks != 0 {
		t.Fatalf("disjoint query must not touch points: %+v", disjoint.Stats)
	}
	t.Logf("不相交查询: boundTested=%d pruned=%d pointChecks=%d",
		disjoint.Stats.BoundTestedNodes, disjoint.Stats.PrunedNodes, disjoint.Stats.PointChecks)

	// 删除中间编号后结果仍按编号升序，且结构不合并。
	if err := idx.Delete([]string{"p1", "p500", "p999"}); err != nil {
		t.Fatal(err)
	}
	res3, err := idx.Query(context.Background(), Rect{7, 3, 8, 4})
	if err != nil {
		t.Fatal(err)
	}
	if len(res3.IDs) != n-3 {
		t.Fatalf("hits after delete = %d, want %d", len(res3.IDs), n-3)
	}
	if !sortedStrings(res3.IDs) {
		t.Fatalf("ids not ascending: head=%v", res3.IDs[:5])
	}
	leavesBefore := idx.Stats().LeafCount
	// 再删到容量以下：溢出消失，但叶子格划分必须保持不变（不合并）。
	rest := make([]string, 0, n-3-2)
	for i := 0; i < n; i++ {
		id := "p" + itoa(i)
		if id != "p1" && id != "p500" && id != "p999" {
			rest = append(rest, id)
		}
		if len(rest) == n-3-2 {
			break
		}
	}
	if err := idx.Delete(rest); err != nil {
		t.Fatal(err)
	}
	stAfter := idx.Stats()
	if stAfter.OverflowCells != 0 {
		t.Fatalf("after dropping below capacity overflow must be 0, got %d", stAfter.OverflowCells)
	}
	if stAfter.LeafCount != leavesBefore {
		t.Fatalf("delete must not merge cells: leaves %d -> %d", leavesBefore, stAfter.LeafCount)
	}
	t.Logf("删除后剩余=%d overflowCells=%d leafCells 保持=%d（不合并）",
		stAfter.PointCount, stAfter.OverflowCells, stAfter.LeafCount)
}

func TestDeleteValidation(t *testing.T) {
	idx := mustNew(t, 0, 0, 4, 2, nil)
	if err := idx.Insert([]Point{{ID: "a", X: 0, Y: 0}, {ID: "b", X: 3, Y: 3}}); err != nil {
		t.Fatal(err)
	}
	before := idx.Stats()
	for _, ids := range [][]string{
		{"missing"},
		{"a", "missing"},
		{"a", "a"},
		{""},
	} {
		err := idx.Delete(ids)
		if !errors.Is(err, ErrDeleteNotFound) {
			t.Fatalf("delete %v: want ErrDeleteNotFound, got %v", ids, err)
		}
		if got := idx.Stats(); got != before {
			t.Fatalf("rejected delete changed structure: %+v vs %+v", got, before)
		}
		t.Logf("删除输入=%v => 拒绝(%v)，结构不变", ids, err)
	}
	if err := idx.Delete([]string{"a", "b"}); err != nil {
		t.Fatal(err)
	}
	if got := idx.Stats().PointCount; got != 0 {
		t.Fatalf("point count after full delete = %d, want 0", got)
	}
}

func sortedStrings(s []string) bool {
	for i := 1; i < len(s); i++ {
		if s[i-1] > s[i] {
			return false
		}
	}
	return true
}

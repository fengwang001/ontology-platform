package index

import (
	"errors"
	"fmt"
	"math/rand"
	"reflect"
	"sort"
	"sync"
	"testing"
)

func testIndex() *Index {
	return New(
		Column{Name: "a", Type: KindInt},
		Column{Name: "b", Type: KindInt},
		Column{Name: "c", Type: KindInt},
	)
}

// seed 填充一份含空值的确定性数据集。
func seed(t *testing.T, ix *Index, rows [][]Value) {
	t.Helper()
	for _, r := range rows {
		if err := ix.Insert(r...); err != nil {
			t.Fatalf("insert %v: %v", r, err)
		}
	}
}

func defaultRows() [][]Value {
	v := Int
	n := Null
	return [][]Value{
		{n(), n(), n()},
		{n(), v(2), v(5)},
		{n(), v(4), v(5)},
		{v(1), n(), v(5)},
		{v(1), v(2), v(5)},
		{v(1), v(4), v(5)},
		{v(1), v(6), v(3)},
		{v(2), v(2), v(5)},
		{v(2), v(4), v(5)},
		{v(2), v(4), v(7)},
		{v(2), v(5), v(5)},
		{v(3), v(4), v(5)},
		{v(4), v(2), v(2)},
		{v(6), v(6), v(6)},
	}
}

// baseline 是全表逐行过滤的对拍基准。
func baseline(ix *Index, conds []Cond) [][]Value {
	var out [][]Value
	for _, k := range ix.snapshot() {
		if ix.MatchesAll(conds, k) {
			out = append(out, k)
		}
	}
	return out
}

// intervalCount 独立统计快照中落在任一区间内的条目数。
func intervalCount(snap [][]Value, plan Plan) int {
	n := 0
	for _, k := range snap {
		for _, iv := range plan.Intervals {
			if !belowLo(k, iv.Lo) && !aboveHi(k, iv.Hi) {
				n++
				break
			}
		}
	}
	return n
}

// checkPlanShape 校验区间按索引序排列且互不重叠。
func checkPlanShape(t *testing.T, ix *Index, plan Plan) {
	t.Helper()
	snap := ix.snapshot()
	prevEnd := 0
	for i, iv := range plan.Intervals {
		start := 0
		if !iv.Lo.Unbounded {
			start = sort.Search(len(snap), func(j int) bool { return !belowLo(snap[j], iv.Lo) })
		}
		end := len(snap)
		if !iv.Hi.Unbounded {
			end = sort.Search(len(snap), func(j int) bool { return aboveHi(snap[j], iv.Hi) })
		}
		if start > end {
			t.Fatalf("interval %d inverted: %v", i, iv)
		}
		if start < prevEnd {
			t.Fatalf("interval %d overlaps previous: %v", i, iv)
		}
		prevEnd = end
	}
}

// runAndCheck 执行扫描并与全表过滤对拍，同时校验考察条目数与输出有序性。
func runAndCheck(t *testing.T, ix *Index, q Query, limit int) Result {
	t.Helper()
	res, err := ix.Scan(q, limit)
	if err != nil {
		t.Fatalf("scan %s: %v", q, err)
	}
	want := baseline(ix, q.Conds)
	if len(res.Keys) == 0 && len(want) == 0 {
		// 均为空，视为相等
	} else if !reflect.DeepEqual(res.Keys, want) {
		t.Fatalf("mismatch for %s\n got: %v\nwant: %v", q, res.Keys, want)
	}
	snap := ix.snapshot()
	if got := intervalCount(snap, res.Plan); got != res.Examined {
		t.Fatalf("examined=%d, but intervals hold %d entries for %s", res.Examined, got, q)
	}
	for i := 1; i < len(res.Keys); i++ {
		if CompareKeys(res.Keys[i-1], res.Keys[i]) > 0 {
			t.Fatalf("output not in index order for %s: %v", q, res.Keys)
		}
	}
	checkPlanShape(t, ix, res.Plan)
	t.Logf("input: %s (L=%d)\nplan: %s\noutput: %v\nbasis: examined=%d equals entries inside derived intervals; output equals full-table filter (%d rows)",
		q, limit, res.Plan, res.Keys, res.Examined, len(want))
	return res
}

// 首列属于 {1,2} 且次列大于 3 且第三列等于 5。
func TestInRangeEq(t *testing.T) {
	ix := testIndex()
	seed(t, ix, defaultRows())
	q := Query{Conds: []Cond{In("a", Int(1), Int(2)), Gt("b", Int(3)), Eq("c", Int(5))}}
	res := runAndCheck(t, ix, q, 64)
	if len(res.Plan.Intervals) != 2 {
		t.Fatalf("want 2 intervals (one per prefix combo), got %s", res.Plan)
	}
	if len(res.Plan.Residual) != 1 || res.Plan.Residual[0].Column != "c" {
		t.Fatalf("column c must be residual after range column b, got %v", res.Plan.Residual)
	}
	for _, k := range res.Keys {
		if k[0].I != 1 && k[0].I != 2 || k[1].I <= 3 || k[2].I != 5 {
			t.Fatalf("unexpected key %v", k)
		}
	}
}

// 首列大于 1 且次列等于 2：范围在首列，次列只能残余过滤。
func TestRangeFirstColThenEq(t *testing.T) {
	ix := testIndex()
	seed(t, ix, defaultRows())
	q := Query{Conds: []Cond{Gt("a", Int(1)), Eq("b", Int(2))}}
	res := runAndCheck(t, ix, q, 64)
	if len(res.Plan.Intervals) != 1 {
		t.Fatalf("want 1 interval, got %s", res.Plan)
	}
	iv := res.Plan.Intervals[0]
	if iv.Lo.Inclusive || len(iv.Lo.Key) != 1 || iv.Lo.Key[0].I != 1 || !iv.Hi.Unbounded {
		t.Fatalf("want interval (1, +inf), got %v", iv)
	}
	if len(res.Plan.Residual) != 1 || res.Plan.Residual[0].Column != "b" {
		t.Fatalf("column b must be residual, got %v", res.Plan.Residual)
	}
	for _, k := range res.Keys {
		if k[0].I <= 1 || k[1].Kind == KindNull || k[1].I != 2 {
			t.Fatalf("unexpected key %v", k)
		}
	}
}

// 首列小于 5 不含空值：区间下界必须排除空值。
func TestLtExcludesNull(t *testing.T) {
	ix := testIndex()
	seed(t, ix, defaultRows())
	q := Query{Conds: []Cond{Lt("a", Int(5))}}
	res := runAndCheck(t, ix, q, 64)
	for _, k := range res.Keys {
		if k[0].Kind == KindNull || k[0].I >= 5 {
			t.Fatalf("unexpected key %v", k)
		}
	}
	nullRows := 0
	for _, k := range ix.snapshot() {
		if k[0].Kind == KindNull {
			nullRows++
		}
	}
	if res.Examined+nullRows > ix.Len() {
		t.Fatalf("examined=%d should exclude %d null rows", res.Examined, nullRows)
	}
	iv := res.Plan.Intervals[0]
	if iv.Lo.Inclusive || iv.Lo.Key[0].Kind != KindNull {
		t.Fatalf("lower bound must be (NULL) exclusive, got %v", iv.Lo)
	}
}

// 矛盾条件：空区间集合且考察零条。
func TestContradiction(t *testing.T) {
	ix := testIndex()
	seed(t, ix, defaultRows())
	cases := []Query{
		{Conds: []Cond{Gt("a", Int(5)), Lt("a", Int(2))}},
		{Conds: []Cond{Eq("a", Int(1)), Eq("a", Int(2))}},
		{Conds: []Cond{IsNull("a"), Gt("a", Int(1))}},
		{Conds: []Cond{In("a", Int(1), Int(2)), In("a", Int(3), Int(4))}},
		{Conds: []Cond{Ge("a", Int(3)), Le("a", Int(3)), Eq("a", Int(4))}},
		{Conds: []Cond{In("b", Int(1), Int(9)), Gt("b", Int(100))}},
	}
	for _, q := range cases {
		before := ix.Stats()
		res := runAndCheck(t, ix, q, 64)
		if !res.Plan.Empty || len(res.Plan.Intervals) != 0 {
			t.Fatalf("want empty plan for %s, got %s", q, res.Plan)
		}
		if res.Examined != 0 || len(res.Keys) != 0 {
			t.Fatalf("contradiction must examine 0 entries, got %d", res.Examined)
		}
		after := ix.Stats()
		if after.Examined != before.Examined {
			t.Fatalf("empty plan must not examine entries")
		}
	}
}

// 组合数超限退化：从首次超限的列起改作残余过滤。
func TestComboLimitDegrade(t *testing.T) {
	ix := testIndex()
	seed(t, ix, defaultRows())
	q := Query{Conds: []Cond{In("a", Int(1), Int(2), Int(3)), In("b", Int(2), Int(4))}}
	res := runAndCheck(t, ix, q, 5) // 3 组合后，再乘 2 得 6 > 5，b 列退化
	if len(res.Plan.Intervals) != 3 {
		t.Fatalf("want 3 prefix intervals, got %s", res.Plan)
	}
	if len(res.Plan.Residual) != 1 || res.Plan.Residual[0].Column != "b" {
		t.Fatalf("column b must degrade to residual, got %v", res.Plan.Residual)
	}
	// L=6 时不再退化
	res2 := runAndCheck(t, ix, q, 6)
	if len(res2.Plan.Intervals) != 6 || len(res2.Plan.Residual) != 0 {
		t.Fatalf("with L=6 want 6 point intervals and no residual, got %s", res2.Plan)
	}
	if !reflect.DeepEqual(res.Keys, res2.Keys) {
		t.Fatalf("degraded and full plans must return identical results")
	}
}

// 未知列、类型不符、空集合、L 非正：整体拒绝且原因可区分，统计不变。
func TestValidation(t *testing.T) {
	ix := testIndex()
	seed(t, ix, defaultRows())
	ok := Query{Conds: []Cond{Eq("a", Int(1))}}
	if _, err := ix.Scan(ok, 8); err != nil {
		t.Fatalf("warm-up scan: %v", err)
	}
	before := ix.Stats()
	cases := []struct {
		name string
		q    Query
		l    int
		want error
	}{
		{"unknown column", Query{Conds: []Cond{Eq("zzz", Int(1))}}, 8, ErrUnknownColumn},
		{"type mismatch", Query{Conds: []Cond{Eq("a", String("x"))}}, 8, ErrTypeMismatch},
		{"null value", Query{Conds: []Cond{Eq("a", Null())}}, 8, ErrTypeMismatch},
		{"null in set", Query{Conds: []Cond{In("a", Int(1), Null())}}, 8, ErrTypeMismatch},
		{"empty set", Query{Conds: []Cond{In("a")}}, 8, ErrEmptySet},
		{"zero limit", ok, 0, ErrInvalidLimit},
		{"negative limit", ok, -3, ErrInvalidLimit},
	}
	for _, tc := range cases {
		_, err := ix.Scan(tc.q, tc.l)
		if !errors.Is(err, tc.want) {
			t.Fatalf("%s: want error %v, got %v", tc.name, tc.want, err)
		}
		t.Logf("input: %s (L=%d)\nrejected: %v\nbasis: validation failure, stats unchanged", tc.q, tc.l, err)
	}
	if got := ix.Stats(); got != before {
		t.Fatalf("rejected queries must not change stats: before=%+v after=%+v", before, got)
	}
}

// 为空视为等值：IS NULL 形成点区间，且只匹配空值。
func TestIsNullAsEquality(t *testing.T) {
	ix := testIndex()
	seed(t, ix, defaultRows())
	q := Query{Conds: []Cond{IsNull("a"), Eq("b", Int(2))}}
	res := runAndCheck(t, ix, q, 8)
	if len(res.Plan.Intervals) != 1 || len(res.Plan.Residual) != 0 {
		t.Fatalf("IS NULL + Eq should form one point interval, got %s", res.Plan)
	}
	for _, k := range res.Keys {
		if k[0].Kind != KindNull || k[1].Kind == KindNull || k[1].I != 2 {
			t.Fatalf("unexpected key %v", k)
		}
	}
}

// 首列无条件：整个索引为一个区间。
func TestNoConditionOnFirstColumn(t *testing.T) {
	ix := testIndex()
	seed(t, ix, defaultRows())
	res := runAndCheck(t, ix, Query{Conds: []Cond{Eq("c", Int(5))}}, 8)
	if len(res.Plan.Intervals) != 1 ||
		!res.Plan.Intervals[0].Lo.Unbounded || !res.Plan.Intervals[0].Hi.Unbounded {
		t.Fatalf("want single whole-index interval, got %s", res.Plan)
	}
	if res.Examined != ix.Len() {
		t.Fatalf("whole-index interval must examine all %d entries, got %d", ix.Len(), res.Examined)
	}
}

// 同一快照与条件反复扫描结果完全相同。
func TestDeterministic(t *testing.T) {
	ix := testIndex()
	seed(t, ix, defaultRows())
	q := Query{Conds: []Cond{In("a", Int(1), Int(2)), Gt("b", Int(1))}}
	r1, err := ix.Scan(q, 8)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		r2, err := ix.Scan(q, 8)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(r1.Keys, r2.Keys) || r1.Examined != r2.Examined {
			t.Fatalf("repeated scans differ: %v vs %v", r1.Keys, r2.Keys)
		}
	}
}

// 一千组随机条件与全表过滤对拍。
func TestRandomizedAgainstFullFilter(t *testing.T) {
	rng := rand.New(rand.NewSource(20260930))
	ix := testIndex()
	var rows [][]Value
	for i := 0; i < 220; i++ {
		k := make([]Value, 3)
		for j := range k {
			if rng.Intn(5) == 0 {
				k[j] = Null()
			} else {
				k[j] = Int(int64(rng.Intn(8) - 1)) // -1..6，制造边界命中
			}
		}
		rows = append(rows, k)
	}
	seed(t, ix, rows)
	cols := []string{"a", "b", "c"}
	randVal := func() Value { return Int(int64(rng.Intn(9) - 1)) }
	for iter := 0; iter < 1000; iter++ {
		n := 1 + rng.Intn(4)
		q := Query{}
		for j := 0; j < n; j++ {
			col := cols[rng.Intn(3)]
			switch rng.Intn(7) {
			case 0:
				q.Conds = append(q.Conds, Eq(col, randVal()))
			case 1:
				m := 1 + rng.Intn(3)
				vs := make([]Value, m)
				for k := range vs {
					vs[k] = randVal()
				}
				q.Conds = append(q.Conds, In(col, vs...))
			case 2:
				q.Conds = append(q.Conds, Lt(col, randVal()))
			case 3:
				q.Conds = append(q.Conds, Le(col, randVal()))
			case 4:
				q.Conds = append(q.Conds, Gt(col, randVal()))
			case 5:
				q.Conds = append(q.Conds, Ge(col, randVal()))
			case 6:
				q.Conds = append(q.Conds, IsNull(col))
			}
		}
		limit := 1 + rng.Intn(10)
		res := runAndCheck(t, ix, q, limit)
		_ = res
		if iter%250 == 0 {
			t.Logf("iter=%d stats=%+v", iter, ix.Stats())
		}
	}
}

// 扫描与插入、删除并发：每次扫描看到一致快照，结果始终有序且与某次静止基线一致。
func TestConcurrentScanInsertDelete(t *testing.T) {
	ix := testIndex()
	seed(t, ix, defaultRows())
	q := Query{Conds: []Cond{Ge("a", Int(1)), Le("a", Int(3))}}

	var wg sync.WaitGroup
	stop := make(chan struct{})
	for w := 0; w < 2; w++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for i := 0; ; i++ {
				select {
				case <-stop:
					return
				default:
				}
				k := Int(int64((i + id) % 7))
				if err := ix.Insert(k, k, k); err != nil {
					t.Errorf("insert: %v", err)
					return
				}
				ix.Delete(k, k, k)
			}
		}(w)
	}
	for s := 0; s < 200; s++ {
		res, err := ix.Scan(q, 16)
		if err != nil {
			t.Fatalf("scan: %v", err)
		}
		for i := 1; i < len(res.Keys); i++ {
			if CompareKeys(res.Keys[i-1], res.Keys[i]) > 0 {
				t.Fatalf("concurrent scan output not in index order: %v", res.Keys)
			}
		}
		for _, k := range res.Keys {
			if k[0].Kind == KindNull || k[0].I < 1 || k[0].I > 3 {
				t.Fatalf("key outside snapshot-consistent range: %v", k)
			}
		}
	}
	close(stop)
	wg.Wait()

	// 静止后：扫描结果必须与全表过滤一致且可重复。
	r1 := runAndCheck(t, ix, q, 16)
	r2 := runAndCheck(t, ix, q, 16)
	if !reflect.DeepEqual(r1.Keys, r2.Keys) {
		t.Fatalf("quiescent repeated scans differ")
	}
	fmt.Printf("concurrent test done, stats=%+v\n", ix.Stats())
}

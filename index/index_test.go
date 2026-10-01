package index

import (
	"errors"
	"fmt"
	"math/rand"
	"strings"
	"sync"
	"testing"
)

// ---------- helpers ----------

func mustInsert(t *testing.T, idx *Index, rowID string, values ...any) {
	t.Helper()
	if err := idx.Insert(rowID, values...); err != nil {
		t.Fatalf("insert %s: %v", rowID, err)
	}
}

// When the prefix combinations exceed L, the column that first crosses the
// limit degrades to a residual filter together with everything after it.
func TestComboLimitDegrade(t *testing.T) {
	idx := newABCIndex()
	id := 0
	for a := int64(1); a <= 3; a++ {
		for b := int64(1); b <= 3; b++ {
			id++
			mustInsert(t, idx, fmt.Sprintf("r%02d", id), a, b, int64(0))
		}
	}
	conds := []Condition{
		{Column: "a", Op: OpIn, Values: []any{int64(1), int64(2)}},
		{Column: "b", Op: OpIn, Values: []any{int64(1), int64(2)}},
	}

	// L = 2: a gives 2 combos, b would make 4 > 2, so b degrades.
	res := checkInvariants(t, idx, conds, 2)
	if res.Plan.PrefixCols != 1 || res.Plan.RangeCol != -1 || res.Plan.ResidualFrom != 1 {
		t.Fatalf("plan shape = prefix %d range %d residual %d, want 1/-1/1",
			res.Plan.PrefixCols, res.Plan.RangeCol, res.Plan.ResidualFrom)
	}
	if got := len(res.Plan.Intervals); got != 2 {
		t.Fatalf("interval count = %d, want 2", got)
	}
	// Examined: a in {1,2} x any b -> 6; result: b in {1,2} too -> 4.
	if res.Examined != 6 || len(res.Entries) != 4 {
		t.Fatalf("examined=%d result=%d, want 6/4", res.Examined, len(res.Entries))
	}

	// L = 4: both set columns fit, 4 point intervals.
	res = checkInvariants(t, idx, conds, 4)
	if res.Plan.PrefixCols != 2 || res.Plan.ResidualFrom != 2 {
		t.Fatalf("plan shape = prefix %d residual %d, want 2/2",
			res.Plan.PrefixCols, res.Plan.ResidualFrom)
	}
	if got := len(res.Plan.Intervals); got != 4 {
		t.Fatalf("interval count = %d, want 4", got)
	}
	if res.Examined != 4 || len(res.Entries) != 4 {
		t.Fatalf("examined=%d result=%d, want 4/4", res.Examined, len(res.Entries))
	}
}

// IS NULL behaves as equality on NULL; ranges collapsing to a single point
// and sets intersected with ranges follow the equality/set rules.
func TestNullEqualityAndCollapses(t *testing.T) {
	idx := newABCIndex()
	vals := []any{nil, int64(1), int64(2), int64(3), int64(4)}
	for i, a := range vals {
		mustInsert(t, idx, fmt.Sprintf("r%d", i), a, int64(0), int64(0))
	}

	// IS NULL -> equality on NULL: one point interval [NULL, NULL].
	res := checkInvariants(t, idx, []Condition{{Column: "a", Op: OpIsNull}}, 16)
	if res.Plan.PrefixCols != 1 || len(res.Plan.Intervals) != 1 {
		t.Fatalf("IS NULL plan = %s, want one point interval", res.Plan)
	}
	if len(res.Entries) != 1 || res.Entries[0].Key[0] != nil {
		t.Fatalf("IS NULL result = %v, want the NULL row only", rowIDs(res.Entries))
	}

	// a >= 3 AND a <= 3 collapses to equality on 3.
	res = checkInvariants(t, idx, []Condition{
		{Column: "a", Op: OpGe, Value: int64(3)},
		{Column: "a", Op: OpLe, Value: int64(3)},
	}, 16)
	if res.Plan.PrefixCols != 1 || len(res.Plan.Intervals) != 1 {
		t.Fatalf("collapsed range plan = %s, want one point interval", res.Plan)
	}
	if len(res.Entries) != 1 || res.Examined != 1 {
		t.Fatalf("collapsed range: result=%d examined=%d, want 1/1",
			len(res.Entries), res.Examined)
	}

	// IN {1,2,3} AND a >= 2 stays a set {2,3}: two point intervals.
	res = checkInvariants(t, idx, []Condition{
		{Column: "a", Op: OpIn, Values: []any{int64(1), int64(2), int64(3)}},
		{Column: "a", Op: OpGe, Value: int64(2)},
	}, 16)
	if len(res.Plan.Intervals) != 2 || res.Plan.PrefixCols != 1 {
		t.Fatalf("set-and-range plan = %s, want two point intervals", res.Plan)
	}
	if len(res.Entries) != 2 || res.Examined != 2 {
		t.Fatalf("set-and-range: result=%d examined=%d, want 2/2",
			len(res.Entries), res.Examined)
	}
}

// No condition on the first column: the whole index is one interval and
// every entry is examined.
func TestNoConditionFirstColumn(t *testing.T) {
	idx := newABCIndex()
	for i := 0; i < 7; i++ {
		mustInsert(t, idx, fmt.Sprintf("r%d", i), int64(i), int64(i%3), int64(0))
	}
	conds := []Condition{{Column: "b", Op: OpEq, Value: int64(1)}}
	res := checkInvariants(t, idx, conds, 16)
	if len(res.Plan.Intervals) != 1 || res.Plan.ResidualFrom != 0 {
		t.Fatalf("plan = %s, want a single full interval", res.Plan)
	}
	if res.Examined != 7 {
		t.Fatalf("examined = %d, want 7 (whole index)", res.Examined)
	}
}

// Invalid queries are rejected as a whole with distinguishable reasons and
// never change the statistics.
func TestValidation(t *testing.T) {
	idx := NewIndex(
		Column{Name: "a", Kind: KindInt},
		Column{Name: "s", Kind: KindString},
	)
	mustInsert(t, idx, "r1", int64(1), "x")

	// One accepted scan so the counters are non-zero.
	if _, err := idx.Scan([]Condition{{Column: "a", Op: OpEq, Value: int64(1)}}, 8); err != nil {
		t.Fatalf("baseline scan: %v", err)
	}

	cases := []struct {
		name  string
		conds []Condition
		limit int
		want  error
	}{
		{"unknown column", []Condition{{Column: "nope", Op: OpEq, Value: int64(1)}}, 8, ErrUnknownColumn},
		{"string for int", []Condition{{Column: "a", Op: OpEq, Value: "text"}}, 8, ErrTypeMismatch},
		{"float for int", []Condition{{Column: "a", Op: OpEq, Value: 1.5}}, 8, ErrTypeMismatch},
		{"int for string", []Condition{{Column: "s", Op: OpEq, Value: int64(1)}}, 8, ErrTypeMismatch},
		{"null operand", []Condition{{Column: "a", Op: OpLt, Value: nil}}, 8, ErrTypeMismatch},
		{"null in set", []Condition{{Column: "a", Op: OpIn, Values: []any{int64(1), nil}}}, 8, ErrTypeMismatch},
		{"empty set", []Condition{{Column: "a", Op: OpIn, Values: []any{}}}, 8, ErrEmptySet},
		{"nil set", []Condition{{Column: "a", Op: OpIn}}, 8, ErrEmptySet},
		{"zero limit", []Condition{{Column: "a", Op: OpEq, Value: int64(1)}}, 0, ErrInvalidComboLimit},
		{"negative limit", []Condition{{Column: "a", Op: OpEq, Value: int64(1)}}, -3, ErrInvalidComboLimit},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			scansBefore, examinedBefore := idx.Stats()
			_, err := idx.Scan(tc.conds, tc.limit)
			if !errors.Is(err, tc.want) {
				t.Fatalf("error = %v, want %v", err, tc.want)
			}
			t.Logf("input: conds=%s L=%d -> rejected: %v", formatConds(tc.conds), tc.limit, err)
			scansAfter, examinedAfter := idx.Stats()
			if scansAfter != scansBefore || examinedAfter != examinedBefore {
				t.Fatalf("stats changed by rejected query: (%d,%d) -> (%d,%d)",
					scansBefore, examinedBefore, scansAfter, examinedAfter)
			}
		})
	}
}

// Insert/Delete validation.
func TestWriteValidation(t *testing.T) {
	idx := NewIndex(Column{Name: "a", Kind: KindInt})
	if err := idx.Insert("r1", int64(1), int64(2)); err == nil {
		t.Fatal("insert with wrong value count accepted")
	}
	if err := idx.Insert("r1", "text"); !errors.Is(err, ErrTypeMismatch) {
		t.Fatalf("insert type error = %v, want %v", err, ErrTypeMismatch)
	}
	mustInsert(t, idx, "r1", int64(1))
	if err := idx.Insert("r1", int64(2)); err == nil {
		t.Fatal("duplicate row id accepted")
	}
	if !idx.Delete("r1") || idx.Delete("r1") {
		t.Fatal("delete semantics broken")
	}
}

// randomConds builds 0..5 random conditions over columns a, b (int) and
// c (string), drawn from a small domain to produce hits, intersections and
// contradictions.
func randomConds(rng *rand.Rand) []Condition {
	cols := []string{"a", "b", "c"}
	strs := []string{"p", "q", "r"}
	n := rng.Intn(6)
	conds := make([]Condition, 0, n)
	for i := 0; i < n; i++ {
		col := cols[rng.Intn(len(cols))]
		operand := func() any {
			if col == "c" {
				return strs[rng.Intn(len(strs))]
			}
			return int64(rng.Intn(10))
		}
		switch op := Op(rng.Intn(7)); op {
		case OpIsNull:
			conds = append(conds, Condition{Column: col, Op: OpIsNull})
		case OpIn:
			size := 1 + rng.Intn(3)
			vals := make([]any, size)
			for j := range vals {
				vals[j] = operand()
			}
			conds = append(conds, Condition{Column: col, Op: OpIn, Values: vals})
		default:
			conds = append(conds, Condition{Column: col, Op: op, Value: operand()})
		}
	}
	return conds
}

// One thousand random condition sets are checked against full-table
// filtering; every iteration logs its input, intervals, output and verdict.
func TestRandomVsFullFilter(t *testing.T) {
	rng := rand.New(rand.NewSource(20260930))
	idx := NewIndex(
		Column{Name: "a", Kind: KindInt},
		Column{Name: "b", Kind: KindInt},
		Column{Name: "c", Kind: KindString},
	)
	strs := []string{"p", "q", "r"}
	for i := 0; i < 150; i++ {
		var a, b, c any
		if rng.Intn(10) > 0 {
			a = int64(rng.Intn(10))
		}
		if rng.Intn(10) > 0 {
			b = int64(rng.Intn(10))
		}
		if rng.Intn(10) > 0 {
			c = strs[rng.Intn(len(strs))]
		}
		mustInsert(t, idx, fmt.Sprintf("r%03d", i), a, b, c)
	}
	for iter := 0; iter < 1000; iter++ {
		conds := randomConds(rng)
		limit := 1 + rng.Intn(6)
		checkInvariants(t, idx, conds, limit)
	}
}

// A snapshot keeps observing the state from its creation: repeated scans
// with the same conditions return identical results even while the live
// index changes.
func TestSnapshotDeterminism(t *testing.T) {
	idx := NewIndex(
		Column{Name: "a", Kind: KindInt},
		Column{Name: "b", Kind: KindInt},
	)
	for i := 0; i < 100; i++ {
		mustInsert(t, idx, fmt.Sprintf("r%03d", i), int64(i%10), int64(i%5))
	}
	snap := idx.Snapshot()
	conds := []Condition{
		{Column: "a", Op: OpGe, Value: int64(3)},
		{Column: "b", Op: OpIn, Values: []any{int64(1), int64(2)}},
	}
	res1, err := snap.Scan(conds, 8)
	if err != nil {
		t.Fatalf("snapshot scan: %v", err)
	}

	// Mutate the live index after the snapshot was taken.
	for i := 100; i < 150; i++ {
		mustInsert(t, idx, fmt.Sprintf("r%03d", i), int64(i%10), int64(i%5))
	}
	for i := 0; i < 20; i++ {
		idx.Delete(fmt.Sprintf("r%03d", i))
	}

	res2, err := snap.Scan(conds, 8)
	if err != nil {
		t.Fatalf("snapshot rescan: %v", err)
	}
	if !equalEntries(res1.Entries, res2.Entries) || res1.Examined != res2.Examined {
		t.Fatalf("same snapshot + conditions gave different results: (%d rows, examined %d) vs (%d rows, examined %d)",
			len(res1.Entries), res1.Examined, len(res2.Entries), res2.Examined)
	}
	want := fullFilter(idx, snap.Entries(), conds)
	if !equalEntries(res1.Entries, want) {
		t.Fatalf("snapshot result diverges from full-table filter of the snapshot")
	}
	t.Logf("input: conds=%s L=8", formatConds(conds))
	t.Logf("snapshot result: %d row(s), examined %d; identical across rescans, matches snapshot full-filter",
		len(res1.Entries), res1.Examined)

	// The live index reflects the mutations.
	res3, err := idx.Scan(conds, 8)
	if err != nil {
		t.Fatalf("live scan: %v", err)
	}
	t.Logf("live index after mutations: %d row(s), examined %d", len(res3.Entries), res3.Examined)
	if res3.Examined == res1.Examined && len(res3.Entries) == len(res1.Entries) {
		t.Fatal("live scan did not observe the mutations")
	}
}

// Scans run concurrently with inserts and deletes; every scan sees one
// consistent snapshot and returns index-ordered results.
func TestConcurrentScanWrite(t *testing.T) {
	idx := NewIndex(
		Column{Name: "a", Kind: KindInt},
		Column{Name: "b", Kind: KindInt},
	)
	for i := 0; i < 200; i++ {
		mustInsert(t, idx, fmt.Sprintf("r%04d", i), int64(i%20), int64(i%7))
	}
	var wg sync.WaitGroup
	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(int64(w)))
			for i := 0; i < 500; i++ {
				id := fmt.Sprintf("w%d-%04d", w, i)
				if err := idx.Insert(id, int64(rng.Intn(20)), int64(rng.Intn(7))); err != nil {
					t.Errorf("insert: %v", err)
					return
				}
				if i%2 == 0 {
					idx.Delete(id)
				}
			}
		}(w)
	}
	for r := 0; r < 4; r++ {
		wg.Add(1)
		go func(r int) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(int64(100 + r)))
			for i := 0; i < 500; i++ {
				conds := []Condition{
					{Column: "a", Op: OpGe, Value: int64(rng.Intn(20))},
					{Column: "b", Op: OpLe, Value: int64(rng.Intn(7))},
				}
				res, err := idx.Scan(conds, 4)
				if err != nil {
					t.Errorf("scan: %v", err)
					return
				}
				if !isSorted(res.Entries) {
					t.Errorf("concurrent scan result not in index order")
					return
				}
				if res.Examined < len(res.Entries) {
					t.Errorf("examined %d < result size %d", res.Examined, len(res.Entries))
					return
				}
			}
		}(r)
	}
	wg.Wait()
}

// Accepted scans accumulate statistics; rejected queries do not.
func TestStats(t *testing.T) {
	idx := NewIndex(Column{Name: "a", Kind: KindInt})
	for i := 0; i < 10; i++ {
		mustInsert(t, idx, fmt.Sprintf("r%d", i), int64(i))
	}
	if scans, examined := idx.Stats(); scans != 0 || examined != 0 {
		t.Fatalf("initial stats = (%d,%d), want (0,0)", scans, examined)
	}
	res, err := idx.Scan([]Condition{{Column: "a", Op: OpGe, Value: int64(5)}}, 4)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	scans, examined := idx.Stats()
	if scans != 1 || examined != int64(res.Examined) {
		t.Fatalf("stats = (%d,%d), want (1,%d)", scans, examined, res.Examined)
	}
	if _, err := idx.Scan([]Condition{{Column: "ghost", Op: OpEq, Value: int64(1)}}, 4); err == nil {
		t.Fatal("invalid query accepted")
	}
	if scans2, examined2 := idx.Stats(); scans2 != scans || examined2 != examined {
		t.Fatalf("rejected query changed stats: (%d,%d) -> (%d,%d)",
			scans, examined, scans2, examined2)
	}
}

// fullFilter is the reference implementation: evaluate every condition on
// every entry of the snapshot.
func fullFilter(idx *Index, entries []Entry, conds []Condition) []Entry {
	norm := normalizeConditions(idx.cols, conds)
	var out []Entry
	for _, e := range entries {
		if matchesAll(idx.colIdx, norm, e.Key) {
			out = append(out, e)
		}
	}
	return out
}

// bruteExamined counts the snapshot entries inside the derived intervals.
func bruteExamined(entries []Entry, plan *Plan) int {
	n := 0
	for _, e := range entries {
		for _, iv := range plan.Intervals {
			if iv.contains(e.Key) {
				n++
				break
			}
		}
	}
	return n
}

func equalEntries(a, b []Entry) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if compareEntries(a[i], b[i]) != 0 {
			return false
		}
	}
	return true
}

func isSorted(entries []Entry) bool {
	for i := 1; i < len(entries); i++ {
		if compareEntries(entries[i-1], entries[i]) > 0 {
			return false
		}
	}
	return true
}

func rowIDs(entries []Entry) []string {
	ids := make([]string, len(entries))
	for i, e := range entries {
		ids[i] = e.RowID
	}
	return ids
}

func formatConds(conds []Condition) string {
	if len(conds) == 0 {
		return "(none)"
	}
	parts := make([]string, len(conds))
	for i, c := range conds {
		switch c.Op {
		case OpIsNull:
			parts[i] = c.Column + " IS NULL"
		case OpIn:
			vals := make([]string, len(c.Values))
			for j, v := range c.Values {
				vals[j] = formatValue(v)
			}
			parts[i] = c.Column + " IN (" + strings.Join(vals, ", ") + ")"
		default:
			parts[i] = c.Column + " " + c.Op.String() + " " + formatValue(c.Value)
		}
	}
	return strings.Join(parts, " AND ")
}

func lowerSatisfied(b Bound, key []any) bool {
	c := cmpPrefix(key, b.Prefix)
	return c > 0 || (c == 0 && b.Inclusive)
}

func upperSatisfied(b Bound, key []any) bool {
	c := cmpPrefix(key, b.Prefix)
	return c < 0 || (c == 0 && b.Inclusive)
}

// compareBounds orders two bounds lexicographically by prefix values; a
// shorter shared prefix sorts first, then inclusive before exclusive.
func compareBounds(a, b Bound) int {
	for i := 0; i < len(a.Prefix) && i < len(b.Prefix); i++ {
		if c := compareValues(a.Prefix[i], b.Prefix[i]); c != 0 {
			return c
		}
	}
	if len(a.Prefix) != len(b.Prefix) {
		if len(a.Prefix) < len(b.Prefix) {
			return -1
		}
		return 1
	}
	if a.Inclusive == b.Inclusive {
		return 0
	}
	if a.Inclusive {
		return -1
	}
	return 1
}

// assertIntervalsOrdered checks that the derived intervals are sorted in
// index order and pairwise non-overlapping.
func assertIntervalsOrdered(t *testing.T, numCols int, plan *Plan) {
	t.Helper()
	for i := 1; i < len(plan.Intervals); i++ {
		prev, cur := plan.Intervals[i-1], plan.Intervals[i]
		if compareBounds(prev.Lower, cur.Lower) >= 0 {
			t.Fatalf("intervals out of order: %s then %s", prev, cur)
		}
		// The smallest key that can satisfy cur.Lower (NULL sorts first):
		// it must not still satisfy prev.Upper.
		key := make([]any, numCols)
		copy(key, cur.Lower.Prefix)
		if upperSatisfied(prev.Upper, key) && lowerSatisfied(cur.Lower, key) {
			t.Fatalf("intervals overlap: %s and %s (key %v in both)", prev, cur, key)
		}
	}
}

// checkInvariants runs one scan and verifies: the result equals full-table
// filtering, the examined count equals the snapshot entries inside the
// derived intervals, and the output is in index order. It logs the input
// conditions, the derived intervals, the output and the verdict basis.
func checkInvariants(t *testing.T, idx *Index, conds []Condition, comboLimit int) *Result {
	t.Helper()
	res, err := idx.Scan(conds, comboLimit)
	if err != nil {
		t.Fatalf("scan rejected: %v", err)
	}
	snap := idx.Snapshot()
	want := fullFilter(idx, snap.Entries(), conds)
	wantExamined := bruteExamined(snap.Entries(), res.Plan)
	match := equalEntries(res.Entries, want)
	sorted := isSorted(res.Entries)

	t.Logf("input: conds=%s L=%d", formatConds(conds), comboLimit)
	t.Logf("derived intervals:\n%s", res.Plan)
	t.Logf("output: %d row(s) %v", len(res.Entries), rowIDs(res.Entries))
	t.Logf("verdict basis: examined=%d brute-force-interval-count=%d full-filter-match=%v sorted=%v",
		res.Examined, wantExamined, match, sorted)

	if !match {
		t.Fatalf("result mismatch vs full-table filter:\n got %v\nwant %v",
			rowIDs(res.Entries), rowIDs(want))
	}
	if res.Examined != wantExamined {
		t.Fatalf("examined=%d, want %d (snapshot entries inside derived intervals)",
			res.Examined, wantExamined)
	}
	if !sorted {
		t.Fatalf("result not in index order: %v", rowIDs(res.Entries))
	}
	assertIntervalsOrdered(t, len(idx.cols), res.Plan)
	return res
}

// ---------- targeted tests ----------

func newABCIndex() *Index {
	return NewIndex(
		Column{Name: "a", Kind: KindInt},
		Column{Name: "b", Kind: KindInt},
		Column{Name: "c", Kind: KindInt},
	)
}

// First column IN {1,2}, second column > 3, third column = 5: the set
// column forms two prefix combinations, the range column is merged into
// the interval bounds, and the third column degrades to a residual filter.
func TestSetRangeEq(t *testing.T) {
	idx := newABCIndex()
	id := 0
	for _, a := range []any{nil, int64(1), int64(2), int64(3)} {
		for _, b := range []any{nil, int64(3), int64(4), int64(5)} {
			for _, c := range []any{nil, int64(5), int64(6)} {
				id++
				mustInsert(t, idx, fmt.Sprintf("r%03d", id), a, b, c)
			}
		}
	}
	conds := []Condition{
		{Column: "a", Op: OpIn, Values: []any{int64(1), int64(2)}},
		{Column: "b", Op: OpGt, Value: int64(3)},
		{Column: "c", Op: OpEq, Value: int64(5)},
	}
	res := checkInvariants(t, idx, conds, 16)
	if got := len(res.Plan.Intervals); got != 2 {
		t.Fatalf("interval count = %d, want 2", got)
	}
	if res.Plan.PrefixCols != 1 || res.Plan.RangeCol != 1 || res.Plan.ResidualFrom != 2 {
		t.Fatalf("plan shape = prefix %d range %d residual %d, want 1/1/2",
			res.Plan.PrefixCols, res.Plan.RangeCol, res.Plan.ResidualFrom)
	}
	// a in {1,2} x b in {4,5} x c = 5 -> 4 rows.
	if got := len(res.Entries); got != 4 {
		t.Fatalf("result size = %d, want 4", got)
	}
	// Examined rows: a in {1,2} x b > 3 (b in {4,5}) x any c -> 2*2*3 = 12.
	if res.Examined != 12 {
		t.Fatalf("examined = %d, want 12", res.Examined)
	}
}

// First column > 1, second column = 2: the leading range column becomes
// the interval bounds and the equality on the second column is residual.
func TestRangeThenEq(t *testing.T) {
	idx := newABCIndex()
	id := 0
	for _, a := range []any{nil, int64(1), int64(2), int64(3)} {
		for _, b := range []any{nil, int64(2), int64(3)} {
			id++
			mustInsert(t, idx, fmt.Sprintf("r%03d", id), a, b, int64(0))
		}
	}
	conds := []Condition{
		{Column: "a", Op: OpGt, Value: int64(1)},
		{Column: "b", Op: OpEq, Value: int64(2)},
	}
	res := checkInvariants(t, idx, conds, 16)
	if got := len(res.Plan.Intervals); got != 1 {
		t.Fatalf("interval count = %d, want 1", got)
	}
	if res.Plan.PrefixCols != 0 || res.Plan.RangeCol != 0 || res.Plan.ResidualFrom != 1 {
		t.Fatalf("plan shape = prefix %d range %d residual %d, want 0/0/1",
			res.Plan.PrefixCols, res.Plan.RangeCol, res.Plan.ResidualFrom)
	}
	// a in {2,3} x b = 2 -> 2 rows.
	if got := len(res.Entries); got != 2 {
		t.Fatalf("result size = %d, want 2", got)
	}
}

// a < 5: NULL sorts first and falls inside the interval, but comparisons
// never match NULL, so the result must not contain NULL keys.
func TestLtExcludesNull(t *testing.T) {
	idx := newABCIndex()
	vals := []any{nil, int64(1), int64(4), int64(5), int64(9)}
	for i, a := range vals {
		mustInsert(t, idx, fmt.Sprintf("r%d", i), a, int64(0), int64(0))
	}
	conds := []Condition{{Column: "a", Op: OpLt, Value: int64(5)}}
	res := checkInvariants(t, idx, conds, 16)
	for _, e := range res.Entries {
		if e.Key[0] == nil {
			t.Fatalf("NULL key %v in result of a < 5", e)
		}
	}
	if got := len(res.Entries); got != 2 {
		t.Fatalf("result size = %d, want 2 (a in {1,4})", got)
	}
	// The interval (-inf, 5) also contains the NULL entry, which is
	// examined but filtered out.
	if res.Examined != 3 {
		t.Fatalf("examined = %d, want 3 (NULL, 1, 4)", res.Examined)
	}
}

// Contradictory conditions yield an empty interval set and examine zero
// entries.
func TestContradictions(t *testing.T) {
	cases := []struct {
		name  string
		conds []Condition
	}{
		{"eq vs eq", []Condition{
			{Column: "a", Op: OpEq, Value: int64(1)},
			{Column: "a", Op: OpEq, Value: int64(2)},
		}},
		{"range vs range", []Condition{
			{Column: "a", Op: OpGt, Value: int64(5)},
			{Column: "a", Op: OpLt, Value: int64(3)},
		}},
		{"open bounds meet", []Condition{
			{Column: "a", Op: OpGt, Value: int64(5)},
			{Column: "a", Op: OpLe, Value: int64(5)},
		}},
		{"is null vs eq", []Condition{
			{Column: "a", Op: OpIsNull},
			{Column: "a", Op: OpEq, Value: int64(1)},
		}},
		{"set vs set", []Condition{
			{Column: "a", Op: OpIn, Values: []any{int64(1), int64(2)}},
			{Column: "a", Op: OpIn, Values: []any{int64(3)}},
		}},
		{"set vs range", []Condition{
			{Column: "a", Op: OpIn, Values: []any{int64(1), int64(2)}},
			{Column: "a", Op: OpGt, Value: int64(5)},
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			idx := newABCIndex()
			for i := 0; i < 10; i++ {
				mustInsert(t, idx, fmt.Sprintf("r%d", i), int64(i), int64(i), int64(i))
			}
			res, err := idx.Scan(tc.conds, 16)
			if err != nil {
				t.Fatalf("scan rejected: %v", err)
			}
			t.Logf("input: conds=%s", formatConds(tc.conds))
			t.Logf("derived intervals: %s", res.Plan)
			t.Logf("verdict basis: empty=%v examined=%d output=%d row(s)",
				res.Plan.Empty, res.Examined, len(res.Entries))
			if !res.Plan.Empty {
				t.Fatalf("plan not marked empty: %s", res.Plan)
			}
			if res.Examined != 0 {
				t.Fatalf("examined = %d, want 0", res.Examined)
			}
			if len(res.Entries) != 0 {
				t.Fatalf("result size = %d, want 0", len(res.Entries))
			}
		})
	}
}

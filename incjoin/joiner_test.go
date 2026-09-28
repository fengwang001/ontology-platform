package incjoin

import (
	"bytes"
	"fmt"
	"math/rand"
	"strings"
	"sync"
	"testing"
)

// ---- 测试用 Logger ----

type bufLogger struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (l *bufLogger) Logf(format string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	fmt.Fprintf(&l.buf, format+"\n", args...)
}

func (l *bufLogger) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.String()
}

// ---- 全量重算参考实现（与增量结果对比）----

type resultTriple struct {
	k, l, r string
}

// fullRecompute 从两侧基表直接计算连接多重集（重数=乘积），返回有序条目。
func fullRecompute(left, right map[string]map[string]int64) []DiffEntry {
	m := make(map[resultTriple]int64)
	for k, lvals := range left {
		rvals := right[k]
		for lv, lm := range lvals {
			for rv, rm := range rvals {
				m[resultTriple{k, lv, rv}] = lm * rm
			}
		}
	}
	out := make([]DiffEntry, 0, len(m))
	for t, mult := range m {
		out = append(out, DiffEntry{Key: t.k, LeftVal: t.l, RightVal: t.r, Mult: mult})
	}
	sortDiff(out)
	return out
}

func entriesEqual(a, b []DiffEntry) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func rowEntriesEqual(a, b []Row) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// checkInvariants 校验物化结果与基表重算一致、无负重数、总数正确，返回错误而非终止。
func checkInvariants(j *Joiner, where string) error {
	want := fullRecompute(j.left, j.right)
	got := make([]DiffEntry, 0)
	var sum int64
	for k, sub := range j.mat {
		for p, m := range sub {
			if m <= 0 {
				return fmt.Errorf("%s: non-positive materialized mult %d at %s/%s/%s", where, m, k, p.l, p.r)
			}
			got = append(got, DiffEntry{Key: k, LeftVal: p.l, RightVal: p.r, Mult: m})
			sum += m
		}
	}
	sortDiff(got)
	if !entriesEqual(want, got) {
		return fmt.Errorf("%s: materialized join diverges from recompute\n got=%v\nwant=%v", where, got, want)
	}
	if sum != j.totalResult {
		return fmt.Errorf("%s: totalResult=%d but materialized sum=%d", where, j.totalResult, sum)
	}
	return nil
}

// assertInvariants 在当前 goroutine 为测试主 goroutine 时使用（失败即终止）。
func assertInvariants(t *testing.T, j *Joiner, where string) {
	t.Helper()
	if err := checkInvariants(j, where); err != nil {
		t.Fatal(err)
	}
}

// ---- 用例 ----

func TestBasicJoinMultiplicity(t *testing.T) {
	j := NewJoiner(Options{})
	diff, err := j.Apply(Change{
		Left:  []Row{{Key: "k", Value: "a", Mult: 2}, {Key: "k", Value: "b", Mult: 3}},
		Right: []Row{{Key: "k", Value: "x", Mult: 4}},
	})
	if err != nil {
		t.Fatal(err)
	}
	// 2*4=8 与 3*4=12
	want := []DiffEntry{
		{Key: "k", LeftVal: "a", RightVal: "x", Mult: 8},
		{Key: "k", LeftVal: "b", RightVal: "x", Mult: 12},
	}
	if !entriesEqual(diff, want) {
		t.Fatalf("diff=%v want=%v", diff, want)
	}
	if got := j.Snapshot(); !entriesEqual(got, want) {
		t.Fatalf("snapshot=%v want=%v", got, want)
	}
	assertInvariants(t, j, "basic")
}

func TestDiffIsPostMinusPre(t *testing.T) {
	j := NewJoiner(Options{})
	mustApply := func(ch Change) []DiffEntry {
		d, err := j.Apply(ch)
		if err != nil {
			t.Fatal(err)
		}
		return d
	}

	mustApply(Change{Left: []Row{{Key: "k", Value: "a", Mult: 1}}})
	// 右侧插入：新增 (k,a,x) 重数 1*2=2
	d := mustApply(Change{Right: []Row{{Key: "k", Value: "x", Mult: 2}}})
	if !entriesEqual(d, []DiffEntry{{Key: "k", LeftVal: "a", RightVal: "x", Mult: 2}}) {
		t.Fatalf("d=%v", d)
	}

	// 左行 a 重数 1->3：结果重数 2->6，差分 +4
	d = mustApply(Change{Left: []Row{{Key: "k", Value: "a", Mult: 2}}})
	if !entriesEqual(d, []DiffEntry{{Key: "k", LeftVal: "a", RightVal: "x", Mult: 4}}) {
		t.Fatalf("d=%v", d)
	}

	// 右行 x 重数 2->1：结果重数 6->3，差分 -3
	d = mustApply(Change{Right: []Row{{Key: "k", Value: "x", Mult: -1}}})
	if !entriesEqual(d, []DiffEntry{{Key: "k", LeftVal: "a", RightVal: "x", Mult: -3}}) {
		t.Fatalf("d=%v", d)
	}

	// 删除左行 a（3->0）：元组消失，差分 -3
	d = mustApply(Change{Left: []Row{{Key: "k", Value: "a", Mult: -3}}})
	if !entriesEqual(d, []DiffEntry{{Key: "k", LeftVal: "a", RightVal: "x", Mult: -3}}) {
		t.Fatalf("d=%v", d)
	}
	if len(j.Snapshot()) != 0 {
		t.Fatalf("expected empty snapshot, got %v", j.Snapshot())
	}
	assertInvariants(t, j, "diff-seq")
}

func TestSimultaneousChangeBothTables(t *testing.T) {
	j := NewJoiner(Options{})
	if _, err := j.Apply(Change{
		Left:  []Row{{Key: "k", Value: "a", Mult: 1}},
		Right: []Row{{Key: "k", Value: "x", Mult: 1}},
	}); err != nil {
		t.Fatal(err)
	}
	// 同一批：左 a->2、右 x->2，并各自新增一个值。
	diff, err := j.Apply(Change{
		Left:  []Row{{Key: "k", Value: "a", Mult: 1}, {Key: "k", Value: "b", Mult: 5}},
		Right: []Row{{Key: "k", Value: "x", Mult: 1}, {Key: "k", Value: "y", Mult: 7}},
	})
	if err != nil {
		t.Fatal(err)
	}
	// 批后矩阵：a,x=4; a,y=14; b,x=10; b,y=35；批前只有 a,x=1。
	want := []DiffEntry{
		{Key: "k", LeftVal: "a", RightVal: "x", Mult: 3},
		{Key: "k", LeftVal: "a", RightVal: "y", Mult: 14},
		{Key: "k", LeftVal: "b", RightVal: "x", Mult: 10},
		{Key: "k", LeftVal: "b", RightVal: "y", Mult: 35},
	}
	if !entriesEqual(diff, want) {
		t.Fatalf("diff=%v want=%v", diff, want)
	}
	assertInvariants(t, j, "both")
}

func TestDeleteNonexistentRejectedAndAtomic(t *testing.T) {
	j := NewJoiner(Options{})
	_, err := j.Apply(Change{Left: []Row{{Key: "k", Value: "a", Mult: 1}}})
	if err != nil {
		t.Fatal(err)
	}
	before := j.Snapshot()
	leftBefore := j.LeftSnapshot()
	rightBefore := j.RightSnapshot()

	// 删除量 2 > 现存 1 → 负重数，拒绝整批。
	_, err = j.Apply(Change{Left: []Row{{Key: "k", Value: "a", Mult: -2}}})
	reject, ok := err.(*RejectError)
	if !ok || reject.Reason != ReasonDeleteNonexistent {
		t.Fatalf("want DELETE_NONEXISTENT_ROW, got %v", err)
	}

	if got := j.LeftSnapshot(); len(got) != 1 || got[0] != (Row{Key: "k", Value: "a", Mult: 1}) {
		t.Fatalf("left table mutated by rejected batch: %v", got)
	}
	if !entriesEqual(j.Snapshot(), before) {
		t.Fatalf("materialized result changed after rejection: %v vs %v", j.Snapshot(), before)
	}
	if got := j.LeftSnapshot(); !rowEntriesEqual(got, leftBefore) {
		t.Fatalf("left table changed after rejection: %v vs %v", got, leftBefore)
	}
	if got := j.RightSnapshot(); !rowEntriesEqual(got, rightBefore) {
		t.Fatalf("right table changed after rejection: %v vs %v", got, rightBefore)
	}
	assertInvariants(t, j, "after-reject")

	// 删除从未存在过的行。
	_, err = j.Apply(Change{Right: []Row{{Key: "k", Value: "z", Mult: -1}}})
	if reject, ok := err.(*RejectError); !ok || reject.Reason != ReasonDeleteNonexistent {
		t.Fatalf("want delete-nonexistent, got %v", err)
	}

	// 同批内多行先插后删、净值为负 → 仍按批后负重数拒绝。
	_, err = j.Apply(Change{Left: []Row{
		{Key: "k", Value: "a", Mult: 1},
		{Key: "k", Value: "a", Mult: -3},
	}})
	if reject, ok := err.(*RejectError); !ok || reject.Reason != ReasonDeleteNonexistent {
		t.Fatalf("want delete-nonexistent for net-negative batch, got %v", err)
	}
	if len(j.LeftSnapshot()) != 1 {
		t.Fatalf("state mutated: %v", j.LeftSnapshot())
	}
}

func TestInvalidInputs(t *testing.T) {
	cases := []struct {
		name   string
		ch     Change
		reason RejectReason
	}{
		{"empty key left", Change{Left: []Row{{Key: "", Value: "a", Mult: 1}}}, ReasonEmptyKey},
		{"empty key right", Change{Right: []Row{{Key: "", Value: "a", Mult: 1}}}, ReasonEmptyKey},
		{"empty value left", Change{Left: []Row{{Key: "k", Value: "", Mult: 1}}}, ReasonEmptyValue},
		{"empty value right", Change{Right: []Row{{Key: "k", Value: "", Mult: -1}}}, ReasonEmptyValue},
		{"zero mult", Change{Left: []Row{{Key: "k", Value: "a", Mult: 0}}}, ReasonInvalidMultSign},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			j := NewJoiner(Options{})
			_, err := j.Apply(c.ch)
			reject, ok := err.(*RejectError)
			if !ok || reject.Reason != c.reason {
				t.Fatalf("want %s, got %v", c.reason, err)
			}
			if len(j.Snapshot()) != 0 || len(j.LeftSnapshot()) != 0 || len(j.RightSnapshot()) != 0 {
				t.Fatal("rejected batch mutated state")
			}
		})
	}
}

func TestResultLimit(t *testing.T) {
	j := NewJoiner(Options{MaxResultTuples: 3})
	// L:a*2, R:x*1 => 2 个元组，通过。
	if _, err := j.Apply(Change{
		Left:  []Row{{Key: "k", Value: "a", Mult: 2}},
		Right: []Row{{Key: "k", Value: "x", Mult: 1}},
	}); err != nil {
		t.Fatal(err)
	}
	// 再插 R:y*1 => 批后 4 个元组，超限拒绝。
	_, err := j.Apply(Change{Right: []Row{{Key: "k", Value: "y", Mult: 1}}})
	if reject, ok := err.(*RejectError); !ok || reject.Reason != ReasonResultLimitExceeded {
		t.Fatalf("want limit reject, got %v", err)
	}
	if j.totalResult != 2 {
		t.Fatalf("totalResult mutated: %d", j.totalResult)
	}
	assertInvariants(t, j, "limit")

	// 超限只按重数展开计数：不同元组数虽为 1，但 3 行 > 上限 2，拒绝。
	j2 := NewJoiner(Options{MaxResultTuples: 2})
	_, err = j2.Apply(Change{
		Left:  []Row{{Key: "k", Value: "a", Mult: 3}},
		Right: []Row{{Key: "k", Value: "x", Mult: 1}},
	})
	if reject, ok := err.(*RejectError); !ok || reject.Reason != ReasonResultLimitExceeded {
		t.Fatalf("want limit reject by multiplicity, got %v", err)
	}
}

func TestDiffAgreesWithFullRecompute(t *testing.T) {
	// 一串混合增删的脚本化批次；累计差分必须始终等于全量重算。
	script := []Change{
		{Left: []Row{{Key: "k1", Value: "a", Mult: 2}}, Right: []Row{{Key: "k1", Value: "x", Mult: 3}, {Key: "k2", Value: "y", Mult: 1}}},
		{Left: []Row{{Key: "k2", Value: "b", Mult: 4}, {Key: "k1", Value: "a", Mult: -1}}},
		{Right: []Row{{Key: "k1", Value: "x", Mult: -1}, {Key: "k2", Value: "z", Mult: 2}}},
		{Left: []Row{{Key: "k1", Value: "c", Mult: 5}}, Right: []Row{{Key: "k2", Value: "y", Mult: -1}}},
		{Left: []Row{{Key: "k2", Value: "b", Mult: -4}}},
		{Left: []Row{{Key: "k1", Value: "a", Mult: -1}, {Key: "k1", Value: "c", Mult: -5}}, Right: []Row{{Key: "k1", Value: "x", Mult: -2}, {Key: "k2", Value: "z", Mult: -2}}},
	}
	j := NewJoiner(Options{})
	// accumulated: 从空开始顺序应用差分得到的连接多重集。
	accum := make(map[resultTriple]int64)
	for i, ch := range script {
		diff, err := j.Apply(ch)
		if err != nil {
			t.Fatalf("batch %d: %v", i, err)
		}
		for _, e := range diff {
			accum[resultTriple{e.Key, e.LeftVal, e.RightVal}] += e.Mult
		}
		// 顺序应用差分后的多重集 == 当前全量快照。
		got := j.Snapshot()
		want := make([]DiffEntry, 0)
		for t, m := range accum {
			if m != 0 {
				want = append(want, DiffEntry{Key: t.k, LeftVal: t.l, RightVal: t.r, Mult: m})
			}
		}
		sortDiff(want)
		if !entriesEqual(got, want) {
			t.Fatalf("batch %d: applying diffs != materialized\n got=%v\nwant=%v", i, got, want)
		}
		// 且与从基表全量重算一致。
		if !entriesEqual(got, fullRecompute(j.left, j.right)) {
			t.Fatalf("batch %d: snapshot != full recompute", i)
		}
	}
	if len(accum) != 0 {
		// 末批删空后所有累计重数应为 0（键可能保留零值，计数即可）。
		for triple, m := range accum {
			if m != 0 {
				t.Fatalf("residual mult %d for %v after deleting all", m, triple)
			}
		}
	}
	if len(j.Snapshot()) != 0 {
		t.Fatalf("expected empty final snapshot, got %v", j.Snapshot())
	}
}

func TestDeterminismSameSequenceSameOutput(t *testing.T) {
	script := []Change{
		{Left: []Row{{Key: "b", Value: "a", Mult: 2}, {Key: "a", Value: "a", Mult: 1}}, Right: []Row{{Key: "a", Value: "z", Mult: 1}, {Key: "b", Value: "z", Mult: 1}}},
		{Right: []Row{{Key: "a", Value: "y", Mult: 3}}},
		{Left: []Row{{Key: "a", Value: "a", Mult: 2}, {Key: "b", Value: "a", Mult: -1}}},
		{Left: []Row{{Key: "a", Value: "a", Mult: -3}}, Right: []Row{{Key: "b", Value: "z", Mult: -1}}},
	}
	run := func() [][]DiffEntry {
		j := NewJoiner(Options{})
		out := make([][]DiffEntry, 0, len(script))
		for _, ch := range script {
			d, err := j.Apply(ch)
			if err != nil {
				t.Fatal(err)
			}
			out = append(out, d)
		}
		return out
	}
	r1, r2 := run(), run()
	if fmt.Sprint(r1) != fmt.Sprint(r2) {
		t.Fatalf("non-deterministic output:\n%v\n%v", r1, r2)
	}
}

func TestConcurrentAppliesAndSnapshots(t *testing.T) {
	j := NewJoiner(Options{})
	const goroutines = 8
	const perG = 50

	var wg sync.WaitGroup
	// 写者：每个 goroutine 占用互不相交的键空间，所有批都应成功。
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < perG; i++ {
				k := fmt.Sprintf("g%d-k%d", g, i)
				_, err := j.Apply(Change{
					Left:  []Row{{Key: k, Value: "L", Mult: int64(i + 1)}},
					Right: []Row{{Key: k, Value: "R", Mult: 2}},
				})
				if err != nil {
					t.Errorf("apply: %v", err)
					return
				}
			}
		}(g)
	}

	// 读者：并发热取一致性视图，与同一时刻基表重算对比。
	stop := make(chan struct{})
	readerDone := make(chan struct{})
	var readerErr error
	var readerOnce sync.Once
	go func() {
		defer close(readerDone)
		for {
			select {
			case <-stop:
				return
			default:
				j.mu.RLock()
				if err := checkInvariants(j, "concurrent reader"); err != nil {
					readerOnce.Do(func() { readerErr = err })
					j.mu.RUnlock()
					return
				}
				snap := j.snapshotLocked()
				j.mu.RUnlock()
				for _, e := range snap {
					if e.Mult <= 0 {
						readerOnce.Do(func() {
							readerErr = fmt.Errorf("negative/zero mult in view: %v", e)
						})
						return
					}
				}
			}
		}
	}()

	wg.Wait()
	close(stop)
	<-readerDone
	if readerErr != nil {
		t.Fatal(readerErr)
	}

	// 最终：总元组数 = sum_g sum_i (i+1)*2。
	var wantTotal int64
	for i := 0; i < perG; i++ {
		wantTotal += int64(i+1) * 2
	}
	wantTotal *= goroutines
	if j.totalResult != wantTotal {
		t.Fatalf("total=%d want=%d", j.totalResult, wantTotal)
	}
	assertInvariants(t, j, "final")
}

func TestEmptyBatchIsNoop(t *testing.T) {
	j := NewJoiner(Options{})
	d, err := j.Apply(Change{})
	if err != nil || len(d) != 0 {
		t.Fatalf("empty batch: d=%v err=%v", d, err)
	}
}

func TestLoggingContents(t *testing.T) {
	lg := &bufLogger{}
	j := NewJoiner(Options{Logger: lg})

	if _, err := j.Apply(Change{Left: []Row{{Key: "k", Value: "a", Mult: 1}}, Right: []Row{{Key: "k", Value: "x", Mult: 1}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := j.Apply(Change{Left: []Row{{Key: "k", Value: "a", Mult: -1}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := j.Apply(Change{Left: []Row{{Key: "k", Value: "a", Mult: -9}}}); err == nil {
		t.Fatal("want rejection")
	}

	log := lg.String()
	for _, want := range []string{
		"apply begin",                        // 输入
		`left=[(key="k",val="a",mult=1)]`,    // 输入内容
		`right=[(key="k",val="x",mult=1)]`,   // 输入内容
		"(key=\"k\",l=\"a\",r=\"x\",mult=1)", // 输出差分
		"mult=-1",                            // 负差分
		"apply committed",                    // 判定依据：提交
		"basis: validation ok",               // 判定依据
		"apply rejected",                     // 判定依据：拒绝
		"DELETE_NONEXISTENT_ROW",             // 拒绝原因
	} {
		if !strings.Contains(log, want) {
			t.Errorf("log missing %q\n--- log ---\n%s", want, log)
		}
	}
}

func TestAggregateOverflowRejected(t *testing.T) {
	j := NewJoiner(Options{})
	// 同一 (key,value) 两行正增量之和溢出 int64。
	_, err := j.Apply(Change{Left: []Row{
		{Key: "k", Value: "a", Mult: maxInt64},
		{Key: "k", Value: "a", Mult: maxInt64},
	}})
	if reject, ok := err.(*RejectError); !ok || reject.Reason != ReasonMultOverflow {
		t.Fatalf("want MULTIPLICITY_OVERFLOW on aggregate, got %v", err)
	}
	if len(j.LeftSnapshot()) != 0 || len(j.Snapshot()) != 0 {
		t.Fatal("rejected batch mutated state")
	}

	// 一正一负但负端绝对值过大导致求和溢出（MinInt64 无法取绝对值的极端情形）。
	_, err = j.Apply(Change{Right: []Row{
		{Key: "k", Value: "x", Mult: -1},
		{Key: "k", Value: "x", Mult: minInt64},
	}})
	if reject, ok := err.(*RejectError); !ok || reject.Reason != ReasonMultOverflow {
		t.Fatalf("want MULTIPLICITY_OVERFLOW on aggregate (mixed signs), got %v", err)
	}
	if len(j.RightSnapshot()) != 0 {
		t.Fatal("rejected batch mutated state")
	}
}

func TestProductOverflowRejected(t *testing.T) {
	j := NewJoiner(Options{})
	// 左行重数 MaxInt64、右行 1：乘积不溢出，提交。
	if _, err := j.Apply(Change{
		Left:  []Row{{Key: "k", Value: "a", Mult: maxInt64}},
		Right: []Row{{Key: "k", Value: "x", Mult: 1}},
	}); err != nil {
		t.Fatal(err)
	}
	before := j.Snapshot()
	// 右行 1->2：乘积 2*MaxInt64 溢出 int64，拒绝整批且状态不变。
	_, err := j.Apply(Change{Right: []Row{{Key: "k", Value: "x", Mult: 1}}})
	if reject, ok := err.(*RejectError); !ok || reject.Reason != ReasonMultOverflow {
		t.Fatalf("want MULTIPLICITY_OVERFLOW, got %v", err)
	}
	if got := j.Snapshot(); !entriesEqual(got, before) {
		t.Fatalf("state mutated after overflow: %v vs %v", got, before)
	}
	if j.totalResult != maxInt64 {
		t.Fatalf("totalResult=%d", j.totalResult)
	}
	assertInvariants(t, j, "overflow")
}

func TestRandomizedDiffsMatchRecompute(t *testing.T) {
	// 随机生成始终合法的批次（删除量不超过现存重数），验证：
	// 1) 顺序应用差分恒等于全量快照；2) 全量快照恒等于从基表重算；3) 重放序列输出完全一致。
	rng := rand.New(rand.NewSource(20260928))
	const batches = 300
	gen := func() (*Joiner, []Change, [][]DiffEntry) {
		j := NewJoiner(Options{})
		script := make([]Change, 0, batches)
		outputs := make([][]DiffEntry, 0, batches)
		// 直接跟踪两侧重数，用于保证生成的删除合法。
		lcur := map[string]int64{}
		rcur := map[string]int64{}
		keys := []string{"k1", "k2", "k3"}
		vals := []string{"a", "b", "c"}

		pick := func(s []string) string { return s[rng.Intn(len(s))] }
		for b := 0; b < batches; b++ {
			ch := Change{}
			if rng.Intn(2) == 0 {
				k, v := pick(keys), pick(vals)
				cur := lcur[k+"|"+v]
				d := randomDelta(rng, cur)
				if d != 0 {
					ch.Left = []Row{{Key: k, Value: v, Mult: d}}
					lcur[k+"|"+v] = cur + d
				}
			}
			if rng.Intn(2) == 0 {
				k, v := pick(keys), pick(vals)
				cur := rcur[k+"|"+v]
				d := randomDelta(rng, cur)
				if d != 0 {
					ch.Right = []Row{{Key: k, Value: v, Mult: d}}
					rcur[k+"|"+v] = cur + d
				}
			}
			if len(ch.Left) == 0 && len(ch.Right) == 0 {
				b--
				continue
			}
			diff, err := j.Apply(ch)
			if err != nil {
				t.Fatalf("batch %d %+v: %v", b, ch, err)
			}
			script = append(script, ch)
			outputs = append(outputs, diff)
		}
		return j, script, outputs
	}

	j1, script, out1 := gen()

	// 从差分累加重建并与快照、重算对比。
	accum := make(map[resultTriple]int64)
	for _, d := range out1 {
		for _, e := range d {
			accum[resultTriple{e.Key, e.LeftVal, e.RightVal}] += e.Mult
		}
	}
	fromDiff := make([]DiffEntry, 0)
	for triple, m := range accum {
		if m != 0 {
			fromDiff = append(fromDiff, DiffEntry{Key: triple.k, LeftVal: triple.l, RightVal: triple.r, Mult: m})
		}
	}
	sortDiff(fromDiff)
	snap := j1.Snapshot()
	if !entriesEqual(fromDiff, snap) {
		t.Fatalf("accumulated diffs != snapshot\n %v\n %v", fromDiff, snap)
	}
	if !entriesEqual(snap, fullRecompute(j1.left, j1.right)) {
		t.Fatalf("snapshot != recompute: %v", snap)
	}

	// 重放同一序列：输出必须逐批完全相同。
	j2 := NewJoiner(Options{})
	for i, ch := range script {
		d, err := j2.Apply(ch)
		if err != nil {
			t.Fatalf("replay batch %d: %v", i, err)
		}
		if !entriesEqual(d, out1[i]) {
			t.Fatalf("batch %d output differs on replay:\n %v\n %v", i, d, out1[i])
		}
	}
}

// randomDelta 生成一个不使 cur 变负的非零增量；cur 为当前重数。
func randomDelta(rng *rand.Rand, cur int64) int64 {
	if cur == 0 {
		return int64(rng.Intn(3) + 1)
	}
	switch rng.Intn(3) {
	case 0:
		return int64(rng.Intn(3) + 1) // 插入 1..3
	case 1:
		return -(int64(rng.Intn(int(cur)) + 1)) // 删除 1..cur（恒合法）
	default:
		if cur <= 3 {
			return -cur // 全删
		}
		return -int64(rng.Intn(3) + 1)
	}
}

func TestOverflowHelpers(t *testing.T) {
	if _, ok := addChecked(maxInt64, 1); ok {
		t.Fatal("add overflow not detected")
	}
	if _, ok := addChecked(minInt64, -1); ok {
		t.Fatal("add negative overflow not detected")
	}
	if v, ok := addChecked(5, -3); !ok || v != 2 {
		t.Fatalf("addChecked normal: %d %v", v, ok)
	}
	if _, ok := mulChecked(maxInt64, 2); ok {
		t.Fatal("mul overflow not detected")
	}
	if v, ok := mulChecked(3, 4); !ok || v != 12 {
		t.Fatalf("mulChecked normal: %d %v", v, ok)
	}
}

const (
	maxInt64 = int64(1<<63 - 1)
	minInt64 = -maxInt64 - 1
)

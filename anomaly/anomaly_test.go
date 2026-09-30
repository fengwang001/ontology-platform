package anomaly

import (
	"fmt"
	"math/rand"
	"reflect"
	"sort"
	"sync"
	"testing"
)

func v0() Version { return Version{0, 0} }

func rd(k string, v Version) Op {
	return Op{Key: k, Read: &v}
}
func wr(k string) Op { return Op{Key: k} }

func wantResult(t *testing.T, r Result, cat Category, level string, seq []int, edges []EdgeType) {
	t.Helper()
	if r.Category != cat || r.Level != level {
		t.Fatalf("want %s/%s, got %s/%s (%s)", cat, level, r.Category, r.Level, r.Reason)
	}
	if !reflect.DeepEqual(r.Witness, seq) {
		t.Fatalf("witness want %v, got %v", seq, r.Witness)
	}
	gotTypes := make([]EdgeType, len(r.Edges))
	for i, e := range r.Edges {
		gotTypes[i] = e.Type
	}
	if !reflect.DeepEqual(gotTypes, edges) {
		t.Fatalf("edge types want %v, got %v", edges, gotTypes)
	}
}

func logResult(t *testing.T, name string, h History, r Result) {
	t.Helper()
	t.Logf("case=%s input=%+v output category=%q level=%q witness=%v edges=%+v reason=%q rejected=%v err=%v",
		name, h, r.Category, r.Level, r.Witness, r.Edges, r.Reason, r.Rejected, r.Err)
}

// 丢失更新：T1、T2 都读 x0 后写 x；环 T1 -(rw)-> T2 -(ww)-> T1 => G-single。
func TestLostUpdate(t *testing.T) {
	h := History{
		Txns: []Txn{
			{ID: 1, Status: Committed, Ops: []Op{rd("x", v0()), wr("x")}},
			{ID: 2, Status: Committed, Ops: []Op{rd("x", v0()), wr("x")}},
		},
		Order: map[string][]Version{
			"x": {{0, 0}, {1, 1}, {2, 1}},
		},
	}
	r := Analyze(h)
	logResult(t, "lost-update", h, r)
	wantResult(t, r, GSingle, "PL-2", []int{1, 2}, []EdgeType{WW, RW})
}

// 读偏斜：T1 读到旧 x0 与 T2 写的新 y；边为一条 rw 与一条 wr => G-single。
func TestReadSkew(t *testing.T) {
	h := History{
		Txns: []Txn{
			{ID: 1, Status: Committed, Ops: []Op{rd("x", v0()), rd("y", Version{2, 1})}},
			{ID: 2, Status: Committed, Ops: []Op{wr("x"), wr("y")}},
		},
		Order: map[string][]Version{
			"x": {{0, 0}, {2, 1}},
			"y": {{0, 0}, {2, 1}},
		},
	}
	r := Analyze(h)
	logResult(t, "read-skew", h, r)
	wantResult(t, r, GSingle, "PL-2", []int{1, 2}, []EdgeType{RW, WR})
}

// 写偏斜：两条 rw 边构成环 => G2。
func TestWriteSkew(t *testing.T) {
	h := History{
		Txns: []Txn{
			{ID: 1, Status: Committed, Ops: []Op{rd("x", v0()), wr("y")}},
			{ID: 2, Status: Committed, Ops: []Op{rd("y", v0()), wr("x")}},
		},
		Order: map[string][]Version{
			"x": {{0, 0}, {2, 1}},
			"y": {{0, 0}, {1, 1}},
		},
	}
	r := Analyze(h)
	logResult(t, "write-skew", h, r)
	wantResult(t, r, G2, "PL-2+", []int{1, 2}, []EdgeType{RW, RW})
}

// G1a：已提交事务读到已中止事务写的版本。
func TestReadAborted(t *testing.T) {
	h := History{
		Txns: []Txn{
			{ID: 1, Status: Aborted, Ops: []Op{wr("x")}},
			{ID: 2, Status: Committed, Ops: []Op{rd("x", Version{1, 1})}},
		},
		Order: map[string][]Version{"x": {{0, 0}}},
	}
	r := Analyze(h)
	logResult(t, "read-aborted", h, r)
	if r.Category != G1a || r.Level != "PL-1" || r.Witness != nil {
		t.Fatalf("want G1a/PL-1 no witness, got %+v", r)
	}
}

// G1b：已提交事务读到别的事务对该键的非最后一次写。
func TestReadIntermediate(t *testing.T) {
	h := History{
		Txns: []Txn{
			{ID: 1, Status: Committed, Ops: []Op{wr("x"), wr("x")}},
			{ID: 2, Status: Committed, Ops: []Op{rd("x", Version{1, 1})}},
		},
		Order: map[string][]Version{"x": {{0, 0}, {1, 2}}},
	}
	r := Analyze(h)
	logResult(t, "read-intermediate", h, r)
	if r.Category != G1b || r.Level != "PL-1" || r.Witness != nil {
		t.Fatalf("want G1b/PL-1 no witness, got %+v", r)
	}
}

// G0：纯写写环。
func TestWriteWriteCycle(t *testing.T) {
	h := History{
		Txns: []Txn{
			{ID: 1, Status: Committed, Ops: []Op{wr("x"), wr("y"), wr("y")}},
			{ID: 2, Status: Committed, Ops: []Op{wr("x"), wr("y"), wr("y")}},
		},
		Order: map[string][]Version{
			"x": {{0, 0}, {1, 1}, {2, 1}},
			"y": {{0, 0}, {2, 2}, {1, 2}},
		},
	}
	r := Analyze(h)
	logResult(t, "ww-cycle", h, r)
	wantResult(t, r, G0, "无", []int{1, 2}, []EdgeType{WW, WW})
}

// G1c：仅由 ww 与 wr 边组成、且至少一条 wr 的环。
func TestG1c(t *testing.T) {
	h := History{
		Txns: []Txn{
			{ID: 1, Status: Committed, Ops: []Op{rd("x", Version{2, 1}), wr("y")}},
			{ID: 2, Status: Committed, Ops: []Op{rd("y", Version{1, 1}), wr("x")}},
		},
		Order: map[string][]Version{
			"x": {{0, 0}, {2, 1}},
			"y": {{0, 0}, {1, 1}},
		},
	}
	r := Analyze(h)
	logResult(t, "g1c", h, r)
	wantResult(t, r, G1c, "PL-1", []int{1, 2}, []EdgeType{WR, WR})
}

// 无异常 => PL-3。
func TestSerializable(t *testing.T) {
	h := History{
		Txns: []Txn{
			{ID: 1, Status: Committed, Ops: []Op{wr("x")}},
			{ID: 2, Status: Committed, Ops: []Op{rd("x", Version{1, 1})}},
		},
		Order: map[string][]Version{"x": {{0, 0}, {1, 1}}},
	}
	r := Analyze(h)
	logResult(t, "serializable", h, r)
	if r.Category != None || r.Level != "PL-3" {
		t.Fatalf("want PL-3, got %+v", r)
	}
}

// 多类并存：G0（纯 ww 环）必须最先命中，即使同图还能走出含 rw 的环。
func TestPrecedenceG0OverRest(t *testing.T) {
	h := History{
		Txns: []Txn{
			{ID: 1, Status: Committed, Ops: []Op{rd("y", v0()), wr("x"), wr("y"), wr("y")}},
			{ID: 2, Status: Committed, Ops: []Op{wr("x"), wr("y"), wr("y")}},
		},
		Order: map[string][]Version{
			"x": {{0, 0}, {1, 1}, {2, 1}},
			"y": {{0, 0}, {2, 2}, {1, 2}},
		},
	}
	r := Analyze(h)
	logResult(t, "precedence-G0", h, r)
	if r.Category != G0 {
		t.Fatalf("want G0 first, got %s (%s)", r.Category, r.Reason)
	}
}

// G1a 优先于任何环类。
func TestPrecedenceG1a(t *testing.T) {
	h := History{
		Txns: []Txn{
			{ID: 1, Status: Aborted, Ops: []Op{wr("x")}},
			{ID: 2, Status: Committed, Ops: []Op{rd("x", Version{1, 1}), wr("y")}},
			{ID: 3, Status: Committed, Ops: []Op{rd("y", v0()), wr("y")}},
		},
		Order: map[string][]Version{
			"x": {{0, 0}},
			"y": {{0, 0}, {3, 1}, {2, 1}},
		},
	}
	r := Analyze(h)
	logResult(t, "precedence-G1a", h, r)
	if r.Category != G1a {
		t.Fatalf("want G1a first, got %s", r.Category)
	}
}

// G1b 优先于环类。
func TestPrecedenceG1b(t *testing.T) {
	h := History{
		Txns: []Txn{
			{ID: 1, Status: Committed, Ops: []Op{wr("x"), wr("x"), rd("y", v0())}},
			{ID: 2, Status: Committed, Ops: []Op{rd("x", Version{1, 1}), wr("y")}},
		},
		Order: map[string][]Version{
			"x": {{0, 0}, {1, 2}},
			"y": {{0, 0}, {2, 1}},
		},
	}
	r := Analyze(h)
	logResult(t, "precedence-G1b", h, r)
	if r.Category != G1b {
		t.Fatalf("want G1b before cycle class, got %s", r.Category)
	}
}

func TestValidation(t *testing.T) {
	base := func() History {
		return History{
			Txns: []Txn{
				{ID: 1, Status: Committed, Ops: []Op{rd("x", v0()), wr("x")}},
			},
			Order: map[string][]Version{"x": {{0, 0}, {1, 1}}},
		}
	}

	cases := []struct {
		name string
		mut  func(*History)
		code string
	}{
		{"zero-id", func(h *History) { h.Txns[0].ID = 0 }, ErrTxnID},
		{"dup-id", func(h *History) {
			h.Txns = append(h.Txns, Txn{ID: 1, Status: Aborted})
		}, ErrTxnID},
		{"bad-status", func(h *History) { h.Txns[0].Status = "x" }, ErrStatus},
		{"missing-version", func(h *History) { h.Txns[0].Ops[0] = rd("x", Version{2, 1}) }, ErrReadVersion},
		{"order-aborted", func(h *History) {
			h.Txns[0].Status = Aborted
			h.Order["x"] = []Version{{0, 0}, {1, 1}}
		}, ErrOrder},
		{"order-intermediate", func(h *History) {
			h.Txns[0].Ops = append(h.Txns[0].Ops, wr("x"))
			h.Order["x"] = []Version{{0, 0}, {1, 1}, {1, 2}}
		}, ErrOrder},
		{"order-missing-last", func(h *History) {
			h.Order["x"] = []Version{{0, 0}}
		}, ErrOrder},
		{"too-many", func(h *History) {
			for i := 2; i <= 13; i++ {
				h.Txns = append(h.Txns, Txn{ID: i, Status: Aborted})
			}
		}, ErrTooMany},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := base()
			c.mut(&h)
			r := Analyze(h)
			logResult(t, "reject-"+c.name, h, r)
			if !r.Rejected || r.ErrCode != c.code {
				t.Fatalf("want rejection %s, got %+v", c.code, r)
			}
		})
	}
}

// 多种输入违规并存时严格按题面顺序只报第一个。
func TestRejectionPriority(t *testing.T) {
	// 同时有：编号 0、非法状态、引用不存在版本、次序非法、事务超限。
	h := History{
		Txns: []Txn{
			{ID: 0, Status: "weird", Ops: []Op{rd("x", Version{9, 9})}},
			{ID: 0, Status: Committed},
			{ID: 2, Status: Committed},
			{ID: 3, Status: Committed},
			{ID: 4, Status: Committed},
			{ID: 5, Status: Committed},
			{ID: 6, Status: Committed},
			{ID: 7, Status: Committed},
			{ID: 8, Status: Committed},
			{ID: 9, Status: Committed},
			{ID: 10, Status: Committed},
			{ID: 11, Status: Committed},
			{ID: 12, Status: Committed},
		},
		Order: map[string][]Version{"x": {{7, 1}}},
	}
	r := Analyze(h)
	logResult(t, "reject-priority", h, r)
	if r.ErrCode != ErrTxnID {
		t.Fatalf("want ErrTxnID first, got %s", r.ErrCode)
	}
}

// 被拒绝的判定不改变输入状态。
func TestRejectionIsImmutable(t *testing.T) {
	h := History{Txns: []Txn{{ID: 0, Status: Committed}}}
	before := fmt.Sprintf("%#v", h)
	_ = Analyze(h)
	if fmt.Sprintf("%#v", h) != before {
		t.Fatal("Analyze mutated rejected input")
	}
}

// 排列无关：打乱事务与操作顺序后，类别/等级/见证环完全一致。
func TestPermutationIndependent(t *testing.T) {
	h := History{
		Txns: []Txn{
			{ID: 3, Status: Committed, Ops: []Op{wr("z"), rd("x", v0())}},
			{ID: 1, Status: Committed, Ops: []Op{rd("z", v0()), wr("x"), wr("y")}},
			{ID: 2, Status: Committed, Ops: []Op{wr("z"), wr("x"), rd("y", Version{1, 3})}},
		},
		Order: map[string][]Version{
			"x": {{0, 0}, {1, 2}, {2, 2}},
			"y": {{0, 0}, {1, 3}},
			"z": {{0, 0}, {3, 1}, {2, 1}},
		},
	}
	want := Analyze(h)
	rng := rand.New(rand.NewSource(42))
	for iter := 0; iter < 20; iter++ {
		shuffled := h
		shuffled.Txns = append([]Txn(nil), h.Txns...)
		rng.Shuffle(len(shuffled.Txns), func(i, j int) {
			shuffled.Txns[i], shuffled.Txns[j] = shuffled.Txns[j], shuffled.Txns[i]
		})
		for i := range shuffled.Txns {
			ops := append([]Op(nil), shuffled.Txns[i].Ops...)
			rng.Shuffle(len(ops), func(a, b int) { ops[a], ops[b] = ops[b], ops[a] })
			shuffled.Txns[i].Ops = ops
		}
		got := Analyze(shuffled)
		if got.Category != want.Category || got.Level != want.Level ||
			!reflect.DeepEqual(got.Witness, want.Witness) {
			t.Fatalf("iter %d: got %+v want %+v", iter, got, want)
		}
	}
	logResult(t, "permutation-independent", h, want)
}

// 并发调用：-race 下并发判定同一历史结果一致。
func TestConcurrent(t *testing.T) {
	h := History{
		Txns: []Txn{
			{ID: 1, Status: Committed, Ops: []Op{rd("x", v0()), wr("y")}},
			{ID: 2, Status: Committed, Ops: []Op{rd("y", v0()), wr("x")}},
		},
		Order: map[string][]Version{
			"x": {{0, 0}, {2, 1}},
			"y": {{0, 0}, {1, 1}},
		},
	}
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r := Analyze(h)
			if r.Category != G2 {
				t.Errorf("concurrent Analyze got %s", r.Category)
			}
		}()
	}
	wg.Wait()
}

// 最短见证环：存在更短环时取最短者。
func TestShortestWitness(t *testing.T) {
	h := History{
		Txns: []Txn{
			// T1、T2 构成 2 环；T3、T4、T5 构成 3 环，键互不相交。
			{ID: 1, Status: Committed, Ops: []Op{rd("x", v0()), wr("y")}},
			{ID: 2, Status: Committed, Ops: []Op{rd("y", v0()), wr("x")}},
			{ID: 3, Status: Committed, Ops: []Op{rd("a", v0()), wr("b")}},
			{ID: 4, Status: Committed, Ops: []Op{rd("b", v0()), wr("c")}},
			{ID: 5, Status: Committed, Ops: []Op{rd("c", v0()), wr("a")}},
		},
		Order: map[string][]Version{
			"x": {{0, 0}, {2, 1}},
			"y": {{0, 0}, {1, 1}},
			"a": {{0, 0}, {5, 1}},
			"b": {{0, 0}, {3, 1}},
			"c": {{0, 0}, {4, 1}},
		},
	}
	r := Analyze(h)
	logResult(t, "witness-selection", h, r)
	if r.Category != G2 || !reflect.DeepEqual(r.Witness, []int{1, 2}) {
		t.Fatalf("want G2 shortest witness [1 2], got %s %v (%s)", r.Category, r.Witness, r.Reason)
	}
}

// 等长环取事务编号序列字典序最小，且以环内最小编号起头。
func TestLexWitness(t *testing.T) {
	// 两对相互独立的写偏斜：环 [1,4] 与 [2,3]，取 [1,4]。
	h := History{
		Txns: []Txn{
			{ID: 1, Status: Committed, Ops: []Op{rd("a", v0()), wr("b")}},
			{ID: 4, Status: Committed, Ops: []Op{rd("b", v0()), wr("a")}},
			{ID: 2, Status: Committed, Ops: []Op{rd("c", v0()), wr("d")}},
			{ID: 3, Status: Committed, Ops: []Op{rd("d", v0()), wr("c")}},
		},
		Order: map[string][]Version{
			"a": {{0, 0}, {4, 1}},
			"b": {{0, 0}, {1, 1}},
			"c": {{0, 0}, {3, 1}},
			"d": {{0, 0}, {2, 1}},
		},
	}
	r := Analyze(h)
	logResult(t, "lex-witness", h, r)
	if r.Category != G2 || !reflect.DeepEqual(r.Witness, []int{1, 4}) {
		t.Fatalf("want lex-smallest witness [1 4], got %s %v", r.Category, r.Witness)
	}
}

// genRandomHistory 生成只含已提交事务的小历史（环类/G1b 场景），
// 并构造合法的版本次序：每个键的版本写入者按随机排列，同事务保留其最后一次写。
func genRandomHistory(rng *rand.Rand) History {
	n := 2 + rng.Intn(5) // 2..6 个事务
	keys := []string{"x", "y", "z"}
	type writeRec struct {
		txn int
		seq int
	}
	lastWriter := map[string]map[int]int{} // key -> txn -> 该 txn 对 key 的总写次
	keyWriters := map[string][]int{}

	txns := make([]Txn, n)
	for i := range txns {
		txns[i] = Txn{ID: i + 1, Status: Committed}
	}

	// 先决定每个事务对每个键写几次（0..2）。
	perTxnWrites := make([][]int, n)
	for i := range perTxnWrites {
		perTxnWrites[i] = make([]int, len(keys))
		for k := range keys {
			if rng.Intn(2) == 0 {
				perTxnWrites[i][k] = rng.Intn(3)
			}
		}
	}
	// 保证每个键至少有一个写者。
	for k := range keys {
		has := false
		for i := 0; i < n; i++ {
			if perTxnWrites[i][k] > 0 {
				has = true
			}
		}
		if !has {
			perTxnWrites[rng.Intn(n)][k] = 1
		}
	}

	order := map[string][]Version{}
	for ki, name := range keys {
		var writers []int
		for i := 0; i < n; i++ {
			if c := perTxnWrites[i][ki]; c > 0 {
				writers = append(writers, i+1)
			}
		}
		keyWriters[name] = writers
		if lastWriter[name] == nil {
			lastWriter[name] = map[int]int{}
		}
		for _, w := range writers {
			lastWriter[name][w] = perTxnWrites[w-1][ki]
		}
		rng.Shuffle(len(writers), func(a, b int) { writers[a], writers[b] = writers[b], writers[a] })
		vs := []Version{{0, 0}}
		for _, w := range writers {
			vs = append(vs, Version{w, lastWriter[name][w]})
		}
		order[name] = vs
	}

	// 生成操作：写操作按 seq 展开；读操作以一定概率读次序内某版本
	// （小概率读中间版本以触发 G1b）。
	pos := map[string]map[Version]bool{}
	for name, vs := range order {
		pos[name] = map[Version]bool{}
		for _, x := range vs {
			pos[name][x] = true
		}
	}
	for i := range txns {
		var ops []Op
		for ki, name := range keys {
			for s := 0; s < perTxnWrites[i][ki]; s++ {
				ops = append(ops, wr(name))
			}
		}
		// 每个事务随机读 1~2 个键。
		readKeys := append([]string(nil), keys...)
		rng.Shuffle(len(readKeys), func(a, b int) { readKeys[a], readKeys[b] = readKeys[b], readKeys[a] })
		for _, name := range readKeys[:1+rng.Intn(2)] {
			var chosen Version
			if rng.Intn(5) == 0 {
				// 可能读自己的中间版本（不违规）或他人的中间版本（G1b）。
				tid := rng.Intn(n) + 1
				c := perTxnWrites[tid-1][keyID(name)]
				if c >= 2 {
					chosen = Version{tid, 1 + rng.Intn(c-1)}
				} else {
					chosen = order[name][rng.Intn(len(order[name]))]
				}
			} else {
				chosen = order[name][rng.Intn(len(order[name]))]
			}
			ops = append(ops, rd(name, chosen))
		}
		rng.Shuffle(len(ops), func(a, b int) { ops[a], ops[b] = ops[b], ops[a] })
		txns[i].Ops = ops
	}

	return History{Txns: txns, Order: order}
}

func keyID(k string) int {
	for i, name := range []string{"x", "y", "z"} {
		if name == k {
			return i
		}
	}
	return -1
}

func normalizeSeq(seq []int) {
	minPos := 0
	for i := 1; i < len(seq); i++ {
		if seq[i] < seq[minPos] {
			minPos = i
		}
	}
	out := append([]int(nil), seq...)
	for i := range seq {
		seq[i] = out[(minPos+i)%len(out)]
	}
}

func lexLess(a, b []int) bool {
	if len(a) != len(b) {
		return len(a) < len(b)
	}
	for i := range a {
		if a[i] != b[i] {
			return a[i] < b[i]
		}
	}
	return false
}

// oracleAnalyze 是独立参照：枚举所有简单环节点序列与每步的具体边类型，
// 逐环穷举各类最早命中情况；见证环取最短、等长字典序最小。
func oracleAnalyze(h History) Result {
	v, err := validate(h)
	if err != nil {
		return Result{Rejected: true, ErrCode: err.(*invalidHistory).code}
	}
	g, g1a, g1b := buildGraph(h, v)
	switch {
	case g1a != nil:
		return Result{Category: G1a, Level: "PL-1"}
	case g1b != nil:
		return Result{Category: G1b, Level: "PL-1"}
	}

	n := len(g.nodes)
	mask := make([][]EdgeType, n)
	for i := range mask {
		mask[i] = make([]EdgeType, n)
	}
	for e, t := range g.edges {
		a := sort.SearchInts(g.nodes, e[0])
		b := sort.SearchInts(g.nodes, e[1])
		mask[a][b] = t
	}

	bestSeq := map[Category][]int{}
	consider := func(perm []int) {
		m := len(perm)
		options := make([][]EdgeType, m)
		for i := range perm {
			t := mask[perm[i]][perm[(i+1)%m]]
			if t == 0 {
				return
			}
			for _, et := range []EdgeType{WW, WR, RW} {
				if t&et != 0 {
					options[i] = append(options[i], et)
				}
			}
		}
		chosen := make([]EdgeType, m)
		var combos func(pos int)
		combos = func(pos int) {
			if pos == m {
				cat := classify(chosen)
				seq := make([]int, m)
				for i, p := range perm {
					seq[i] = g.nodes[p]
				}
				normalizeSeq(seq)
				if b, ok := bestSeq[cat]; !ok || lexLess(seq, b) {
					bestSeq[cat] = append([]int(nil), seq...)
				}
				return
			}
			for _, et := range options[pos] {
				chosen[pos] = et
				combos(pos + 1)
			}
		}
		combos(0)
	}

	// 枚举所有简单环（最小下标固定在首位）。
	used := make([]bool, n)
	var perm []int
	var gen func(first int)
	gen = func(first int) {
		if len(perm) >= 2 {
			consider(perm)
		}
		for nxt := first + 1; nxt < n; nxt++ {
			if !used[nxt] {
				used[nxt] = true
				perm = append(perm, nxt)
				gen(first)
				perm = perm[:len(perm)-1]
				used[nxt] = false
			}
		}
	}
	for first := 0; first < n; first++ {
		perm = []int{first}
		used[first] = true
		gen(first)
		used[first] = false
	}

	for _, cat := range []Category{G0, G1c, GSingle, G2} {
		if seq, ok := bestSeq[cat]; ok {
			level := map[Category]string{G0: "无", G1c: "PL-1", GSingle: "PL-2", G2: "PL-2+"}[cat]
			return Result{Category: cat, Level: level, Witness: seq}
		}
	}
	return Result{Category: None, Level: "PL-3"}
}

// 随机小历史与逐环穷举参照对拍。
func TestRandomDifferential(t *testing.T) {
	rng := rand.New(rand.NewSource(20260930))
	for iter := 0; iter < 8000; iter++ {
		h := genRandomHistory(rng)
		got := Analyze(h)
		want := oracleAnalyze(h)
		if !resultsEquivalent(got, want) {
			t.Fatalf("iter %d mismatch:\ninput=%+v\ngot =%+v\nwant=%+v", iter, h, got, want)
		}
		if iter < 5 {
			logResult(t, fmt.Sprintf("random-%d", iter), h, got)
		}
	}
}

func resultsEquivalent(a, b Result) bool {
	if a.Rejected != b.Rejected {
		return false
	}
	if a.Rejected {
		return a.ErrCode == b.ErrCode
	}
	return a.Category == b.Category && a.Level == b.Level &&
		reflect.DeepEqual(a.Witness, b.Witness)
}

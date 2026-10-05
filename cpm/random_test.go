package cpm

import (
	"fmt"
	"math/rand"
	"reflect"
	"sort"
	"sync"
	"testing"
)

// naive 是每次更新后整网重算的朴素参照实现，用于对拍。
type naive struct {
	maxN, maxE int
	deadline   int64
	n          int
	dur        []int64
	snet       []int64
	fnlt       []int64
	edges      [][3]int64 // (u, v, lag)，按插入顺序
	edgeSet    map[[2]int]bool
	es, ef, lf []int64
	pf         int64
	crit       []int
	baseline   []int64
	succ       [][]edge
	pred       [][]edge
}

func newNaive(maxN, maxE int, deadline int64) *naive {
	return &naive{maxN: maxN, maxE: maxE, deadline: deadline, edgeSet: map[[2]int]bool{}}
}

func (n *naive) recompute() {
	n.succ = make([][]edge, n.n)
	n.pred = make([][]edge, n.n)
	for _, e := range n.edges {
		u, v, lag := int(e[0]), int(e[1]), e[2]
		n.succ[u] = append(n.succ[u], edge{to: v, lag: lag})
		n.pred[v] = append(n.pred[v], edge{to: u, lag: lag})
	}
	// Kahn 拓扑序（入度为 0 者按编号入队）。
	var queue []int
	indeg2 := make([]int, n.n)
	for v := 0; v < n.n; v++ {
		indeg2[v] = len(n.pred[v])
		if indeg2[v] == 0 {
			queue = append(queue, v)
		}
	}
	var order []int
	for len(queue) > 0 {
		u := queue[0]
		queue = queue[1:]
		order = append(order, u)
		for _, e := range n.succ[u] {
			indeg2[e.to]--
			if indeg2[e.to] == 0 {
				queue = append(queue, e.to)
			}
		}
	}
	n.es = make([]int64, n.n)
	n.ef = make([]int64, n.n)
	for _, v := range order {
		es := n.snet[v]
		for _, e := range n.pred[v] {
			if val := n.ef[e.to] + e.lag; val > es {
				es = val
			}
		}
		n.es[v] = es
		n.ef[v] = es + n.dur[v]
	}
	n.pf = 0
	for v := 0; v < n.n; v++ {
		if n.ef[v] > n.pf {
			n.pf = n.ef[v]
		}
	}
	n.lf = make([]int64, n.n)
	for i := len(order) - 1; i >= 0; i-- {
		v := order[i]
		lf := n.deadline
		if n.fnlt[v] >= 0 && n.fnlt[v] < lf {
			lf = n.fnlt[v]
		}
		for _, e := range n.succ[v] {
			if val := n.lf[e.to] - n.dur[e.to] - e.lag; val < lf {
				lf = val
			}
		}
		n.lf[v] = lf
	}
	n.crit = nil
	if n.n > 0 {
		minTF := n.lf[0] - n.ef[0]
		for v := 1; v < n.n; v++ {
			if tf := n.lf[v] - n.ef[v]; tf < minTF {
				minTF = tf
			}
		}
		for v := 0; v < n.n; v++ {
			if n.lf[v]-n.ef[v] == minTF {
				n.crit = append(n.crit, v)
			}
		}
	}
}

func (n *naive) snapshot() (es, lf []int64, pf int64, crit []int) {
	return append([]int64(nil), n.es...), append([]int64(nil), n.lf...), n.pf,
		append([]int(nil), n.crit...)
}

func (n *naive) makeReport(oldES, oldLF []int64, oldPF int64, oldCrit []int) Report {
	rep := Report{OldPF: oldPF, NewPF: n.pf}
	// 只统计既有任务的 ES/LF 变化（新任务的初始化不算变化）。
	for v := 0; v < len(oldES); v++ {
		if n.es[v] != oldES[v] {
			rep.ChangedES = append(rep.ChangedES, v)
		}
		if n.lf[v] != oldLF[v] {
			rep.ChangedLF = append(rep.ChangedLF, v)
		}
	}
	oldSet := map[int]bool{}
	for _, v := range oldCrit {
		oldSet[v] = true
	}
	newSet := map[int]bool{}
	for _, v := range n.crit {
		newSet[v] = true
		if !oldSet[v] {
			rep.CritAdded = append(rep.CritAdded, v)
		}
	}
	for _, v := range oldCrit {
		if !newSet[v] {
			rep.CritRemoved = append(rep.CritRemoved, v)
		}
	}
	sort.Ints(rep.CritAdded)
	sort.Ints(rep.CritRemoved)
	return rep
}

func (n *naive) addTask(dur int64) (Report, error) {
	if dur < 0 || dur > maxDuration {
		return Report{}, ErrInvalidParam
	}
	if n.n >= n.maxN {
		return Report{}, ErrTaskLimit
	}
	oldES, oldLF, oldPF, oldCrit := n.snapshot()
	n.dur = append(n.dur, dur)
	n.snet = append(n.snet, 0)
	n.fnlt = append(n.fnlt, noFNLT)
	n.n++
	n.recompute()
	return n.makeReport(oldES, oldLF, oldPF, oldCrit), nil
}

func (n *naive) reaches(u, v int) bool {
	// u 是否可沿 succ 到达 v（基于 edges）
	seen := map[int]bool{u: true}
	stack := []int{u}
	for len(stack) > 0 {
		x := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if x == v {
			return true
		}
		for _, e := range n.edges {
			if int(e[0]) == x && !seen[int(e[1])] {
				seen[int(e[1])] = true
				stack = append(stack, int(e[1]))
			}
		}
	}
	return false
}

func (n *naive) addDep(u, v int, lag int64) (Report, error) {
	if lag < -maxLag || lag > maxLag {
		return Report{}, ErrInvalidParam
	}
	if u < 0 || u >= n.n || v < 0 || v >= n.n {
		return Report{}, ErrTaskNotFound
	}
	if n.edgeSet[[2]int{u, v}] {
		return Report{}, ErrDepExists
	}
	if len(n.edges) >= n.maxE {
		return Report{}, ErrDepLimit
	}
	if u == v || n.reaches(v, u) {
		return Report{}, ErrCycle
	}
	oldES, oldLF, oldPF, oldCrit := n.snapshot()
	n.edges = append(n.edges, [3]int64{int64(u), int64(v), lag})
	n.edgeSet[[2]int{u, v}] = true
	n.recompute()
	return n.makeReport(oldES, oldLF, oldPF, oldCrit), nil
}

func (n *naive) removeDep(u, v int) (Report, error) {
	if u < 0 || u >= n.n || v < 0 || v >= n.n {
		return Report{}, ErrTaskNotFound
	}
	if !n.edgeSet[[2]int{u, v}] {
		return Report{}, ErrDepNotFound
	}
	oldES, oldLF, oldPF, oldCrit := n.snapshot()
	for i, e := range n.edges {
		if int(e[0]) == u && int(e[1]) == v {
			n.edges = append(n.edges[:i], n.edges[i+1:]...)
			break
		}
	}
	delete(n.edgeSet, [2]int{u, v})
	n.recompute()
	return n.makeReport(oldES, oldLF, oldPF, oldCrit), nil
}

func (n *naive) setDuration(v int, dur int64) (Report, error) {
	if dur < 0 || dur > maxDuration {
		return Report{}, ErrInvalidParam
	}
	if v < 0 || v >= n.n {
		return Report{}, ErrTaskNotFound
	}
	oldES, oldLF, oldPF, oldCrit := n.snapshot()
	n.dur[v] = dur
	n.recompute()
	return n.makeReport(oldES, oldLF, oldPF, oldCrit), nil
}

func (n *naive) setConstraint(v int, snet, fnlt int64) (Report, error) {
	if snet < 0 || snet > maxSNET || fnlt < noFNLT || fnlt > maxFNLT {
		return Report{}, ErrInvalidParam
	}
	if v < 0 || v >= n.n {
		return Report{}, ErrTaskNotFound
	}
	oldES, oldLF, oldPF, oldCrit := n.snapshot()
	n.snet[v] = snet
	n.fnlt[v] = fnlt
	n.recompute()
	return n.makeReport(oldES, oldLF, oldPF, oldCrit), nil
}

func (n *naive) setBaseline() {
	n.baseline = append([]int64(nil), n.ef...)
}

func (n *naive) variance(v int) (int64, error) {
	if v < 0 || v >= n.n {
		return 0, ErrTaskNotFound
	}
	if n.baseline == nil || v >= len(n.baseline) {
		return 0, ErrNoBaseline
	}
	return n.ef[v] - n.baseline[v], nil
}

func (n *naive) path() []int {
	if n.n == 0 {
		return nil
	}
	crit := map[int]bool{}
	for _, v := range n.crit {
		crit[v] = true
	}
	start := -1
	for _, v := range n.crit {
		has := false
		for _, e := range n.pred[v] {
			if crit[e.to] && n.es[v] == n.ef[e.to]+e.lag {
				has = true
				break
			}
		}
		if !has {
			start = v
			break
		}
	}
	p := []int{start}
	cur := start
	for {
		next := -1
		for _, e := range n.succ[cur] {
			if crit[e.to] && n.es[e.to] == n.ef[cur]+e.lag {
				if next == -1 || e.to < next {
					next = e.to
				}
			}
		}
		if next == -1 {
			break
		}
		p = append(p, next)
		cur = next
	}
	return p
}

func (n *naive) ff(v int) int64 {
	if len(n.succ[v]) == 0 {
		return n.pf - n.ef[v]
	}
	ff := n.es[n.succ[v][0].to] - n.succ[v][0].lag - n.ef[v]
	for _, e := range n.succ[v][1:] {
		if val := n.es[e.to] - e.lag - n.ef[v]; val < ff {
			ff = val
		}
	}
	return ff
}

type randOp struct {
	kind       string
	u, v       int
	lag        int64
	dur        int64
	snet, fnlt int64
}

func (o randOp) String() string {
	switch o.kind {
	case "addtask":
		return fmt.Sprintf("AddTask(dur=%d)", o.dur)
	case "adddep":
		return fmt.Sprintf("AddDep(%d,%d,lag=%d)", o.u, o.v, o.lag)
	case "removedep":
		return fmt.Sprintf("RemoveDep(%d,%d)", o.u, o.v)
	case "setdur":
		return fmt.Sprintf("SetDuration(%d,%d)", o.v, o.dur)
	case "setcon":
		return fmt.Sprintf("SetConstraint(%d,snet=%d,fnlt=%d)", o.v, o.snet, o.fnlt)
	default:
		return "SetBaseline()"
	}
}

func genOp(rng *rand.Rand, ntasks int) randOp {
	k := rng.Intn(100)
	randTask := func() int {
		if ntasks == 0 || rng.Intn(15) == 0 {
			return rng.Intn(ntasks + 3) // 可能指向不存在的任务
		}
		return rng.Intn(ntasks)
	}
	switch {
	case k < 20:
		dur := int64(rng.Intn(9))
		if rng.Intn(20) == 0 {
			dur = int64(rng.Intn(2000002)) - 500000 // 可能越界
		}
		return randOp{kind: "addtask", dur: dur}
	case k < 45:
		lag := int64(rng.Intn(11) - 5)
		if rng.Intn(20) == 0 {
			lag = int64(rng.Intn(4000002)) - 2000000 // 可能越界
		}
		return randOp{kind: "adddep", u: randTask(), v: randTask(), lag: lag}
	case k < 60:
		return randOp{kind: "removedep", u: randTask(), v: randTask()}
	case k < 75:
		dur := int64(rng.Intn(9))
		if rng.Intn(20) == 0 {
			dur = -1
		}
		return randOp{kind: "setdur", v: randTask(), dur: dur}
	case k < 90:
		snet := int64(rng.Intn(16))
		if rng.Intn(25) == 0 {
			snet = int64(rng.Intn(2000000002))
		}
		fnlt := int64(-1)
		if rng.Intn(2) == 0 {
			fnlt = int64(rng.Intn(46))
		}
		if rng.Intn(25) == 0 {
			fnlt = int64(rng.Intn(2000000000002))
		}
		return randOp{kind: "setcon", v: randTask(), snet: snet, fnlt: fnlt}
	default:
		return randOp{kind: "baseline"}
	}
}

func applyReal(c *CPM, o randOp) (Report, error) {
	switch o.kind {
	case "addtask":
		_, rep, err := c.AddTask(o.dur)
		return rep, err
	case "adddep":
		return c.AddDep(o.u, o.v, o.lag)
	case "removedep":
		return c.RemoveDep(o.u, o.v)
	case "setdur":
		return c.SetDuration(o.v, o.dur)
	case "setcon":
		return c.SetConstraint(o.v, o.snet, o.fnlt)
	default:
		c.SetBaseline()
		return Report{}, nil
	}
}

func applyNaive(n *naive, o randOp) (Report, error) {
	switch o.kind {
	case "addtask":
		return n.addTask(o.dur)
	case "adddep":
		return n.addDep(o.u, o.v, o.lag)
	case "removedep":
		return n.removeDep(o.u, o.v)
	case "setdur":
		return n.setDuration(o.v, o.dur)
	case "setcon":
		return n.setConstraint(o.v, o.snet, o.fnlt)
	default:
		n.setBaseline()
		return Report{}, nil
	}
}

// checkEvalBounds 校验 fwdEval/bwdEval 不超过题目给出的上界。
func checkEvalBounds(t *testing.T, c *CPM, o randOp, rep Report) (int, int) {
	t.Helper()
	var xset []int
	switch o.kind {
	case "addtask":
		xset = []int{c.n - 1}
	case "adddep", "removedep":
		xset = []int{o.u, o.v}
	case "setdur", "setcon":
		xset = []int{o.v}
	default:
		return 0, 0
	}
	fset := map[int]bool{}
	for _, v := range xset {
		fset[v] = true
	}
	for _, v := range rep.ChangedES {
		fset[v] = true
	}
	fb := len(xset)
	for v := range fset {
		fb += 1 + len(c.succ[v])
	}
	bset := map[int]bool{}
	for _, v := range xset {
		bset[v] = true
	}
	for _, v := range rep.ChangedLF {
		bset[v] = true
	}
	bb := len(xset)
	for v := range bset {
		bb += 1 + len(c.pred[v])
	}
	if c.fwdEval > fb {
		t.Fatalf("fwdEval=%d exceeds bound %d (op=%s)", c.fwdEval, fb, o)
	}
	if c.bwdEval > bb {
		t.Fatalf("bwdEval=%d exceeds bound %d (op=%s)", c.bwdEval, bb, o)
	}
	return fb, bb
}

// checkAgainstNaive 比较增量实现与朴素实现的完整状态。
func checkAgainstNaive(t *testing.T, c *CPM, n *naive) {
	t.Helper()
	if !reflect.DeepEqual(c.es, n.es) {
		t.Fatalf("ES mismatch:\n got %v\nwant %v", c.es, n.es)
	}
	if !reflect.DeepEqual(c.ef, n.ef) {
		t.Fatalf("EF mismatch:\n got %v\nwant %v", c.ef, n.ef)
	}
	if !reflect.DeepEqual(c.lf, n.lf) {
		t.Fatalf("LF mismatch:\n got %v\nwant %v", c.lf, n.lf)
	}
	for v := 0; v < n.n; v++ {
		if want := n.lf[v] - n.ef[v]; c.tf[v] != want {
			t.Fatalf("TF(%d): got %d, want %d", v, c.tf[v], want)
		}
		if got := c.ffOf(v); got != n.ff(v) {
			t.Fatalf("FF(%d): got %d, want %d", v, got, n.ff(v))
		}
	}
	if c.pf != n.pf {
		t.Fatalf("PF: got %d, want %d", c.pf, n.pf)
	}
	if !reflect.DeepEqual(c.sortedCrit(), n.crit) && !(len(c.sortedCrit()) == 0 && len(n.crit) == 0) {
		t.Fatalf("critical set mismatch:\n got %v\nwant %v", c.sortedCrit(), n.crit)
	}
	if got, want := c.CriticalPath(), n.path(); !reflect.DeepEqual(got, want) &&
		!(len(got) == 0 && len(want) == 0) {
		t.Fatalf("critical path mismatch:\n got %v\nwant %v", got, want)
	}
}

// ffOf 是不加锁的内部 FF（测试用）。
func (c *CPM) ffOf(v int) int64 {
	if len(c.succ[v]) == 0 {
		return c.pf - c.ef[v]
	}
	ff := c.es[c.succ[v][0].to] - c.succ[v][0].lag - c.ef[v]
	for _, e := range c.succ[v][1:] {
		if val := c.es[e.to] - e.lag - c.ef[v]; val < ff {
			ff = val
		}
	}
	return ff
}

// 2000 组随机更新序列：增量实现、重放实现与整网重算的朴素实现三方对照，
// 日志打印输入、输出与判定依据（go test -v 可见）。
func TestRandomDifferential(t *testing.T) {
	const sequences = 2000
	for seq := 0; seq < sequences; seq++ {
		rng := rand.New(rand.NewSource(int64(seq)*7919 + 13))
		maxN := 2 + rng.Intn(8)
		maxE := 4 + rng.Intn(20)
		deadline := int64(rng.Intn(41))
		c1, err := New(maxN, maxE, deadline)
		if err != nil {
			t.Fatal(err)
		}
		c2, _ := New(maxN, maxE, deadline)
		nv := newNaive(maxN, maxE, deadline)
		t.Logf("seq=%d 输入: maxN=%d maxE=%d D=%d", seq, maxN, maxE, deadline)
		ops := 20 + rng.Intn(30)
		for i := 0; i < ops; i++ {
			o := genOp(rng, c1.n)
			rep1, err1 := applyReal(c1, o)
			rep2, err2 := applyReal(c2, o)
			repN, errN := applyNaive(nv, o)
			if err1 != err2 || err1 != errN {
				t.Fatalf("seq=%d op=%d %s 错误不一致: 增量=%v 重放=%v 朴素=%v",
					seq, i, o, err1, err2, errN)
			}
			if !reflect.DeepEqual(rep1, rep2) {
				t.Fatalf("seq=%d op=%d %s 重放报告不一致:\n got %+v\nwant %+v", seq, i, o, rep1, rep2)
			}
			if o.kind != "baseline" && err1 == nil {
				if !reflect.DeepEqual(rep1, repN) {
					t.Fatalf("seq=%d op=%d %s 报告与朴素不一致:\n got %+v\nwant %+v",
						seq, i, o, rep1, repN)
				}
				fb, bb := checkEvalBounds(t, c1, o, rep1)
				t.Logf("seq=%d op=%d 输入=%s 输出=%+v 判定: fwdEval=%d<=%d bwdEval=%d<=%d 状态与朴素一致",
					seq, i, o, rep1, c1.fwdEval, fb, c1.bwdEval, bb)
			} else {
				t.Logf("seq=%d op=%d 输入=%s 输出: err=%v 判定: 与朴素一致", seq, i, o, err1)
			}
			checkAgainstNaive(t, c1, nv)
			// Variance 对照（覆盖任务不存在/无基线/正常三种情形）。
			v := rng.Intn(maxN + 2)
			gotV, gotE := c1.Variance(v)
			wantV, wantE := nv.variance(v)
			if gotE != wantE || (gotE == nil && gotV != wantV) {
				t.Fatalf("seq=%d op=%d Variance(%d): got (%d,%v), want (%d,%v)",
					seq, i, v, gotV, gotE, wantV, wantE)
			}
		}
	}
}

// 在 n=1000 与 n=100000 的长链上对末端任务抬高 snet：
// fwdEval 与 bwdEval 均不随 n 增长（只重算受影响部分，变化停止即停）。
func TestChainEvalBounded(t *testing.T) {
	prevFwd, prevBwd := -1, -1
	for _, n := range []int{1000, 100000} {
		c, err := New(n, n, int64(n))
		if err != nil {
			t.Fatal(err)
		}
		for i := 0; i < n; i++ {
			// 交错构造：每个任务先以 fnlt=j+1 钉住 LF，再统一前向加边，
			// 使建链每步 O(1)（避免链加长时 LF 整体前移的固有 O(n^2)）。
			if _, _, err := c.AddTask(1); err != nil {
				t.Fatal(err)
			}
			if _, err := c.SetConstraint(i, 0, int64(i+1)); err != nil {
				t.Fatal(err)
			}
		}
		for i := 0; i+1 < n; i++ {
			if _, err := c.AddDep(i, i+1, 0); err != nil {
				t.Fatal(err)
			}
		}
		// 链建成后：ES(j)=j，EF(j)=j+1，LF(j)=j+1，TF(j)=0，全部关键。
		checkI64(t, "PF", c.PF(), int64(n))
		rep, err := c.SetConstraint(n-1, int64(n)+100, -1)
		if err != nil {
			t.Fatal(err)
		}
		checkInts(t, "ChangedES", rep.ChangedES, []int{n - 1})
		checkInts(t, "ChangedLF", rep.ChangedLF, nil)
		t.Logf("n=%d 末端 SetConstraint: fwdEval=%d bwdEval=%d", n, c.fwdEval, c.bwdEval)
		if c.fwdEval > 4 || c.bwdEval > 4 {
			t.Fatalf("n=%d: eval counts too large: fwd=%d bwd=%d", n, c.fwdEval, c.bwdEval)
		}
		if prevFwd >= 0 && (c.fwdEval != prevFwd || c.bwdEval != prevBwd) {
			t.Fatalf("eval counts grow with n: (%d,%d) -> (%d,%d)",
				prevFwd, prevBwd, c.fwdEval, c.bwdEval)
		}
		prevFwd, prevBwd = c.fwdEval, c.bwdEval
	}
}

// checkInvariants 校验题目要求的不变量：
// ES 取到 snet 与前驱 EF+lag 的最大值之一、LF 取到 D/fnlt 与后继 LS-lag 的
// 最小值之一、EF-ES 等于工期、关键集合非空当且仅当有任务。
func checkInvariants(t *testing.T, c *CPM) {
	t.Helper()
	c.mu.Lock()
	defer c.mu.Unlock()
	for v := 0; v < c.n; v++ {
		es := c.snet[v]
		for _, e := range c.pred[v] {
			if val := c.ef[e.to] + e.lag; val > es {
				es = val
			}
		}
		if es != c.es[v] {
			t.Fatalf("invariant ES(%d): stored %d, recomputed %d", v, c.es[v], es)
		}
		if c.ef[v]-c.es[v] != c.dur[v] {
			t.Fatalf("invariant EF-ES!=dur for task %d", v)
		}
		lf := c.deadline
		if c.fnlt[v] >= 0 && c.fnlt[v] < lf {
			lf = c.fnlt[v]
		}
		for _, e := range c.succ[v] {
			if val := c.lf[e.to] - c.dur[e.to] - e.lag; val < lf {
				lf = val
			}
		}
		if lf != c.lf[v] {
			t.Fatalf("invariant LF(%d): stored %d, recomputed %d", v, c.lf[v], lf)
		}
		if c.tf[v] != c.lf[v]-c.ef[v] {
			t.Fatalf("invariant TF(%d)", v)
		}
	}
	pf := int64(0)
	for v := 0; v < c.n; v++ {
		if c.ef[v] > pf {
			pf = c.ef[v]
		}
	}
	if c.pf != pf {
		t.Fatalf("invariant PF: stored %d, recomputed %d", c.pf, pf)
	}
	if (c.n > 0) != (len(c.sortedCrit()) > 0) {
		t.Fatalf("invariant: critical set nonempty iff tasks exist (n=%d, crit=%v)",
			c.n, c.sortedCrit())
	}
	if c.n > 0 {
		minTF := c.tf[0]
		for v := 1; v < c.n; v++ {
			if c.tf[v] < minTF {
				minTF = c.tf[v]
			}
		}
		inCrit := map[int]bool{}
		for _, v := range c.sortedCrit() {
			inCrit[v] = true
		}
		for v := 0; v < c.n; v++ {
			if (c.tf[v] == minTF) != inCrit[v] {
				t.Fatalf("invariant: task %d criticality mismatch (tf=%d minTF=%d)",
					v, c.tf[v], minTF)
			}
		}
	}
}

// 并发调用更新与查询：结果等价于某个串行顺序，查询看不到只完成一半的更新。
func TestConcurrent(t *testing.T) {
	c, err := New(300, 5000, 500)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 100; i++ {
		if _, _, err := c.AddTask(int64(i % 7)); err != nil {
			t.Fatal(err)
		}
	}
	var wg sync.WaitGroup
	for g := 0; g < 4; g++ {
		wg.Add(1)
		go func(seed int64) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(seed))
			for i := 0; i < 300; i++ {
				switch rng.Intn(5) {
				case 0:
					c.AddTask(int64(rng.Intn(5)))
				case 1:
					c.AddDep(rng.Intn(300), rng.Intn(300), int64(rng.Intn(7)-3))
				case 2:
					c.RemoveDep(rng.Intn(300), rng.Intn(300))
				case 3:
					c.SetDuration(rng.Intn(300), int64(rng.Intn(6)))
				case 4:
					c.SetConstraint(rng.Intn(300), int64(rng.Intn(20)), int64(rng.Intn(40))-1)
				}
			}
		}(int64(g) + 1)
	}
	for g := 0; g < 4; g++ {
		wg.Add(1)
		go func(seed int) {
			defer wg.Done()
			for i := 0; i < 300; i++ {
				c.PF()
				c.CriticalPath()
				c.CriticalTasks()
				c.ES((seed + i) % 300)
				c.EF((seed + i) % 300)
				c.LS((seed + i) % 300)
				c.LF((seed + i) % 300)
				c.TF((seed + i) % 300)
				c.FF((seed + i) % 300)
				c.Variance((seed + i) % 300)
			}
		}(g)
	}
	wg.Wait()
	checkInvariants(t, c)
}

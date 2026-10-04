package ssn

import (
	"errors"
	"fmt"
	"math/rand"
	"reflect"
	"strings"
	"sync"
	"testing"
)

// 朴素模拟：按规则逐步写成的独立实现，用于与认证器逐调用对照。

type mVersion struct {
	cs, ps, ss, val int64
}

type mTx struct {
	snap   int64
	done   bool
	reads  map[int]*mVersion
	writes map[int]int64
}

// mCommit 记录一个已提交事务，用于依赖图无环验证。
type mCommit struct {
	id     int
	c      int64
	reads  map[int]int64 // 键 -> 读到的版本 cs
	writes map[int]bool
}

type model struct {
	K, H    int
	n       int64
	nextID  int
	chains  [][]*mVersion
	txs     map[int]*mTx
	commits []mCommit
}

func newModel(K, H int) *model {
	m := &model{
		K:      K,
		H:      H,
		chains: make([][]*mVersion, K),
		txs:    make(map[int]*mTx),
	}
	for i := range m.chains {
		m.chains[i] = []*mVersion{{cs: 0, ps: 0, ss: Inf, val: 0}}
	}
	return m
}

func (m *model) begin() int {
	m.nextID++
	id := m.nextID
	m.txs[id] = &mTx{
		snap:   m.n,
		reads:  make(map[int]*mVersion),
		writes: make(map[int]int64),
	}
	return id
}

func (m *model) read(t, k int) (int64, Reason) {
	tx, ok := m.txs[t]
	if !ok {
		return 0, ReasonUnknownTx
	}
	if tx.done {
		return 0, ReasonTxFinished
	}
	if k < 0 || k >= m.K {
		return 0, ReasonKeyOutOfRange
	}
	if v, ok := tx.writes[k]; ok {
		return v, ReasonNone
	}
	if v, ok := tx.reads[k]; ok {
		return v.val, ReasonNone
	}
	chain := m.chains[k]
	if chain[0].cs > tx.snap {
		return 0, ReasonSnapshotTooOld
	}
	var pick *mVersion
	for _, v := range chain {
		if v.cs <= tx.snap {
			pick = v
		}
	}
	tx.reads[k] = pick
	return pick.val, ReasonNone
}

func (m *model) write(t, k int, val int64) Reason {
	tx, ok := m.txs[t]
	if !ok {
		return ReasonUnknownTx
	}
	if tx.done {
		return ReasonTxFinished
	}
	if k < 0 || k >= m.K {
		return ReasonKeyOutOfRange
	}
	tx.writes[k] = val
	return ReasonNone
}

func (m *model) commit(t int) (int64, Reason) {
	tx, ok := m.txs[t]
	if !ok {
		return 0, ReasonUnknownTx
	}
	if tx.done {
		return 0, ReasonTxFinished
	}
	// ①写写冲突
	for k := range tx.writes {
		latest := m.chains[k][len(m.chains[k])-1]
		if latest.cs > tx.snap {
			tx.done = true
			return 0, ReasonWriteWriteConflict
		}
	}
	// ②
	c := m.n + 1
	// ③
	var eta int64
	pi := c
	for _, v := range tx.reads {
		if v.cs > eta {
			eta = v.cs
		}
		if v.ss < pi {
			pi = v.ss
		}
	}
	for k := range tx.writes {
		latest := m.chains[k][len(m.chains[k])-1]
		if latest.cs > eta {
			eta = latest.cs
		}
		if latest.ps > eta {
			eta = latest.ps
		}
	}
	if pi <= eta {
		tx.done = true
		return 0, ReasonExclusionWindow
	}
	// ④
	m.n = c
	for _, v := range tx.reads {
		if v.ps < c {
			v.ps = c
		}
	}
	for k, val := range tx.writes {
		chain := m.chains[k]
		chain[len(chain)-1].ss = pi
		chain = append(chain, &mVersion{cs: c, ps: 0, ss: Inf, val: val})
		if len(chain) > m.H {
			chain = chain[1:]
		}
		m.chains[k] = chain
	}
	tx.done = true
	rec := mCommit{id: t, c: c, reads: make(map[int]int64), writes: make(map[int]bool)}
	for k, v := range tx.reads {
		rec.reads[k] = v.cs
	}
	for k := range tx.writes {
		rec.writes[k] = true
	}
	m.commits = append(m.commits, rec)
	return c, ReasonNone
}

func (m *model) abort(t int) Reason {
	tx, ok := m.txs[t]
	if !ok {
		return ReasonUnknownTx
	}
	if tx.done {
		return ReasonTxFinished
	}
	tx.done = true
	return ReasonNone
}

func (m *model) dump() State {
	s := State{N: m.n, Chains: make([][]VersionInfo, m.K)}
	for i, chain := range m.chains {
		vs := make([]VersionInfo, len(chain))
		for j, v := range chain {
			vs[j] = VersionInfo{CS: v.cs, PS: v.ps, SS: v.ss, Value: v.val}
		}
		s.Chains[i] = vs
	}
	return s
}

func reasonOf(err error) Reason {
	if err == nil {
		return ReasonNone
	}
	var e *Error
	if errors.As(err, &e) {
		return e.Reason
	}
	return -1
}

// op 是一条可重放的调用记录。
type op struct {
	kind string // begin / read / write / commit / abort
	tx   int
	key  int
	val  int64
}

func (o op) String() string {
	switch o.kind {
	case "begin":
		return "Begin()"
	case "read":
		return fmt.Sprintf("Read(%d, %d)", o.tx, o.key)
	case "write":
		return fmt.Sprintf("Write(%d, %d, %d)", o.tx, o.key, o.val)
	case "commit":
		return fmt.Sprintf("Commit(%d)", o.tx)
	default:
		return fmt.Sprintf("Abort(%d)", o.tx)
	}
}

func genOps(rng *rand.Rand, K, nOps int) []op {
	ops := make([]op, 0, nOps)
	begun := 0
	// 分多轮：每轮前段以 begin/read/write 为主让事务存活更久，
	// 后段密集 commit/abort。后一轮的事务读到更高 cs 的版本，
	// 制造读到的版本被覆盖、水位交错的窗口。
	rounds := 2 + rng.Intn(3)
	segLen := (nOps + rounds - 1) / rounds
	for i := 0; i < nOps; i++ {
		// 事务号取 [0, begun+1]：0 与 begun+1 触发"事务号不存在"。
		tx := rng.Intn(begun + 2)
		key := rng.Intn(K+2) - 1 // [-1, K]：越界与界内都可能
		var r int
		if i%segLen < segLen*3/5 {
			r = rng.Intn(14)
		} else {
			r = 14 + rng.Intn(13)
		}
		switch {
		case r < 4: // begin（仅前期）
			ops = append(ops, op{kind: "begin"})
			begun++
		case r < 9: // read
			ops = append(ops, op{kind: "read", tx: tx, key: key})
		case r < 12: // write
			ops = append(ops, op{kind: "write", tx: tx, key: key, val: int64(rng.Intn(100))})
		case r < 14: // commit（前期少量）
			ops = append(ops, op{kind: "commit", tx: tx})
		case r < 21: // commit（后期密集）
			ops = append(ops, op{kind: "commit", tx: tx})
		case r < 24: // read
			ops = append(ops, op{kind: "read", tx: tx, key: key})
		case r < 26: // write
			ops = append(ops, op{kind: "write", tx: tx, key: key, val: int64(rng.Intn(100))})
		default: // abort
			ops = append(ops, op{kind: "abort", tx: tx})
		}
	}
	return ops
}

// genScript 生成一组随机调用序列：大部分为纯随机序列，部分在随机
// 噪声中嵌入写偏斜、写集 ps、只读异常等危险交错模板，以覆盖安全网
// 中止路径。模板内事务号按 begin 顺序确定，可精确构造。
func genScript(rng *rand.Rand) (K, H int, ops []op) {
	K = 1 + rng.Intn(3)
	H = 2 + rng.Intn(3)
	if K < 2 || rng.Intn(10) < 6 {
		return K, H, genOps(rng, K, 30+rng.Intn(50))
	}
	begun := 0
	begin := func() int {
		ops = append(ops, op{kind: "begin"})
		begun++
		return begun
	}
	noise := func() {
		tx := rng.Intn(begun + 2)
		key := rng.Intn(K+2) - 1
		switch rng.Intn(3) {
		case 0:
			ops = append(ops, op{kind: "read", tx: tx, key: key})
		case 1:
			ops = append(ops, op{kind: "write", tx: tx, key: key, val: int64(rng.Intn(100))})
		case 2:
			ops = append(ops, op{kind: "abort", tx: tx})
		}
	}
	k0, k1 := 0, 1
	for k1 == k0 {
		k0, k1 = rng.Intn(K), rng.Intn(K)
	}
	switch rng.Intn(3) {
	case 0: // 写偏斜
		t1 := begin()
		t2 := begin()
		ops = append(ops,
			op{kind: "read", tx: t1, key: k0}, op{kind: "read", tx: t1, key: k1},
			op{kind: "write", tx: t1, key: k0, val: int64(rng.Intn(100))})
		noise()
		ops = append(ops,
			op{kind: "read", tx: t2, key: k0}, op{kind: "read", tx: t2, key: k1},
			op{kind: "write", tx: t2, key: k1, val: int64(rng.Intn(100))})
		noise()
		ops = append(ops, op{kind: "commit", tx: t1})
		noise()
		ops = append(ops, op{kind: "commit", tx: t2})
	case 1: // 写集键的 ps 参与 η
		tv := begin()
		tw := begin()
		ops = append(ops, op{kind: "write", tx: tw, key: k1, val: 1}, op{kind: "commit", tx: tw})
		noise()
		tr := begin()
		ops = append(ops, op{kind: "read", tx: tr, key: k0}, op{kind: "commit", tx: tr})
		noise()
		ops = append(ops,
			op{kind: "read", tx: tv, key: k1},
			op{kind: "write", tx: tv, key: k0, val: 5},
			op{kind: "commit", tx: tv})
	default: // 三事务只读异常
		t1 := begin()
		t2 := begin()
		ops = append(ops,
			op{kind: "read", tx: t1, key: k1},
			op{kind: "write", tx: t1, key: k1, val: 1},
			op{kind: "commit", tx: t1})
		noise()
		t3 := begin()
		ops = append(ops, op{kind: "read", tx: t3, key: k0}, op{kind: "read", tx: t3, key: k1})
		ops = append(ops,
			op{kind: "read", tx: t2, key: k0}, op{kind: "read", tx: t2, key: k1},
			op{kind: "write", tx: t2, key: k0, val: 9})
		noise()
		ops = append(ops, op{kind: "commit", tx: t3})
		noise()
		ops = append(ops, op{kind: "commit", tx: t2})
	}
	return K, H, ops
}

// applyOne 把一条调用同时施加到认证器（两次独立重放）与朴素模型，
// 逐字段对照返回值与拒绝原因，并把输入、输出与判定依据写入日志。
// 返回该调用的原因（Begin 恒为 ReasonNone）。
func applyOne(t *testing.T, c1, c2 *Certifier, m *model, o op, trace *strings.Builder) Reason {
	t.Helper()
	fail := func(format string, args ...interface{}) {
		t.Fatalf("%s\n%s", fmt.Sprintf(format, args...), trace.String())
	}
	reason := ReasonNone
	switch o.kind {
	case "begin":
		id1, id2, idm := c1.Begin(), c2.Begin(), m.begin()
		fmt.Fprintf(trace, "%-22s -> tx=%d\n", o, id1)
		if id1 != idm || id2 != idm {
			fail("Begin 结果不一致: impl=%d,%d model=%d", id1, id2, idm)
		}
	case "read":
		v1, e1 := c1.Read(o.tx, o.key)
		v2, e2 := c2.Read(o.tx, o.key)
		vm, rm := m.read(o.tx, o.key)
		reason = rm
		fmt.Fprintf(trace, "%-22s -> val=%d reason=%s\n", o, vm, rm)
		if reasonOf(e1) != rm || reasonOf(e2) != rm {
			fail("Read 原因不一致: impl=%s,%s model=%s", reasonOf(e1), reasonOf(e2), rm)
		}
		if rm == ReasonNone && (v1 != vm || v2 != vm) {
			fail("Read 值不一致: impl=%d,%d model=%d", v1, v2, vm)
		}
	case "write":
		e1 := c1.Write(o.tx, o.key, o.val)
		e2 := c2.Write(o.tx, o.key, o.val)
		rm := m.write(o.tx, o.key, o.val)
		reason = rm
		fmt.Fprintf(trace, "%-22s -> reason=%s\n", o, rm)
		if reasonOf(e1) != rm || reasonOf(e2) != rm {
			fail("Write 原因不一致: impl=%s,%s model=%s", reasonOf(e1), reasonOf(e2), rm)
		}
	case "commit":
		n1, e1 := c1.Commit(o.tx)
		n2, e2 := c2.Commit(o.tx)
		nm, rm := m.commit(o.tx)
		reason = rm
		fmt.Fprintf(trace, "%-22s -> c=%d reason=%s\n", o, nm, rm)
		if reasonOf(e1) != rm || reasonOf(e2) != rm {
			fail("Commit 原因不一致: impl=%s,%s model=%s", reasonOf(e1), reasonOf(e2), rm)
		}
		if rm == ReasonNone && (n1 != nm || n2 != nm) {
			fail("Commit 提交号不一致: impl=%d,%d model=%d", n1, n2, nm)
		}
		if rm == ReasonNone || rm == ReasonWriteWriteConflict || rm == ReasonExclusionWindow {
			tx := m.txs[o.tx]
			if bound := len(tx.reads) + len(tx.writes); c1.validateAccesses > bound {
				fail("Commit 认证访问版本数 %d 超过读集+写集 %d", c1.validateAccesses, bound)
			}
		}
	case "abort":
		e1 := c1.Abort(o.tx)
		e2 := c2.Abort(o.tx)
		rm := m.abort(o.tx)
		reason = rm
		fmt.Fprintf(trace, "%-22s -> reason=%s\n", o, rm)
		if reasonOf(e1) != rm || reasonOf(e2) != rm {
			fail("Abort 原因不一致: impl=%s,%s model=%s", reasonOf(e1), reasonOf(e2), rm)
		}
	}
	s1, s2, sm := c1.Dump(), c2.Dump(), m.dump()
	if !reflect.DeepEqual(s1, sm) || !reflect.DeepEqual(s2, sm) {
		fail("状态不一致:\n impl1=%+v\n impl2=%+v\n model=%+v", s1, s2, sm)
	}
	checkInvariants(t, s1)
	return reason
}

// TestRandomAgainstModel 用 2000 组随机调用序列把认证器（独立重放两次）
// 与朴素模拟逐步对照，并对已提交事务用暴力法验证依赖图无环。
func TestRandomAgainstModel(t *testing.T) {
	const scripts = 2000
	var totalCommits, totalWW, totalSSN int
	for i := 0; i < scripts; i++ {
		seed := int64(i) + 1
		rng := rand.New(rand.NewSource(seed))
		K, H, ops := genScript(rng)

		var trace strings.Builder
		fmt.Fprintf(&trace, "script %d: seed=%d K=%d H=%d ops=%d\n", i, seed, K, H, len(ops))

		c1, err := New(K, H)
		if err != nil {
			t.Fatalf("New(%d, %d): %v", K, H, err)
		}
		c2, _ := New(K, H) // 相同调用序列的第二次重放
		m := newModel(K, H)
		for _, o := range ops {
			r := applyOne(t, c1, c2, m, o, &trace)
			if o.kind == "commit" {
				switch r {
				case ReasonWriteWriteConflict:
					totalWW++
				case ReasonExclusionWindow:
					totalSSN++
				}
			}
		}
		totalCommits += len(m.commits)
		checkGraphAcyclic(t, m.commits, &trace)
		if i == 0 {
			t.Logf("样例脚本轨迹（输入、输出与判定依据）:\n%s", trace.String())
		} else {
			t.Logf("script %d 通过: K=%d H=%d ops=%d commits=%d", i, K, H, len(ops), len(m.commits))
		}
	}
	t.Logf("随机对照完成: %d 组序列，共 %d 个已提交事务；写写冲突中止 %d，安全网中止 %d",
		scripts, totalCommits, totalWW, totalSSN)
}

// checkGraphAcyclic 对已提交事务构造写读、写写、读写三类依赖边，
// 用 Kahn 算法判定无环；节点数不超过 8 时再暴力枚举全部排列复核。
func checkGraphAcyclic(t *testing.T, recs []mCommit, trace *strings.Builder) {
	t.Helper()
	if len(recs) == 0 {
		return
	}
	edges := make(map[int]map[int]bool)
	addEdge := func(a, b int) {
		if edges[a] == nil {
			edges[a] = make(map[int]bool)
		}
		edges[a][b] = true
	}
	for _, a := range recs {
		for _, b := range recs {
			if a.id == b.id {
				continue
			}
			for k, cs := range b.reads {
				if a.writes[k] && a.c == cs { // 写读边：b 读到 a 写的版本
					addEdge(a.id, b.id)
				}
			}
			for k, cs := range a.reads {
				if b.writes[k] && b.c > cs { // 读写反依赖：b 覆盖了 a 读到的版本
					addEdge(a.id, b.id)
				}
			}
			for k := range a.writes {
				if b.writes[k] && a.c < b.c { // 写写边：按提交号排序
					addEdge(a.id, b.id)
				}
			}
		}
	}

	// Kahn 拓扑排序。
	indeg := make(map[int]int)
	for _, r := range recs {
		indeg[r.id] = 0
	}
	for _, tos := range edges {
		for b := range tos {
			indeg[b]++
		}
	}
	var queue []int
	for _, r := range recs {
		if indeg[r.id] == 0 {
			queue = append(queue, r.id)
		}
	}
	processed := 0
	for len(queue) > 0 {
		x := queue[0]
		queue = queue[1:]
		processed++
		for b := range edges[x] {
			indeg[b]--
			if indeg[b] == 0 {
				queue = append(queue, b)
			}
		}
	}
	if processed != len(recs) {
		t.Fatalf("已提交事务依赖图有环（Kahn 处理 %d/%d）:\n%s", processed, len(recs), trace.String())
	}

	// 小图暴力枚举全部排列，验证存在满足所有依赖边的串行顺序。
	if len(recs) <= 8 {
		ids := make([]int, len(recs))
		for i, r := range recs {
			ids[i] = r.id
		}
		valid := 0
		var perm func(int)
		perm = func(i int) {
			if i == len(ids) {
				pos := make(map[int]int, len(ids))
				for j, id := range ids {
					pos[id] = j
				}
				ok := true
				for a, tos := range edges {
					for b := range tos {
						if pos[a] >= pos[b] {
							ok = false
						}
					}
				}
				if ok {
					valid++
				}
				return
			}
			for j := i; j < len(ids); j++ {
				ids[i], ids[j] = ids[j], ids[i]
				perm(i + 1)
				ids[i], ids[j] = ids[j], ids[i]
			}
		}
		perm(0)
		if valid == 0 {
			t.Fatalf("暴力枚举 %d 个已提交事务的全部排列均不满足依赖边:\n%s", len(recs), trace.String())
		}
		fmt.Fprintf(trace, "依赖图无环: %d 个已提交事务，%d 个合法串行顺序（暴力枚举）\n", len(recs), valid)
	} else {
		fmt.Fprintf(trace, "依赖图无环: %d 个已提交事务（Kahn）\n", len(recs))
	}
}

// TestConcurrentCalls 并发调用所有方法，验证竞态安全与不变量保持。
func TestConcurrentCalls(t *testing.T) {
	c, err := New(8, 4)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(seed int64) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(seed))
			for i := 0; i < 1000; i++ {
				tx := rng.Intn(24)      // 可能不存在
				key := rng.Intn(10) - 1 // 可能越界
				switch rng.Intn(5) {
				case 0:
					c.Begin()
				case 1:
					c.Read(tx, key)
				case 2:
					c.Write(tx, key, int64(rng.Intn(100)))
				case 3:
					c.Commit(tx)
				case 4:
					c.Abort(tx)
				}
			}
		}(int64(g + 1))
	}
	wg.Wait()
	checkInvariants(t, c.Dump())
	t.Logf("并发调用完成，最终状态不变量保持: n=%d", c.Dump().N)
}

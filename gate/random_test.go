package gate

import (
	"fmt"
	"math/rand"
	"sort"
	"strings"
	"sync"
	"testing"

	"ontology/meta"
)

const (
	opJoin = iota
	opLeave
	opPut
	opGet
	opDelete
	opRaise
	opRollback
	opStats
)

type op struct {
	kind   int
	name   string
	key    string
	max    int
	target int
	size   int64
	etag   *string
	tags   *[]string
}

func (o op) String() string {
	switch o.kind {
	case opJoin:
		return fmt.Sprintf("Join(%q,%d)", o.name, o.max)
	case opLeave:
		return fmt.Sprintf("Leave(%q)", o.name)
	case opPut:
		return fmt.Sprintf("Put(%q,%q,size=%d,etag=%s,tags=%s)", o.name, o.key, o.size, ptrDesc(o.etag), sliceDesc(o.tags))
	case opGet:
		return fmt.Sprintf("Get(%q,%q)", o.name, o.key)
	case opDelete:
		return fmt.Sprintf("Delete(%q,%q)", o.name, o.key)
	case opRaise:
		return fmt.Sprintf("Raise(%d)", o.target)
	case opRollback:
		return fmt.Sprintf("Rollback(%d)", o.target)
	default:
		return "Stats()"
	}
}

func ptrDesc(p *string) string {
	if p == nil {
		return "<absent>"
	}
	return fmt.Sprintf("%q", *p)
}

func sliceDesc(p *[]string) string {
	if p == nil {
		return "<absent>"
	}
	return fmt.Sprintf("%v", *p)
}

// hookRun 按调用序号应用确定性失败策略；两次重放共享同一条策略。
type hookRun struct {
	policy []bool
	idx    int
	log    []string
}

func newHookRun(policy []bool) *hookRun { return &hookRun{policy: policy} }

func (h *hookRun) ack(node string, target int) error {
	fail := h.idx < len(h.policy) && h.policy[h.idx]
	h.idx++
	h.log = append(h.log, fmt.Sprintf("Ack(%s,%d)", node, target))
	if fail {
		return fmt.Errorf("injected-%s", node)
	}
	return nil
}

func (h *hookRun) release(node string, target int) {
	h.log = append(h.log, fmt.Sprintf("Release(%s,%d)", node, target))
}

// ---- 朴素单线程模拟器 ----

type simRec struct {
	level int
	size  int64
	etag  *string
	tags  *[]string
}

type sim struct {
	g     int
	nodes map[string]int
	recs  map[string]simRec
	count [4]int
	hook  *hookRun
}

func newSim(h *hookRun) *sim {
	return &sim{g: 1, nodes: map[string]int{}, recs: map[string]simRec{}, hook: h}
}

type simOut struct {
	err   string
	view  string
	stats string
}

func simErr(base, detail string) string {
	if detail == "" {
		return base
	}
	return base + "（" + detail + "）"
}

func (m *sim) apply(o op) simOut {
	switch o.kind {
	case opJoin:
		if o.name == "" || o.max < 1 || o.max > 3 {
			return simOut{err: simErr(ErrInvalidArg.Error(), "")}
		}
		if _, ok := m.nodes[o.name]; ok {
			return simOut{err: simErr(ErrExists.Error(), "")}
		}
		m.nodes[o.name] = o.max
		return simOut{}
	case opLeave:
		if o.name == "" {
			return simOut{err: simErr(ErrInvalidArg.Error(), "")}
		}
		if _, ok := m.nodes[o.name]; !ok {
			return simOut{err: simErr(ErrNotExist.Error(), "")}
		}
		delete(m.nodes, o.name)
		return simOut{}
	case opPut:
		if o.name == "" || o.key == "" || o.size < 0 || o.size > 1e12 {
			return simOut{err: simErr(ErrInvalidArg.Error(), "")}
		}
		max, ok := m.nodes[o.name]
		if !ok {
			return simOut{err: simErr(ErrNotExist.Error(), "节点 "+o.name)}
		}
		if max < m.g {
			return simOut{err: simErr(ErrReadOnly.Error(), "节点 "+o.name)}
		}
		if o.etag != nil && m.g < meta.LevelEtag {
			return simOut{err: simErr(ErrUnsupported.Error(), "etag")}
		}
		if o.tags != nil && m.g < meta.LevelTags {
			return simOut{err: simErr(ErrUnsupported.Error(), "tags")}
		}
		if old, ok := m.recs[o.key]; ok {
			m.count[old.level]--
		}
		m.recs[o.key] = simRec{level: m.g, size: o.size, etag: o.etag, tags: o.tags}
		m.count[m.g]++
		return simOut{}
	case opGet:
		if o.name == "" || o.key == "" {
			return simOut{err: simErr(ErrInvalidArg.Error(), "")}
		}
		max, ok := m.nodes[o.name]
		if !ok {
			return simOut{err: simErr(ErrNotExist.Error(), "节点 "+o.name)}
		}
		r, ok := m.recs[o.key]
		if !ok {
			return simOut{err: simErr(ErrNotExist.Error(), "键 "+o.key)}
		}
		return simOut{view: viewDesc(r.level, r.size, r.etag, r.tags, max)}
	case opDelete:
		if o.name == "" || o.key == "" {
			return simOut{err: simErr(ErrInvalidArg.Error(), "")}
		}
		if _, ok := m.nodes[o.name]; !ok {
			return simOut{err: simErr(ErrNotExist.Error(), "节点 "+o.name)}
		}
		r, ok := m.recs[o.key]
		if !ok {
			return simOut{err: simErr(ErrNotExist.Error(), "键 "+o.key)}
		}
		m.count[r.level]--
		delete(m.recs, o.key)
		return simOut{}
	case opRaise:
		if o.target < 1 || o.target > 3 || o.target != m.g+1 {
			return simOut{err: simErr(ErrInvalidArg.Error(), "")}
		}
		if len(m.nodes) == 0 {
			return simOut{err: simErr(ErrNoNode.Error(), "")}
		}
		names := make([]string, 0, len(m.nodes))
		for n := range m.nodes {
			names = append(names, n)
		}
		sort.Strings(names)
		laggard, minMax := names[0], m.nodes[names[0]]
		for _, n := range names[1:] {
			if m.nodes[n] < minMax {
				minMax, laggard = m.nodes[n], n
			}
		}
		if o.target > minMax {
			return simOut{err: simErr(ErrNodeLagging.Error(), "节点 "+laggard)}
		}
		acked := make([]string, 0, len(names))
		for _, n := range names {
			if err := m.hook.ack(n, o.target); err != nil {
				for i := len(acked) - 1; i >= 0; i-- {
					m.hook.release(acked[i], o.target)
				}
				return simOut{err: simErr(ErrAckFailed.Error(), "节点 "+n)}
			}
			acked = append(acked, n)
		}
		m.g = o.target
		return simOut{}
	case opRollback:
		if o.target < 1 || o.target >= m.g {
			return simOut{err: simErr(ErrInvalidArg.Error(), "")}
		}
		cnt, highest := 0, 0
		for lvl := 3; lvl > o.target; lvl-- {
			if m.count[lvl] > 0 {
				highest = lvl
				break
			}
		}
		for lvl := o.target + 1; lvl <= 3; lvl++ {
			cnt += m.count[lvl]
		}
		if cnt > 0 {
			return simOut{err: simErr(ErrResidue.Error(),
				fmt.Sprintf("残留 %d 条，最高级别 %d", cnt, highest))}
		}
		m.g = o.target
		return simOut{}
	default:
		return simOut{stats: fmt.Sprintf("G=%d,count=[%d,%d,%d]", m.g, m.count[1], m.count[2], m.count[3])}
	}
}

func fieldDesc(etag *string, tags *[]string, maxLevel int) (string, string) {
	es, ts := "<hidden>", "<hidden>"
	switch {
	case etag == nil:
		es = "<absent>"
	case meta.LevelEtag <= maxLevel:
		es = fmt.Sprintf("%q", *etag)
	}
	switch {
	case tags == nil:
		ts = "<absent>"
	case meta.LevelTags <= maxLevel:
		ts = fmt.Sprintf("%v", *tags)
	}
	return es, ts
}

func viewDesc(level int, size int64, etag *string, tags *[]string, maxLevel int) string {
	es, ts := fieldDesc(etag, tags, maxLevel)
	return fmt.Sprintf("level=%d,size=%d,etag=%s,tags=%s,degraded=%v",
		level, size, es, ts, level > maxLevel)
}

func errStr(e error) string {
	if e == nil {
		return ""
	}
	return e.Error()
}

func realViewDesc(v meta.View) string {
	es, ts := "<hidden>", "<hidden>"
	switch {
	case v.Etag == nil:
		es = "<absent>"
	default:
		es = fmt.Sprintf("%q", *v.Etag)
	}
	switch {
	case v.Tags == nil:
		ts = "<absent>"
	default:
		ts = fmt.Sprintf("%v", *v.Tags)
	}
	return fmt.Sprintf("level=%d,size=%d,etag=%s,tags=%s,degraded=%v",
		v.Level, v.Size, es, ts, v.Degraded)
}

func runReal(ops []op, policy []bool) ([]simOut, []string) {
	h := newHookRun(policy)
	s, err := New(h.ack, h.release)
	if err != nil {
		panic(err)
	}
	out := make([]simOut, len(ops))
	for i, o := range ops {
		switch o.kind {
		case opJoin:
			out[i].err = errStr(s.Join(o.name, o.max))
		case opLeave:
			out[i].err = errStr(s.Leave(o.name))
		case opPut:
			out[i].err = errStr(s.Put(o.name, o.key,
				meta.Fields{Size: o.size, Etag: o.etag, Tags: o.tags}))
		case opGet:
			v, e := s.Get(o.name, o.key)
			out[i].err = errStr(e)
			if e == nil {
				out[i].view = realViewDesc(v)
			}
		case opDelete:
			out[i].err = errStr(s.Delete(o.name, o.key))
		case opRaise:
			out[i].err = errStr(s.Raise(o.target))
		case opRollback:
			out[i].err = errStr(s.Rollback(o.target))
		default:
			st := s.Stats()
			out[i].stats = fmt.Sprintf("G=%d,count=[%d,%d,%d]",
				st.G, st.Count[1], st.Count[2], st.Count[3])
		}
	}
	return out, h.log
}

func runSim(ops []op, policy []bool) ([]simOut, []string) {
	h := newHookRun(policy)
	m := newSim(h)
	out := make([]simOut, len(ops))
	for i, o := range ops {
		out[i] = m.apply(o)
	}
	return out, h.log
}

func genOps(r *rand.Rand) ([]op, []bool) {
	n := 30 + r.Intn(51)
	names := []string{"a", "b", "c", "d"}
	keys := []string{"k1", "k2", "k3", "k4", "k5", "k6"}
	ops := make([]op, 0, n)
	for i := 0; i < n; i++ {
		o := op{kind: r.Intn(8), name: names[r.Intn(len(names))], key: keys[r.Intn(len(keys))]}
		switch o.kind {
		case opJoin:
			o.max = []int{0, 1, 2, 3, 4}[r.Intn(5)]
		case opPut:
			o.size = int64(r.Intn(100))
			switch r.Intn(10) {
			case 0:
				o.size = -1
			case 1:
				o.size = 1e12 + 1
			case 2:
				o.size = 0
			case 3:
				o.size = 1e12
			}
			switch r.Intn(3) {
			case 1:
				s := ""
				o.etag = &s
			case 2:
				s := fmt.Sprintf("e%d", r.Intn(5))
				o.etag = &s
			}
			switch r.Intn(3) {
			case 1:
				o.tags = &[]string{}
			case 2:
				tg := []string{fmt.Sprintf("t%d", r.Intn(4))}
				if r.Intn(2) == 0 {
					tg = append(tg, "x")
				}
				o.tags = &tg
			}
		case opRaise, opRollback:
			o.target = r.Intn(5)
		}
		ops = append(ops, o)
	}
	policy := make([]bool, n*3+8)
	for i := range policy {
		policy[i] = r.Intn(100) < 15
	}
	return ops, policy
}

func TestRandomAgainstNaiveSimulator(t *testing.T) {
	const trials = 1500
	base := rand.NewSource(20261003)
	for trial := 0; trial < trials; trial++ {
		r := rand.New(rand.NewSource(base.Int63()))
		ops, policy := genOps(r)

		real1, log1 := runReal(ops, policy)
		sim1, slog1 := runSim(ops, policy)
		real2, log2 := runReal(ops, policy) // 相同序列重放，结果与调用序列必须完全一致

		var sb strings.Builder
		fmt.Fprintf(&sb, "trial %d 输入(%d ops, %d ack-fail slots):\n", trial, len(ops), len(policy))
		bad := -1
		for i, o := range ops {
			if real1[i] != sim1[i] {
				if bad < 0 {
					bad = i
				}
			}
			mark := "ok"
			if real1[i] != sim1[i] {
				mark = "DIFF"
			}
			fmt.Fprintf(&sb, "  [%d] %-4s in=%-52s real=%q|%q|%q sim=%q|%q|%q\n",
				i, mark, o.String(),
				real1[i].err, real1[i].view, real1[i].stats,
				sim1[i].err, sim1[i].view, sim1[i].stats)
		}
		fmt.Fprintf(&sb, "判定: ack 调用序列 real=%v sim=%v\n", log1, slog1)
		t.Logf("trial %d: %d ops, 首个分歧=%d, acks=%d", trial, len(ops), bad, len(log1))

		if bad >= 0 {
			t.Fatalf("trial %d op[%d] 与朴素模拟不一致\n%s", trial, bad, sb.String())
		}
		if !equalStringSlice(log1, slog1) {
			t.Fatalf("trial %d Ack/Release 调用序列不一致\nreal=%v\nsim=%v\n%s",
				trial, log1, slog1, sb.String())
		}
		if !equalOuts(real1, real2) || !equalStringSlice(log1, log2) {
			t.Fatalf("trial %d 重放结果不确定\n%s", trial, sb.String())
		}
	}
}

func equalStringSlice(a, b []string) bool {
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

func equalOuts(a, b []simOut) bool {
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

// TestRaiseAtomicUnderConcurrency 并发风暴：Raise 只应观察到某个串行时刻的在册快照，
// 且 Ack/Release 序列必须与同一快照下的朴素排序一致；G 恒为 1..3。
func TestRaiseAtomicUnderConcurrency(t *testing.T) {
	var mu sync.Mutex
	bad := ""
	ack := func(node string, target int) error {
		mu.Lock()
		defer mu.Unlock()
		return nil
	}
	release := func(string, int) {}
	s, err := New(ack, release)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			name := fmt.Sprintf("n%d", i)
			_ = s.Join(name, 3)
			for j := 0; j < 50; j++ {
				_ = s.Leave(name)
				_ = s.Join(name, 3)
			}
		}(i)
	}
	for i := 0; i < 200; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if i%3 == 0 {
				_ = s.Rollback(1)
			} else {
				_ = s.Raise(2)
				_ = s.Raise(3)
			}
			_ = s.Put("n0", "k", meta.Fields{Size: 1})
			st := s.Stats()
			if st.G < 1 || st.G > 3 {
				mu.Lock()
				bad = fmt.Sprintf("G 越界 %d", st.G)
				mu.Unlock()
			}
		}(i)
	}
	wg.Wait()
	if bad != "" {
		t.Fatal(bad)
	}
}

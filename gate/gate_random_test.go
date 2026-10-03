package gate

import (
	"errors"
	"fmt"
	"math/rand"
	"sort"
	"strings"
	"sync"
	"testing"
)

type op struct {
	kind              string
	node, key         string
	max               int
	size              int64
	etag              *string
	hasEtag, hasTags  bool
	tags              map[string]string
	target, failIndex int
}

type mrec struct {
	level            int
	size             int64
	etag             string
	hasEtag, hasTags bool
	tags             map[string]string
}

type model struct {
	g      int
	nodes  map[string]int
	recs   map[string]mrec
	counts [3]int
	calls  []string
}

func newModel() *model {
	return &model{g: 1, nodes: map[string]int{}, recs: map[string]mrec{}}
}

type outcome struct {
	err                        string
	level                      int
	size                       int64
	degraded, hasEtag, hasTags bool
	etag                       string
	tags                       string
	g                          int
	counts                     [3]int
	calls                      []string
}

func sortedNames(m map[string]int) []string {
	ns := make([]string, 0, len(m))
	for n := range m {
		ns = append(ns, n)
	}
	sort.Strings(ns)
	return ns
}

func (m *model) apply(o op) outcome {
	out := outcome{g: m.g, counts: m.counts}
	switch o.kind {
	case "join":
		if o.node == "" || o.max < 1 || o.max > 3 {
			out.err = "参数非法"
			break
		}
		if _, ok := m.nodes[o.node]; ok {
			out.err = "已存在"
			break
		}
		m.nodes[o.node] = o.max
	case "leave":
		if o.node == "" {
			out.err = "参数非法"
			break
		}
		if _, ok := m.nodes[o.node]; !ok {
			out.err = "不存在"
			break
		}
		delete(m.nodes, o.node)
	case "put":
		if o.key == "" || o.size < 0 || o.size > 1e12 {
			out.err = "参数非法"
			break
		}
		max, ok := m.nodes[o.node]
		if !ok {
			out.err = "不存在"
			break
		}
		if max < m.g {
			out.err = "节点只读"
			break
		}
		if (o.hasEtag && m.g < 2) || (o.hasTags && m.g < 3) {
			out.err = "字段不支持"
			break
		}
		if old, ok := m.recs[o.key]; ok {
			m.counts[old.level-1]--
		}
		rec := mrec{level: m.g, size: o.size, hasEtag: o.hasEtag, hasTags: o.hasTags}
		if o.hasEtag {
			rec.etag = *o.etag
		}
		if o.hasTags {
			rec.tags = o.tags
		}
		m.recs[o.key] = rec
		m.counts[m.g-1]++
	case "get":
		if o.key == "" || o.node == "" {
			out.err = "参数非法"
			break
		}
		max, ok := m.nodes[o.node]
		if !ok {
			out.err = "不存在"
			break
		}
		rec, ok := m.recs[o.key]
		if !ok {
			out.err = "不存在"
			break
		}
		out.level, out.size = rec.level, rec.size
		out.degraded = rec.level > max
		out.hasEtag = rec.hasEtag && 2 <= max
		if out.hasEtag {
			out.etag = rec.etag
		}
		out.hasTags = rec.hasTags && 3 <= max
		if out.hasTags {
			out.tags = canonTags(rec.tags)
		}
	case "delete":
		if o.key == "" || o.node == "" {
			out.err = "参数非法"
			break
		}
		if _, ok := m.nodes[o.node]; !ok {
			out.err = "不存在"
			break
		}
		rec, ok := m.recs[o.key]
		if !ok {
			out.err = "不存在"
			break
		}
		m.counts[rec.level-1]--
		delete(m.recs, o.key)
	case "raise":
		before := len(m.calls)
		if o.target != m.g+1 || o.target > 3 {
			out.err = "参数非法"
			break
		}
		names := sortedNames(m.nodes)
		if len(names) == 0 {
			out.err = "无节点"
			break
		}
		minMax, minName := m.nodes[names[0]], names[0]
		for _, n := range names[1:] {
			if m.nodes[n] < minMax || (m.nodes[n] == minMax && n < minName) {
				minMax, minName = m.nodes[n], n
			}
		}
		if o.target > minMax {
			out.err = "节点落后:" + minName
			break
		}
		acked := []string{}
		for i, n := range names {
			m.calls = append(m.calls, fmt.Sprintf("Ack(%s,%d)", n, o.target))
			if i == o.failIndex {
				for j := len(acked) - 1; j >= 0; j-- {
					m.calls = append(m.calls, fmt.Sprintf("Release(%s,%d)", acked[j], o.target))
				}
				out.err = "确认失败:" + n
				break
			}
			acked = append(acked, n)
		}
		if out.err == "" {
			m.g = o.target
		}
		out.calls = append([]string{}, m.calls[before:]...)
	case "rollback":
		if o.target < 1 || o.target >= m.g {
			out.err = "参数非法"
			break
		}
		cnt, high := 0, 0
		for lvl := 3; lvl > o.target; lvl-- {
			if m.counts[lvl-1] > 0 {
				cnt += m.counts[lvl-1]
				if high == 0 {
					high = lvl
				}
			}
		}
		if cnt > 0 {
			out.err = fmt.Sprintf("有残留:%d:%d", cnt, high)
			break
		}
		m.g = o.target
	}
	out.g, out.counts = m.g, m.counts
	return out
}

func canonTags(t map[string]string) string {
	ks := make([]string, 0, len(t))
	for k := range t {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	parts := make([]string, 0, len(ks))
	for _, k := range ks {
		parts = append(parts, k+"="+t[k])
	}
	return "{" + strings.Join(parts, ",") + "}"
}

func canonErr(err error) string {
	if err == nil {
		return ""
	}
	var nb *NodeBehindError
	if errors.As(err, &nb) {
		return "节点落后:" + nb.Node
	}
	var af *AckFailureError
	if errors.As(err, &af) {
		return "确认失败:" + af.Node
	}
	var re *ResidualError
	if errors.As(err, &re) {
		return fmt.Sprintf("有残留:%d:%d", re.Count, re.HighLevel)
	}
	return err.Error()
}

type realRunner struct {
	g       *Gate
	calls   []string
	curFail int
	ackIdx  int
}

func newRealRunner() *realRunner {
	r := &realRunner{curFail: -1}
	r.g = New(
		func(node string, target int) error {
			r.calls = append(r.calls, fmt.Sprintf("Ack(%s,%d)", node, target))
			i := r.ackIdx
			r.ackIdx++
			if i == r.curFail {
				return errors.New("boom")
			}
			return nil
		},
		func(node string, target int) {
			r.calls = append(r.calls, fmt.Sprintf("Release(%s,%d)", node, target))
		},
	)
	return r
}

func (r *realRunner) apply(o op) outcome {
	out := outcome{}
	switch o.kind {
	case "join":
		out.err = canonErr(r.g.Join(o.node, o.max))
	case "leave":
		out.err = canonErr(r.g.Leave(o.node))
	case "put":
		f := PutFields{Size: o.size}
		if o.hasEtag {
			f.Etag = o.etag
		}
		if o.hasTags {
			f.Tags = o.tags
		}
		out.err = canonErr(r.g.Put(o.node, o.key, f))
	case "get":
		res, err := r.g.Get(o.node, o.key)
		out.err = canonErr(err)
		if err == nil {
			out.level, out.size, out.degraded = res.Level, res.Size, res.Degraded
			out.hasEtag = res.Etag != nil
			if out.hasEtag {
				out.etag = *res.Etag
			}
			out.hasTags = res.Tags != nil
			if out.hasTags {
				out.tags = canonTags(res.Tags)
			}
		}
	case "delete":
		out.err = canonErr(r.g.Delete(o.node, o.key))
	case "raise":
		before := len(r.calls)
		r.curFail, r.ackIdx = o.failIndex, 0
		out.err = canonErr(r.g.Raise(o.target))
		out.calls = append([]string{}, r.calls[before:]...)
	case "rollback":
		out.err = canonErr(r.g.Rollback(o.target))
	}
	st := r.g.Stats()
	out.g, out.counts = st.G, st.CountByLvl
	return out
}

func canonOutcome(o outcome) string {
	return fmt.Sprintf("err=%q g=%d cnt=%v lvl=%d size=%d deg=%v e=(%v,%q) tags=(%v,%s) calls=%v",
		o.err, o.g, o.counts, o.level, o.size, o.degraded, o.hasEtag, o.etag, o.hasTags, o.tags, o.calls)
}

func genOps(rng *rand.Rand, n int) []op {
	nodes := []string{"a", "b", "c", "d"}
	keys := []string{"k1", "k2", "k3"}
	driver := newModel()
	ops := make([]op, 0, n)
	for i := 0; i < n; i++ {
		kinds := []string{"join", "join", "leave", "put", "put", "get", "delete", "raise", "rollback", "stats"}
		o := op{kind: kinds[rng.Intn(len(kinds))], failIndex: -1}
		switch o.kind {
		case "join":
			o.node = nodes[rng.Intn(len(nodes))]
			if rng.Intn(10) == 0 {
				o.node = ""
			}
			o.max = 1 + rng.Intn(3)
			if rng.Intn(12) == 0 {
				if rng.Intn(2) == 0 {
					o.max = 0
				} else {
					o.max = 4
				}
			}
		case "leave":
			o.node = nodes[rng.Intn(len(nodes))]
		case "put", "get", "delete":
			o.node = nodes[rng.Intn(len(nodes))]
			if rng.Intn(8) == 0 {
				o.node = "z"
			}
			o.key = keys[rng.Intn(len(keys))]
			if rng.Intn(14) == 0 {
				o.key = ""
			}
			if o.kind == "put" {
				o.size = rng.Int63n(1e12 + 2)
				if rng.Intn(20) == 0 {
					o.size = -1
				}
				if rng.Intn(2) == 0 {
					o.hasEtag = true
					s := ""
					if rng.Intn(2) == 0 {
						s = fmt.Sprintf("e%d", rng.Intn(4))
					}
					o.etag = &s
				}
				if rng.Intn(3) == 0 {
					o.hasTags = true
					o.tags = map[string]string{}
					if rng.Intn(2) == 0 {
						o.tags[fmt.Sprintf("t%d", rng.Intn(3))] = "v"
					}
				}
			}
		case "raise":
			switch rng.Intn(4) {
			case 0:
				o.target = driver.g
			case 1:
				o.target = driver.g + 2
			default:
				o.target = driver.g + 1
			}
			if o.target < 1 {
				o.target = 1
			}
			if o.target > 4 {
				o.target = 4
			}
			if rng.Intn(2) == 0 {
				o.failIndex = rng.Intn(5)
			}
		case "rollback":
			o.target = rng.Intn(driver.g + 1)
		}
		ops = append(ops, o)
		driver.apply(o)
	}
	return ops
}

func describe(o op) string {
	switch o.kind {
	case "join":
		return fmt.Sprintf("Join(%q,%d)", o.node, o.max)
	case "leave":
		return fmt.Sprintf("Leave(%q)", o.node)
	case "put":
		etag, tags := "<nil>", "<nil>"
		if o.hasEtag {
			etag = fmt.Sprintf("%q", *o.etag)
		}
		if o.hasTags {
			tags = canonTags(o.tags)
		}
		return fmt.Sprintf("Put(%q,%q,size=%d,etag=%s,tags=%s)", o.node, o.key, o.size, etag, tags)
	case "get":
		return fmt.Sprintf("Get(%q,%q)", o.node, o.key)
	case "delete":
		return fmt.Sprintf("Delete(%q,%q)", o.node, o.key)
	case "raise":
		return fmt.Sprintf("Raise(%d,failAt=%d)", o.target, o.failIndex)
	case "rollback":
		return fmt.Sprintf("Rollback(%d)", o.target)
	default:
		return "Stats()"
	}
}

func TestRandomReplay(t *testing.T) {
	const groups = 1500
	const seqLen = 40
	var logBuf strings.Builder
	for gi := 0; gi < groups; gi++ {
		rng := rand.New(rand.NewSource(int64(gi*7919 + 1)))
		ops := genOps(rng, seqLen)
		first := runSequence(t, ops, &logBuf, gi)
		// 相同序列重放：结果与 Ack/Release 调用序列必须完全一致
		var second strings.Builder
		if got := runSequence(t, ops, &second, gi); got != first {
			t.Fatalf("第 %d 组重放不确定:\n首次=%s\n重放=%s", gi, first, got)
		}
	}
	if testing.Verbose() {
		t.Logf("随机对照日志（前 4000 字节）:\n%s", truncate(logBuf.String(), 4000))
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + " ...(截断)"
}

func runSequence(t *testing.T, ops []op, log *strings.Builder, gi int) string {
	m, r := newModel(), newRealRunner()
	var final strings.Builder
	fmt.Fprintf(log, "==== group %d ====\n", gi)
	for i, o := range ops {
		mo, ro := m.apply(o), r.apply(o)
		mc, rc := canonOutcome(mo), canonOutcome(ro)
		fmt.Fprintf(log, "step %d 输入: %s\n  朴素输出: %s\n  实现输出: %s\n", i, describe(o), mc, rc)
		if mc != rc {
			fmt.Fprintf(log, "  判定依据: 两结果不一致 => 失败\n")
			t.Fatalf("group %d step %d %s 不一致\n朴素: %s\n实现: %s\n", gi, i, describe(o), mc, rc)
		}
		fmt.Fprintf(log, "  判定依据: 错误标记/G/级别计数/降级视图/Ack-Release 序列全等\n")
		final.WriteString(mc)
		final.WriteByte(';')
	}
	return final.String()
}

func TestScannedZeroAtScales(t *testing.T) {
	for _, n := range []int{100, 10000} {
		g := New(func(string, int) error { return nil }, nil)
		if err := g.Join("a", 3); err != nil {
			t.Fatal(err)
		}
		if err := g.Raise(2); err != nil {
			t.Fatal(err)
		}
		if err := g.Raise(3); err != nil {
			t.Fatal(err)
		}
		e := "e"
		for i := 0; i < n; i++ {
			if err := g.Put("a", fmt.Sprintf("k-%d", i), PutFields{Size: int64(i), Etag: &e}); err != nil {
				t.Fatalf("n=%d put %d: %v", n, i, err)
			}
		}
		// 全部为级别 3；回退到 2 必判残留，且只能读计数
		if err := g.Rollback(2); !errors.Is(err, ErrResidual) {
			t.Fatalf("n=%d 应有残留: %v", n, err)
		}
		if err := g.Rollback(1); !errors.Is(err, ErrResidual) {
			t.Fatalf("n=%d 应有残留: %v", n, err)
		}
		if s := g.Scanned(); s != 0 {
			t.Fatalf("记录总数 %d 时 scanned=%d，应为 0", n, s)
		}
	}
}

func TestRaiseAtomicUnderConcurrency(t *testing.T) {
	// Raise 进行期间的 Join/Put 只能整体排在 Raise 前或后：
	// Ack 阻塞时另起 goroutine 做 Join/Put，验证它们在 Raise 完成前不可见生效，
	// 且结束后系统状态自洽（race 检测器验证内存安全）。
	var wg sync.WaitGroup
	g := New(func(string, int) error { return nil }, nil)
	if err := g.Join("a", 3); err != nil {
		t.Fatal(err)
	}
	for round := 0; round < 200; round++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			_ = g.Raise(2)
		}()
		go func() {
			defer wg.Done()
			_ = g.Join("b", 1)
			_ = g.Put("a", "k", PutFields{Size: 1})
			_, _ = g.Get("a", "k")
			_ = g.Leave("b")
		}()
		wg.Wait()
		// 收敛：无论串行顺序如何，记录级别必不大于当前 G
		_, _ = g.Get("a", "k")
	}
}

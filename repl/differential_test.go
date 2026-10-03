package repl

import (
	"fmt"
	"math/rand"
	"reflect"
	"sort"
	"strings"
	"testing"

	"ontology/link"
	"ontology/region"
)

// op 是随机序列中的一步；Send 的故障结果也由种子决定，保证可重放。
type op struct {
	kind    int // 0 put 1 delete 2 deliver 3 retry 4 get 5 diverged
	r, dst  string
	key     string
	size    int64
	ts      int64
	n       int
	id      region.VersionID
	failPct int
}

type mVersion struct {
	id     region.VersionID
	key    string
	size   int64
	ts     int64
	marker bool
}

type mItem struct {
	v        mVersion
	attempts int
}

type model struct {
	names      []string
	markerRepl bool
	mode       Mode
	capacity   int64
	retry      int

	seq     map[string]int64
	regions map[string]map[region.VersionID]mVersion
	queues  map[edge][]mItem
	failed  map[edge]map[region.VersionID]mItem
	backlog map[edge]int64
	lost    map[edge]int64
	enq     map[edge]int64
	del     map[edge]int64
	ftrans  map[edge]int64
	dup     map[string]int64
	probes  map[string]int64
}

func newModel(names []string, p Params) *model {
	m := &model{
		names: names, markerRepl: p.MarkerRepl, mode: p.Mode,
		capacity: p.Capacity, retry: p.RetryLimit,
		seq:     map[string]int64{},
		regions: map[string]map[region.VersionID]mVersion{},
		queues:  map[edge][]mItem{},
		failed:  map[edge]map[region.VersionID]mItem{},
		backlog: map[edge]int64{},
		lost:    map[edge]int64{}, enq: map[edge]int64{},
		del: map[edge]int64{}, ftrans: map[edge]int64{},
		dup: map[string]int64{}, probes: map[string]int64{},
	}
	for _, n := range names {
		m.seq[n] = 0
		m.regions[n] = map[region.VersionID]mVersion{}
	}
	for _, s := range names {
		for _, d := range names {
			if s != d {
				e := edge{s, d}
				m.failed[e] = map[region.VersionID]mItem{}
				m.queues[e] = nil
				m.backlog[e] = 0
				m.lost[e] = 0
				m.enq[e] = 0
				m.del[e] = 0
				m.ftrans[e] = 0
			}
		}
	}
	return m
}

func (m *model) create(r, key string, size, ts int64, marker bool) error {
	if key == "" || size < 0 || size > 1e9 || ts < 0 || ts > 1e12 {
		return ErrInvalidParam
	}
	if _, ok := m.regions[r]; !ok {
		return ErrNoRegion
	}
	var targets []string
	if !(marker && !m.markerRepl) {
		for _, n := range m.names {
			if n != r {
				targets = append(targets, n)
			}
		}
	}
	var accepted []string
	if m.mode == Strict {
		for _, d := range targets {
			if m.backlog[edge{r, d}]+size > m.capacity {
				return ErrBacklog
			}
		}
		accepted = targets
	} else {
		for _, d := range targets {
			e := edge{r, d}
			if m.backlog[e]+size > m.capacity {
				m.lost[e]++
			} else {
				accepted = append(accepted, d)
			}
		}
	}
	m.seq[r]++
	v := mVersion{region.VersionID{Origin: r, Seq: m.seq[r]}, key, size, ts, marker}
	m.regions[r][v.id] = v
	for _, d := range accepted {
		e := edge{r, d}
		m.queues[e] = append(m.queues[e], mItem{v: v})
		m.backlog[e] += size
		m.enq[e]++
	}
	return nil
}

// sendOutcome 生成一次 Send 结果（两侧必须调用完全相同的抽取序列）：
// roll < failPct/2   -> applied=true,  err!=nil（确认丢失：先 Apply 再计失败）
// failPct/2..failPct -> applied=false, err!=nil
// 否则               -> applied=true,  err=nil
func sendOutcome(rng *rand.Rand, failPct int) (applied bool, isErr bool) {
	roll := rng.Intn(100)
	if roll < failPct {
		return roll*2 < failPct, true
	}
	return true, false
}

func (m *model) deliver(src, dst string, n int, outcomes [][2]bool) {
	e := edge{src, dst}
	q := m.queues[e]
	calls := 0
	for len(q) > 0 && calls < n {
		it := q[0]
		calls++
		applied, isErr := outcomes[calls-1][0], outcomes[calls-1][1]
		var err error
		if isErr {
			err = errTest
		}
		if applied || err == nil {
			m.probes[dst]++
			if _, ok := m.regions[dst][it.v.id]; ok {
				m.dup[dst]++
			} else {
				m.regions[dst][it.v.id] = it.v
			}
		}
		if err == nil {
			q = q[1:]
			m.backlog[e] -= it.v.size
			m.del[e]++
			continue
		}
		it.attempts++
		if it.attempts < m.retry {
			q[0] = it
			break
		}
		q = q[1:]
		m.backlog[e] -= it.v.size
		m.ftrans[e]++
		m.failed[e][it.v.id] = it
	}
	m.queues[e] = q
}

func (m *model) retryOp(src, dst string, id region.VersionID) error {
	if src == "" || dst == "" || src == dst || id.Origin == "" || id.Seq < 1 {
		return ErrInvalidParam
	}
	if _, ok := m.regions[src]; !ok {
		return ErrInvalidParam
	}
	if _, ok := m.regions[dst]; !ok {
		return ErrInvalidParam
	}
	e := edge{src, dst}
	it, ok := m.failed[e][id]
	if !ok {
		return link.ErrNotFound
	}
	if m.backlog[e]+it.v.size > m.capacity {
		return ErrBacklog
	}
	delete(m.failed[e], id)
	it.attempts = 0
	m.queues[e] = append(m.queues[e], it)
	m.backlog[e] += it.v.size
	m.enq[e]++
	return nil
}

func (m *model) current(r, key string) (mVersion, bool) {
	var best mVersion
	have := false
	for _, v := range m.regions[r] {
		if v.key != key {
			continue
		}
		newer := !have || v.ts > best.ts ||
			(v.ts == best.ts && (v.id.Origin > best.id.Origin ||
				(v.id.Origin == best.id.Origin && v.id.Seq > best.id.Seq)))
		if newer {
			best, have = v, true
		}
	}
	return best, have
}

func (m *model) get(r, key string) (mVersion, error) {
	v, ok := m.current(r, key)
	if !ok {
		return mVersion{}, region.ErrNotFound
	}
	if v.marker {
		return v, region.ErrDeleted
	}
	return v, nil
}

func (m *model) diverged(key string) bool {
	var cur region.VersionID
	present := false
	for _, r := range m.names {
		v, ok := m.current(r, key)
		if !ok {
			return true
		}
		if !present {
			cur, present = v.id, true
		} else if v.id != cur {
			return true
		}
	}
	return false
}

type snapshot struct {
	seq     map[string]int64
	regions map[string][]region.VersionID
	pending map[string][]region.VersionID
	failed  map[string][]region.VersionID
	backlog map[string]int64
	lost    map[string]int64
	enq     map[string]int64
	del     map[string]int64
	ftrans  map[string]int64
	dup     map[string]int64
	probes  map[string]int64
}

func sortIDs(ids []region.VersionID) {
	sort.Slice(ids, func(i, j int) bool {
		if ids[i].Origin != ids[j].Origin {
			return ids[i].Origin < ids[j].Origin
		}
		return ids[i].Seq < ids[j].Seq
	})
}

func (m *model) snapshotState() snapshot {
	s := snapshot{
		seq: map[string]int64{}, regions: map[string][]region.VersionID{},
		pending: map[string][]region.VersionID{}, failed: map[string][]region.VersionID{},
		backlog: map[string]int64{}, lost: map[string]int64{}, enq: map[string]int64{},
		del: map[string]int64{}, ftrans: map[string]int64{},
		dup: map[string]int64{}, probes: map[string]int64{},
	}
	for r, n := range m.seq {
		s.seq[r] = n
	}
	for r, set := range m.regions {
		ids := make([]region.VersionID, 0, len(set))
		for _, v := range set {
			ids = append(ids, v.id)
		}
		sortIDs(ids)
		s.regions[r] = ids
		s.dup[r] = m.dup[r]
		s.probes[r] = m.probes[r]
	}
	for e, q := range m.queues {
		k := e.src + "->" + e.dst
		ids := make([]region.VersionID, 0, len(q))
		for _, it := range q {
			ids = append(ids, it.v.id)
		}
		sortIDs(ids)
		s.pending[k] = ids
		s.backlog[k] = m.backlog[e]
		s.enq[k] = m.enq[e]
		s.del[k] = m.del[e]
		s.ftrans[k] = m.ftrans[e]
		s.lost[k] = m.lost[e]
	}
	for e, f := range m.failed {
		k := e.src + "->" + e.dst
		ids := make([]region.VersionID, 0, len(f))
		for _, it := range f {
			ids = append(ids, it.v.id)
		}
		sortIDs(ids)
		s.failed[k] = ids
	}
	return s
}

func clusterSnapshot(t *testing.T, c *Cluster) snapshot {
	t.Helper()
	s := snapshot{
		seq: map[string]int64{}, regions: map[string][]region.VersionID{},
		pending: map[string][]region.VersionID{}, failed: map[string][]region.VersionID{},
		backlog: map[string]int64{}, lost: map[string]int64{}, enq: map[string]int64{},
		del: map[string]int64{}, ftrans: map[string]int64{},
		dup: map[string]int64{}, probes: map[string]int64{},
	}
	for _, r := range c.names {
		s.seq[r] = c.regions[r].Seq()
		ids := make([]region.VersionID, 0)
		for _, v := range c.regions[r].Versions() {
			ids = append(ids, v.ID)
		}
		sortIDs(ids)
		s.regions[r] = ids
		s.dup[r], _ = c.Duplicates(r)
		s.probes[r], _ = c.ApplyProbes(r)
	}
	for _, src := range c.names {
		for _, dst := range c.names {
			if src == dst {
				continue
			}
			k := src + "->" + dst
			l := c.links[edge{src, dst}]
			s.enq[k] = l.Enqueued()
			s.del[k] = l.Delivered()
			s.ftrans[k] = l.FailedTransfers()
			s.backlog[k] = l.Backlog()
			s.lost[k] = l.Lost()
			ids := make([]region.VersionID, 0, l.Pending())
			for _, it := range l.DumpQueue() {
				ids = append(ids, it.ID)
			}
			sortIDs(ids)
			s.pending[k] = ids
			fids, err := c.FailedIDs(src, dst)
			if err != nil {
				t.Fatal(err)
			}
			sortIDs(fids)
			s.failed[k] = fids
		}
	}
	return s
}

func errorKind(err error) string {
	switch err {
	case nil:
		return "nil"
	case region.ErrNotFound:
		return "notfound"
	case region.ErrDeleted:
		return "deleted"
	case ErrInvalidParam:
		return "param"
	case ErrNoRegion:
		return "noregion"
	case ErrBacklog:
		return "backlog"
	case link.ErrNotFound:
		return "failedmissing"
	default:
		return err.Error()
	}
}

func sameErr(a, b error) bool { return errorKind(a) == errorKind(b) }

func compareCurrent(t *testing.T, c *Cluster, m *model, keys []string) {
	t.Helper()
	for _, key := range keys {
		for _, r := range m.names {
			mv, merr := m.get(r, key)
			cv, cerr := c.Get(r, key)
			if !sameErr(merr, cerr) {
				t.Fatalf("Get(%s,%s) 错误不一致: model=%v cluster=%v", r, key, merr, cerr)
			}
			if merr == nil && mv.id != cv.ID {
				t.Fatalf("Get(%s,%s) 当前标识不一致: model=%v cluster=%v", r, key, mv.id, cv.ID)
			}
		}
		if m.diverged(key) != c.Diverged(key) {
			t.Fatalf("Diverged(%s) 不一致: model=%v cluster=%v", key, m.diverged(key), c.Diverged(key))
		}
	}
}

func indexOf(names []string, x string) int {
	for i, n := range names {
		if n == x {
			return i
		}
	}
	return 0
}

func TestRandomDifferential1500(t *testing.T) {
	if testing.Short() {
		t.Skip()
	}
	for seed := int64(1); seed <= 1500; seed++ {
		runDifferential(t, seed, false)
	}
}

// TestReplayDeterminism 抽取若干种子各重放两遍，逐字节快照必须相同。
func TestReplayDeterminism(t *testing.T) {
	if testing.Short() {
		t.Skip()
	}
	for _, seed := range []int64{1, 2, 7, 42, 99, 500, 1000, 1500} {
		first := runDifferential(t, seed, true)
		second := runDifferential(t, seed, true)
		if first != second {
			t.Fatalf("seed=%d 重放结果不一致", seed)
		}
	}
}

func runDifferential(t *testing.T, seed int64, captureLog bool) string {
	t.Helper()
	rng := rand.New(rand.NewSource(seed))

	nr := 2 + rng.Intn(3)
	names := make([]string, 0, nr)
	for i := 0; i < nr; i++ {
		names = append(names, string(rune('A'+i)))
	}
	p := Params{
		MarkerRepl: rng.Intn(2) == 0,
		Mode:       []Mode{Strict, Relaxed}[rng.Intn(2)],
		Capacity:   int64(1 + rng.Intn(40)),
		RetryLimit: 1 + rng.Intn(4),
	}

	c, err := New(append([]string(nil), names...), p)
	if err != nil {
		t.Fatalf("seed=%d 构造失败: %v", seed, err)
	}
	m := newModel(append([]string(nil), names...), p)

	keys := []string{"k1", "k2", "k3"}
	var createdIDs []region.VersionID
	steps := 60 + rng.Intn(60)
	var logb strings.Builder
	fmt.Fprintf(&logb, "=== seed=%d names=%v params=%+v steps=%d ===\n", seed, names, p, steps)

	matchSnap := func(stage string) {
		t.Helper()
		ms, cs := m.snapshotState(), clusterSnapshot(t, c)
		if !reflect.DeepEqual(ms, cs) {
			t.Fatalf("状态不一致[%s] seed=%d\n%s\nmodel=%#v\ncluster=%#v", stage, seed, logb.String(), ms, cs)
		}
	}

	for step := 0; step < steps; step++ {
		r := names[rng.Intn(len(names))]
		key := keys[rng.Intn(len(keys))]
		kind := rng.Intn(6)
		failPct := 30 + rng.Intn(40)

		switch kind {
		case 0: // Put
			size := int64(rng.Intn(int(2*p.Capacity) + 1))
			ts := int64(rng.Intn(20))
			// 偶尔注入非法参数或未知区域。
			bad := rng.Intn(15) == 0
			argR, argSize, argTS := r, size, ts
			if bad {
				switch rng.Intn(3) {
				case 0:
					argR = "Z"
				case 1:
					argSize = -1
				case 2:
					argTS = 1e12 + 1
				}
			}
			merr := m.create(argR, key, argSize, argTS, false)
			cv, cerr := c.Put(argR, key, argSize, argTS)
			if !sameErr(merr, cerr) {
				t.Fatalf("Put 不一致 seed=%d step=%d model=%v cluster=%v\n%s", seed, step, merr, cerr, logb.String())
			}
			if merr == nil {
				createdIDs = append(createdIDs, cv.ID)
			}
			fmt.Fprintf(&logb, "Put(%s,%s,size=%d,ts=%d) => %v ; 判定: 校验次序 参数非法>区域不存在>积压超限\n",
				argR, key, argSize, argTS, errLabel(cerr))

		case 1: // Delete
			ts := int64(rng.Intn(20))
			argR := r
			if rng.Intn(15) == 0 {
				argR = "Z"
			}
			merr := m.create(argR, key, 0, ts, true)
			cv, cerr := c.Delete(argR, key, ts)
			if !sameErr(merr, cerr) {
				t.Fatalf("Delete 不一致 seed=%d step=%d model=%v cluster=%v\n%s", seed, step, merr, cerr, logb.String())
			}
			if merr == nil {
				createdIDs = append(createdIDs, cv.ID)
			}
			fmt.Fprintf(&logb, "Delete(%s,%s,ts=%d) => %v ; MarkerRepl=%v 决定是否产生复制项\n",
				argR, key, ts, errLabel(cerr), p.MarkerRepl)

		case 2: // Deliver
			dst := names[rng.Intn(len(names))]
			if dst == r {
				dst = names[(indexOf(names, dst)+1)%len(names)]
			}
			n := 1 + rng.Intn(5)
			if rng.Intn(20) == 0 {
				n = 0
			}
			srcR := r
			if rng.Intn(30) == 0 {
				srcR = "Z"
			}
			invalid := n < 1 || n > 1000 || srcR == dst
			var cerr error
			if invalid || srcR == "Z" {
				cerr = c.Deliver(srcR, dst, n, func(link.Item) (bool, error) {
					t.Fatalf("参数非法时不得调用 Send seed=%d step=%d", seed, step)
					return false, nil
				})
			}
			merrCheck := error(nil)
			if invalid || srcR == "Z" {
				merrCheck = ErrInvalidParam
			}
			if !sameErr(merrCheck, cerr) {
				t.Fatalf("Deliver 参数错误不一致 seed=%d step=%d model=%v cluster=%v", seed, step, merrCheck, cerr)
			}
			if !invalid && srcR != "Z" {
				shared := rand.New(rand.NewSource(seed*100003 + int64(step)))
				outcomes := make([][2]bool, n)
				for i := range outcomes {
					a, e := sendOutcome(shared, failPct)
					outcomes[i] = [2]bool{a, e}
				}
				m.deliver(srcR, dst, n, outcomes)
				call := 0
				derr := c.Deliver(srcR, dst, n, func(link.Item) (bool, error) {
					applied, isErr := outcomes[call][0], outcomes[call][1]
					call++
					if isErr {
						return applied, errTest
					}
					return applied, nil
				})
				if derr != nil {
					t.Fatalf("Deliver 合法调用失败: %v", derr)
				}
				fmt.Fprintf(&logb, "Deliver(%s,%s,n=%d,failPct=%d) ; 每次 Send 计数1, err空出队, 达R=%d失败转移\n",
					srcR, dst, n, failPct, p.RetryLimit)
			}

		case 3: // Retry
			dst := names[rng.Intn(len(names))]
			if dst == r {
				dst = names[(indexOf(names, dst)+1)%len(names)]
			}
			var id region.VersionID
			if len(createdIDs) > 0 {
				id = createdIDs[rng.Intn(len(createdIDs))]
			} else {
				id = region.VersionID{Origin: r, Seq: int64(1 + rng.Intn(3))}
			}
			merr := m.retryOp(r, dst, id)
			cerr := c.Retry(r, dst, id)
			if !sameErr(merr, cerr) {
				t.Fatalf("Retry 不一致 seed=%d step=%d id=%v model=%v cluster=%v\n%s",
					seed, step, id, merr, cerr, logb.String())
			}
			fmt.Fprintf(&logb, "Retry(%s,%s,%v) => %v ; 次序 参数非法>不存在>积压超限\n", r, dst, id, errLabel(cerr))

		case 4, 5: // Get / Diverged 在下方统一读取比较
		}

		matchSnap(fmt.Sprintf("seed%d-step%d", seed, step))
		compareCurrent(t, c, m, keys)
	}

	// 最终阶段：不注入失败，尽可能排空在队项（失败项保留），再全量对照。
	for round := 0; round < 500; round++ {
		progressed := false
		for _, s := range names {
			for _, d := range names {
				if s == d {
					continue
				}
				if len(m.queues[edge{s, d}]) == 0 {
					continue
				}
				outcomes := make([][2]bool, 1000)
				for i := range outcomes {
					outcomes[i] = [2]bool{true, false}
				}
				m.deliver(s, d, 1000, outcomes)
				if err := c.Deliver(s, d, 1000, func(link.Item) (bool, error) { return true, nil }); err != nil {
					t.Fatal(err)
				}
				progressed = true
			}
		}
		matchSnap(fmt.Sprintf("seed%d-drain%d", seed, round))
		compareCurrent(t, c, m, keys)
		if !progressed {
			break
		}
	}

	matchSnap(fmt.Sprintf("seed%d-final", seed))
	compareCurrent(t, c, m, keys)

	// 恒等式：每条链路 入队=已投递+失败转移+在队。
	for _, s := range names {
		for _, d := range names {
			if s == d {
				continue
			}
			enq, del, ft, pending, _, err := c.LinkStats(s, d)
			if err != nil {
				t.Fatal(err)
			}
			if enq != del+ft+pending {
				t.Fatalf("seed=%d %s->%s 恒等式失败 %d!=%d+%d+%d\n%s",
					seed, s, d, enq, del, ft, pending, logb.String())
			}
			bl, err := c.Backlog(s, d)
			if err != nil {
				t.Fatal(err)
			}
			sum := int64(0)
			for _, it := range c.links[edge{s, d}].DumpQueue() {
				sum += it.Size
			}
			if sum != bl {
				t.Fatalf("seed=%d 积压字节与在队项大小之和不符", seed)
			}
		}
	}

	fmt.Fprintf(&logb, "=== seed=%d 最终判定: 快照/当前版本/Diverged/恒等式 全部一致 ===\n", seed)
	if verbose || captureLog {
		vlog(t, "%s", logb.String())
		return logb.String()
	}
	return logb.String()
}

func errLabel(err error) string {
	if err == nil {
		return "ok"
	}
	return errorKind(err)
}

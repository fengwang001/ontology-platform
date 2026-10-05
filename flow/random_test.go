package flow_test

import (
	"errors"
	"fmt"
	"math/rand"
	"reflect"
	"sort"
	"strings"
	"testing"

	"ontology/flow"
	"ontology/quarantine"
	"ontology/rule"
)

// naive 是逐步朴素模拟：逐条、逐字段、逐规则地按规格字面实现，
// 与 flow.Gate 的干跑/不变式优化无关，用于随机操作序列对照。
type naive struct {
	C, K, Dmax int
	rules      map[string]rule.Rule
	rv         uint64
	queues     map[string][]*nRec
	total      int
	discards   map[string]int
	out        []flow.OutEntry
	dropped    []quarantine.DropEntry
	seq        uint64
	evals      int64
}

type nRec struct {
	seq    uint64
	key    string
	fields map[string]int64
	held   bool
	rv     uint64
	viol   []string
}

func newNaive(c, k, dmax int) *naive {
	return &naive{
		C: c, K: k, Dmax: dmax,
		rules:    make(map[string]rule.Rule),
		queues:   make(map[string][]*nRec),
		discards: make(map[string]int),
	}
}

func cpFields(fields map[string]int64) map[string]int64 {
	out := make(map[string]int64, len(fields))
	for f, v := range fields {
		out[f] = v
	}
	return out
}

func (n *naive) eval(fields map[string]int64) []string {
	var viol []string
	for id, r := range n.rules {
		v, ok := fields[r.Field]
		if !ok || v < r.Lo || v > r.Hi {
			viol = append(viol, id)
		}
	}
	sort.Strings(viol)
	return viol
}

func (n *naive) hasBlock(viol []string) bool {
	for _, id := range viol {
		if n.rules[id].Severity == rule.Block {
			return true
		}
	}
	return false
}

func (n *naive) putRule(r rule.Rule) error {
	if r.ID == "" || r.Field == "" || r.Lo > r.Hi ||
		(r.Severity != rule.Block && r.Severity != rule.Warn) {
		return fmt.Errorf("%w: bad rule", flow.ErrInvalid)
	}
	n.rules[r.ID] = r
	n.rv++
	return nil
}

func (n *naive) dropRule(id string) error {
	if id == "" {
		return fmt.Errorf("%w: empty id", flow.ErrInvalid)
	}
	if _, ok := n.rules[id]; !ok {
		return fmt.Errorf("%w: %s", flow.ErrNoRule, id)
	}
	delete(n.rules, id)
	n.rv++
	return nil
}

func (n *naive) ingest(key string, fields map[string]int64) error {
	if key == "" || len(fields) < 1 || len(fields) > 16 {
		return fmt.Errorf("%w: bad record", flow.ErrInvalid)
	}
	if n.discards[key] >= n.Dmax {
		return fmt.Errorf("%w: %s", flow.ErrBanned, key)
	}
	if len(n.queues[key]) > 0 {
		if len(n.queues[key]) >= n.K {
			return fmt.Errorf("%w: %s", flow.ErrKeyFull, key)
		}
		if n.total >= n.C {
			return fmt.Errorf("%w: %s", flow.ErrFull, key)
		}
		n.seq++
		n.queues[key] = append(n.queues[key], &nRec{seq: n.seq, key: key, fields: cpFields(fields), held: true})
		n.total++
		return nil
	}
	n.evals++
	viol := n.eval(fields)
	if n.hasBlock(viol) {
		if n.total >= n.C {
			return fmt.Errorf("%w: %s", flow.ErrFull, key)
		}
		n.seq++
		n.queues[key] = append(n.queues[key], &nRec{seq: n.seq, key: key, fields: cpFields(fields), rv: n.rv, viol: viol})
		n.total++
		return nil
	}
	n.seq++
	n.out = append(n.out, flow.OutEntry{Seq: n.seq, Key: key, Violations: viol, RV: n.rv})
	return nil
}

func (n *naive) reeval(key string) (int, error) {
	q := n.queues[key]
	if len(q) == 0 {
		return 0, fmt.Errorf("%w: %s", flow.ErrNoQueue, key)
	}
	released := 0
	for len(q) > 0 {
		front := q[0]
		n.evals++
		viol := n.eval(front.fields)
		if n.hasBlock(viol) {
			front.held = false
			front.rv = n.rv
			front.viol = viol
			break
		}
		q = q[1:]
		n.total--
		n.out = append(n.out, flow.OutEntry{Seq: front.seq, Key: key, Violations: viol, RV: n.rv})
		released++
	}
	if len(q) == 0 {
		delete(n.queues, key)
	} else {
		n.queues[key] = q
	}
	return released, nil
}

func (n *naive) reevalAll() int {
	keys := make([]string, 0, len(n.queues))
	for k := range n.queues {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	total := 0
	for _, k := range keys {
		r, _ := n.reeval(k)
		total += r
	}
	return total
}

func (n *naive) fix(key string, patch map[string]int64) error {
	if len(patch) == 0 {
		return fmt.Errorf("%w: empty patch", flow.ErrInvalid)
	}
	q := n.queues[key]
	if len(q) == 0 {
		return fmt.Errorf("%w: %s", flow.ErrNoQueue, key)
	}
	merged := cpFields(q[0].fields)
	for f, v := range patch {
		merged[f] = v
	}
	if len(merged) > 16 {
		return fmt.Errorf("%w: too many fields", flow.ErrInvalid)
	}
	q[0].fields = merged
	_, _ = n.reeval(key)
	return nil
}

func (n *naive) release(key string) error {
	q := n.queues[key]
	if len(q) == 0 {
		return fmt.Errorf("%w: %s", flow.ErrNoQueue, key)
	}
	front := q[0]
	n.queues[key] = q[1:]
	if len(n.queues[key]) == 0 {
		delete(n.queues, key)
	}
	n.total--
	n.out = append(n.out, flow.OutEntry{
		Seq: front.seq, Key: key,
		Violations: append([]string(nil), front.viol...),
		RV:         front.rv, Forced: true,
	})
	if len(n.queues[key]) > 0 {
		_, _ = n.reeval(key)
	}
	return nil
}

func (n *naive) discard(key string) error {
	q := n.queues[key]
	if len(q) == 0 {
		return fmt.Errorf("%w: %s", flow.ErrNoQueue, key)
	}
	front := q[0]
	n.queues[key] = q[1:]
	if len(n.queues[key]) == 0 {
		delete(n.queues, key)
	}
	n.total--
	n.dropped = append(n.dropped, quarantine.DropEntry{
		Seq: front.seq, Key: key, RV: front.rv,
		Violations: append([]string(nil), front.viol...),
	})
	n.discards[key]++
	if len(n.queues[key]) > 0 {
		_, _ = n.reeval(key)
	}
	return nil
}

func (n *naive) clone() *naive {
	c := &naive{
		C: n.C, K: n.K, Dmax: n.Dmax,
		rules:    make(map[string]rule.Rule, len(n.rules)),
		rv:       n.rv,
		queues:   make(map[string][]*nRec, len(n.queues)),
		total:    n.total,
		discards: make(map[string]int, len(n.discards)),
		out:      append([]flow.OutEntry(nil), n.out...),
		dropped:  append([]quarantine.DropEntry(nil), n.dropped...),
		seq:      n.seq,
		evals:    n.evals,
	}
	for id, r := range n.rules {
		c.rules[id] = r
	}
	for k, q := range n.queues {
		nq := make([]*nRec, len(q))
		for i, rec := range q {
			dup := *rec
			dup.fields = cpFields(rec.fields)
			dup.viol = append([]string(nil), rec.viol...)
			nq[i] = &dup
		}
		c.queues[k] = nq
	}
	for k, v := range n.discards {
		c.discards[k] = v
	}
	return c
}

// batch 全有或全无：在整体克隆上逐条模拟，任一失败即整批回滚。
func (n *naive) batch(items []flow.Item) error {
	if len(items) < 1 || len(items) > 100 {
		return fmt.Errorf("%w: bad batch size", flow.ErrInvalid)
	}
	c := n.clone()
	for i, it := range items {
		if err := c.ingest(it.Key, it.Fields); err != nil {
			return &flow.BatchError{Index: i, Err: err}
		}
	}
	*n = *c
	return nil
}

func (n *naive) snapshot() []quarantine.Record {
	keys := make([]string, 0, len(n.queues))
	for k := range n.queues {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var out []quarantine.Record
	for _, k := range keys {
		for _, rec := range n.queues[k] {
			status := quarantine.Quarantined
			if rec.held {
				status = quarantine.Held
			}
			out = append(out, quarantine.Record{
				Seq: rec.seq, Key: rec.key, Fields: rec.fields,
				Status: status, RV: rec.rv, Violations: rec.viol,
			})
		}
	}
	return out
}

var sentinels = []error{
	flow.ErrInvalid, flow.ErrNoRule, flow.ErrKeyFull,
	flow.ErrFull, flow.ErrBanned, flow.ErrNoQueue,
}

// classify 把错误归类到哨兵，便于跨实现比较错误类别。
func classify(err error) error {
	if err == nil {
		return nil
	}
	for _, s := range sentinels {
		if errors.Is(err, s) {
			return s
		}
	}
	return err
}

// sameErr 判定两侧错误是否同类（批次错误还需下标一致）。
func sameErr(got, want error) bool {
	if got == nil || want == nil {
		return got == want
	}
	var gb, nb *flow.BatchError
	gIsBatch := errors.As(got, &gb)
	nIsBatch := errors.As(want, &nb)
	if gIsBatch != nIsBatch {
		return false
	}
	if gIsBatch {
		return gb.Index == nb.Index && classify(gb.Err) == classify(nb.Err)
	}
	return classify(got) == classify(want)
}

var (
	randKeys   = []string{"a", "b", "c", "d", "\x00", "\xff"}
	randFields = []string{"amt", "qty", "w"}
	randRuleID = []string{"r1", "r2", "r3", "r4"}
)

func randomFields(rng *rand.Rand) map[string]int64 {
	switch p := rng.Intn(20); {
	case p < 3: // 非法：无字段
		return nil
	case p == 3: // 非法：17 个字段
		m := make(map[string]int64, 17)
		for i := 0; i < 17; i++ {
			m[fmt.Sprintf("f%d", i)] = int64(i)
		}
		return m
	}
	m := make(map[string]int64)
	for _, f := range randFields {
		if rng.Intn(2) == 0 {
			m[f] = int64(rng.Intn(16) - 3)
		}
	}
	if len(m) == 0 {
		m[randFields[rng.Intn(len(randFields))]] = int64(rng.Intn(16) - 3)
	}
	return m
}

func randomRule(rng *rand.Rand) rule.Rule {
	r := rule.Rule{
		ID:    randRuleID[rng.Intn(len(randRuleID))],
		Field: randFields[rng.Intn(len(randFields))],
		Lo:    int64(rng.Intn(9) - 3),
	}
	if rng.Intn(2) == 0 {
		r.Field = "missing"
	}
	r.Hi = r.Lo + int64(rng.Intn(9))
	if rng.Intn(2) == 0 {
		r.Severity = rule.Block
	} else {
		r.Severity = rule.Warn
	}
	switch p := rng.Intn(10); p { // 20% 概率非法规则
	case 0:
		r.ID = ""
	case 1:
		r.Lo, r.Hi = r.Hi+1, r.Lo
	}
	return r
}

func checkInvariants(t *testing.T, g *flow.Gate, trial int) {
	t.Helper()
	out, dropped, queued := g.Out(), g.Dropped(), g.Queued()
	seen := make(map[uint64]int)
	perKeyOut := make(map[string]uint64)
	for _, e := range out {
		seen[e.Seq]++
		if prev, ok := perKeyOut[e.Key]; ok && e.Seq <= prev {
			t.Fatalf("trial %d: key %q Out seqs not increasing", trial, e.Key)
		}
		perKeyOut[e.Key] = e.Seq
	}
	perKeyDropped := make(map[string]uint64)
	for _, e := range dropped {
		seen[e.Seq]++
		if prev, ok := perKeyDropped[e.Key]; ok && e.Seq <= prev {
			t.Fatalf("trial %d: key %q Dropped seqs not increasing", trial, e.Key)
		}
		perKeyDropped[e.Key] = e.Seq
	}
	for _, r := range queued {
		seen[r.Seq]++
	}
	for seq, cnt := range seen {
		if cnt != 1 {
			t.Fatalf("trial %d: seq %d appears %d times", trial, seq, cnt)
		}
	}
	// 被拒绝的记录不占号：seq 恰为 1..N。
	for i := 1; i <= len(seen); i++ {
		if seen[uint64(i)] != 1 {
			t.Fatalf("trial %d: seq %d missing, logs not contiguous", trial, i)
		}
	}
	if len(queued) > 0 && len(queued) > 100000 {
		t.Fatalf("trial %d: zone overflow", trial)
	}
	// 队首不变式：队首 Quarantined、其后全 Held。
	prevKey := ""
	isHead := true
	for _, r := range queued {
		if r.Key != prevKey {
			prevKey, isHead = r.Key, true
		}
		if isHead && r.Status != quarantine.Quarantined {
			t.Fatalf("trial %d: head of %q is %v", trial, r.Key, r.Status)
		}
		if !isHead && r.Status != quarantine.Held {
			t.Fatalf("trial %d: non-head of %q is %v", trial, r.Key, r.Status)
		}
		isHead = false
	}
}

// TestRandomReplay 用 1500 组随机操作序列对照 flow.Gate 与朴素模拟，
// 逐操作比较错误类别、Out、Dropped、隔离区快照、evals 与 rv。
func TestRandomReplay(t *testing.T) {
	const trials, opsPerTrial = 1500, 60
	for trial := 0; trial < trials; trial++ {
		rng := rand.New(rand.NewSource(int64(trial)))
		c := 1 + rng.Intn(8)
		k := 1 + rng.Intn(c)
		dmax := 1 + rng.Intn(4)
		g, err := flow.NewGate(c, k, dmax)
		if err != nil {
			t.Fatalf("trial %d: NewGate: %v", trial, err)
		}
		n := newNaive(c, k, dmax)
		var trace strings.Builder
		fmt.Fprintf(&trace, "trial %d: C=%d K=%d Dmax=%d\n", trial, c, k, dmax)

		for op := 0; op < opsPerTrial; op++ {
			evalsBefore := g.Evals()
			var gerr, nerr error
			var desc string
			pickKey := func() string { return randKeys[rng.Intn(len(randKeys))] }
			switch x := rng.Intn(100); {
			case x < 18:
				r := randomRule(rng)
				gerr = g.PutRule(r.ID, r.Field, r.Lo, r.Hi, r.Severity)
				nerr = n.putRule(r)
				desc = fmt.Sprintf("PutRule(%+v)", r)
			case x < 25:
				id := randRuleID[rng.Intn(len(randRuleID))]
				if rng.Intn(4) == 0 {
					id = "nope"
				}
				gerr = g.DropRule(id)
				nerr = n.dropRule(id)
				desc = fmt.Sprintf("DropRule(%q)", id)
			case x < 57:
				key, fields := pickKey(), randomFields(rng)
				gerr = g.Ingest(key, fields)
				nerr = n.ingest(key, fields)
				desc = fmt.Sprintf("Ingest(%q,%v)", key, fields)
			case x < 67:
				size := 1 + rng.Intn(5)
				items := make([]flow.Item, size)
				for i := range items {
					items[i] = flow.Item{Key: pickKey(), Fields: randomFields(rng)}
				}
				gerr = g.IngestBatch(items)
				nerr = n.batch(items)
				desc = fmt.Sprintf("IngestBatch(%v)", items)
			case x < 76:
				key := pickKey()
				var gn, nn int
				gn, gerr = g.Reeval(key)
				nn, nerr = n.reeval(key)
				if gerr == nil && nerr == nil && gn != nn {
					t.Fatalf("trial %d op %d: Reeval(%q) released %d vs naive %d\n%s",
						trial, op, key, gn, nn, trace.String())
				}
				desc = fmt.Sprintf("Reeval(%q)=%d", key, gn)
			case x < 80:
				gn, nn := g.ReevalAll(), n.reevalAll()
				if gn != nn {
					t.Fatalf("trial %d op %d: ReevalAll %d vs naive %d\n%s",
						trial, op, gn, nn, trace.String())
				}
				desc = fmt.Sprintf("ReevalAll()=%d", gn)
			case x < 88:
				key := pickKey()
				patch := randomFields(rng)
				gerr = g.Fix(key, patch)
				nerr = n.fix(key, patch)
				desc = fmt.Sprintf("Fix(%q,%v)", key, patch)
			case x < 94:
				key := pickKey()
				gerr = g.Release(key)
				nerr = n.release(key)
				desc = fmt.Sprintf("Release(%q)", key)
			default:
				key := pickKey()
				gerr = g.Discard(key)
				nerr = n.discard(key)
				desc = fmt.Sprintf("Discard(%q)", key)
			}
			fmt.Fprintf(&trace, "op%02d %-44s -> %-30v evals+%d\n",
				op, desc, gerr, g.Evals()-evalsBefore)
			if !sameErr(gerr, nerr) {
				t.Fatalf("trial %d op %d %s: gate err %v vs naive err %v\n%s",
					trial, op, desc, gerr, nerr, trace.String())
			}
			if !reflect.DeepEqual(g.Out(), n.out) {
				t.Fatalf("trial %d op %d %s: Out diverged\ngate:  %+v\nnaive: %+v\n%s",
					trial, op, desc, g.Out(), n.out, trace.String())
			}
			if !reflect.DeepEqual(g.Dropped(), n.dropped) {
				t.Fatalf("trial %d op %d %s: Dropped diverged\ngate:  %+v\nnaive: %+v\n%s",
					trial, op, desc, g.Dropped(), n.dropped, trace.String())
			}
			if !reflect.DeepEqual(g.Queued(), n.snapshot()) {
				t.Fatalf("trial %d op %d %s: zone diverged\ngate:  %+v\nnaive: %+v\n%s",
					trial, op, desc, g.Queued(), n.snapshot(), trace.String())
			}
			if g.Evals() != n.evals {
				t.Fatalf("trial %d op %d %s: evals %d vs naive %d\n%s",
					trial, op, desc, g.Evals(), n.evals, trace.String())
			}
			if g.RV() != n.rv {
				t.Fatalf("trial %d op %d %s: rv %d vs naive %d\n%s",
					trial, op, desc, g.RV(), n.rv, trace.String())
			}
		}
		checkInvariants(t, g, trial)
		t.Logf("trial %d done: out=%d dropped=%d queued=%d evals=%d rv=%d\n%s",
			trial, len(g.Out()), len(g.Dropped()), len(g.Queued()), g.Evals(), g.RV(), trace.String())
	}
}

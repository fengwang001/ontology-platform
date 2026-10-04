package flow

import (
	"fmt"
	"math/rand"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"

	"ontology/rule"
)

// naiveOut / naiveDrop 是朴素模型中的记录，字段与真实系统逐一对应。
type naiveRec struct {
	seq        int64
	key        string
	fields     map[string]int64
	rv         int64
	violations []string
	forced     bool
}

type naiveRule struct {
	field  string
	lo, hi int64
	sev    rule.Severity
}

type naiveEntry struct {
	rec   naiveRec
	state quarantineState
}

type quarantineState int

const (
	qState quarantineState = iota
	heldState
)

// naiveModel 完全按规格文字逐步重放，与生产实现刻意不同：
// 每次重判都从规则表即时求值，不做快照复用；队列用朴素切片。
type naiveModel struct {
	c, k, dmax int
	rules      map[string]naiveRule
	rv         int64
	seq        int64
	evals      int64
	queues     map[string][]naiveEntry
	discard    map[string]int
	out        []naiveRec
	dropped    []naiveRec
}

func newNaive(c, k, dmax int) *naiveModel {
	return &naiveModel{
		c: c, k: k, dmax: dmax,
		rules:   map[string]naiveRule{},
		queues:  map[string][]naiveEntry{},
		discard: map[string]int{},
	}
}

func (m *naiveModel) eval(fields map[string]int64) (int64, []string, bool) {
	m.evals++
	ids := make([]string, 0)
	block := false
	for id := range m.rules {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	viol := []string(nil)
	for _, id := range ids {
		r := m.rules[id]
		v, ok := fields[r.field]
		if !ok || v < r.lo || v > r.hi {
			viol = append(viol, id)
			if r.sev == rule.Block {
				block = true
			}
		}
	}
	return m.rv, viol, block
}

func (m *naiveModel) put(id, field string, lo, hi int64, sev rule.Severity) {
	m.rules[id] = naiveRule{field: field, lo: lo, hi: hi, sev: sev}
	m.rv++
}

func (m *naiveModel) drop(id string) bool {
	if _, ok := m.rules[id]; !ok {
		return false
	}
	delete(m.rules, id)
	m.rv++
	return true
}

func (m *naiveModel) total() int {
	n := 0
	for _, q := range m.queues {
		n += len(q)
	}
	return n
}

func (m *naiveModel) ingest(key string, fields map[string]int64) (string, error) {
	if m.discard[key] >= m.dmax {
		return "", ErrBanned
	}
	clone := cloneM(fields)
	if len(m.queues[key]) > 0 {
		if len(m.queues[key]) >= m.k {
			return "", ErrKeyFull
		}
		if m.total() >= m.c {
			return "", ErrFull
		}
		m.seq++
		m.queues[key] = append(m.queues[key], naiveEntry{
			rec:   naiveRec{seq: m.seq, key: key, fields: clone},
			state: heldState,
		})
		return "Held", nil
	}
	rv, viol, block := m.eval(clone)
	if !block {
		m.seq++
		m.out = append(m.out, naiveRec{seq: m.seq, key: key, fields: clone, rv: rv, violations: viol})
		return "Passed", nil
	}
	if m.total() >= m.c {
		m.evals--
		return "", ErrFull
	}
	m.seq++
	m.queues[key] = append(m.queues[key], naiveEntry{
		rec:   naiveRec{seq: m.seq, key: key, fields: clone, rv: rv, violations: viol},
		state: qState,
	})
	return "Quarantined", nil
}

func (m *naiveModel) reeval(key string) (int, error) {
	q := m.queues[key]
	if len(q) == 0 {
		return 0, ErrNoQueue
	}
	n := 0
	for len(q) > 0 {
		rv, viol, block := m.eval(q[0].rec.fields)
		if block {
			q[0].state = qState
			q[0].rec.rv = rv
			q[0].rec.violations = viol
			break
		}
		r := q[0].rec
		r.rv = rv
		r.violations = viol
		m.out = append(m.out, r)
		q = q[1:]
		n++
	}
	m.queues[key] = q
	return n, nil
}

func (m *naiveModel) fix(key string, patch map[string]int64) (int, error) {
	q := m.queues[key]
	if len(q) == 0 {
		return 0, ErrNoQueue
	}
	added := 0
	for name := range patch {
		if _, ok := q[0].rec.fields[name]; !ok {
			added++
		}
	}
	if len(q[0].rec.fields)+added > 16 {
		return 0, ErrInvalidParam
	}
	for name, v := range patch {
		q[0].rec.fields[name] = v
	}
	return m.reeval(key)
}

func (m *naiveModel) release(key string) (int, error) {
	q := m.queues[key]
	if len(q) == 0 {
		return 0, ErrNoQueue
	}
	r := q[0].rec
	r.forced = true
	m.out = append(m.out, r)
	m.queues[key] = q[1:]
	n, _ := m.reeval(key) // 无后继时隐式重判视为正常（0 放行）
	return n, nil
}

func (m *naiveModel) discardRec(key string) (int, error) {
	q := m.queues[key]
	if len(q) == 0 {
		return 0, ErrNoQueue
	}
	r := q[0].rec
	m.dropped = append(m.dropped, r)
	m.queues[key] = q[1:]
	m.discard[key]++
	n, _ := m.reeval(key)
	return n, nil
}

func cloneM(in map[string]int64) map[string]int64 {
	out := make(map[string]int64, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

// op 是一条随机脚本操作；两系统执行相同脚本。
type op struct {
	kind   string
	key    string
	fields map[string]int64
	rID    string
	rField string
	rLo    int64
	rHi    int64
	rSev   rule.Severity
	items  []Item
}

func genOps(rng *rand.Rand) []op {
	n := 30 + rng.Intn(60)
	keys := []string{"a", "b", "c"}
	fields := []string{"amt", "qty", "x"}
	ops := make([]op, n)
	ruleIDs := []string{"r1", "r2"}
	for i := range ops {
		switch rng.Intn(12) {
		case 0, 1, 2: // ingest
			f := map[string]int64{}
			for _, fld := range fields[:1+rng.Intn(2)] {
				f[fld] = int64(rng.Intn(12)) - 1
			}
			ops[i] = op{kind: "ingest", key: keys[rng.Intn(len(keys))], fields: f}
		case 3, 4: // batch
			cnt := 1 + rng.Intn(4)
			items := make([]Item, cnt)
			for j := range items {
				items[j] = Item{Key: keys[rng.Intn(len(keys))],
					Fields: map[string]int64{"amt": int64(rng.Intn(12)) - 1}}
			}
			ops[i] = op{kind: "batch", items: items}
		case 5: // put rule
			id := ruleIDs[rng.Intn(2)]
			lo := int64(rng.Intn(5))
			hi := lo + int64(rng.Intn(8))
			sev := rule.Block
			if rng.Intn(2) == 0 {
				sev = rule.Warn
			}
			ops[i] = op{kind: "put", rID: id, rField: fields[rng.Intn(len(fields))],
				rLo: lo, rHi: hi, rSev: sev}
		case 6:
			ops[i] = op{kind: "drop", rID: ruleIDs[rng.Intn(2)]}
		case 7:
			ops[i] = op{kind: "reeval", key: append(keys, "zzz")[rng.Intn(len(keys)+1)]}
		case 8:
			ops[i] = op{kind: "reevalall"}
		case 9:
			ops[i] = op{kind: "fix", key: keys[rng.Intn(len(keys))],
				fields: map[string]int64{"amt": int64(rng.Intn(12))}}
		case 10:
			ops[i] = op{kind: "release", key: keys[rng.Intn(len(keys))]}
		case 11:
			ops[i] = op{kind: "discard", key: keys[rng.Intn(len(keys))]}
		}
	}
	return ops
}

func fieldsString(f map[string]int64) string {
	parts := make([]string, 0, len(f))
	keys := make([]string, 0, len(f))
	for k := range f {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		parts = append(parts, k+"="+strconv.FormatInt(f[k], 10))
	}
	return "{" + strings.Join(parts, ",") + "}"
}

func describeOp(o op) string {
	switch o.kind {
	case "ingest":
		return fmt.Sprintf("Ingest(%q,%s)", o.key, fieldsString(o.fields))
	case "batch":
		ps := make([]string, len(o.items))
		for i, it := range o.items {
			ps[i] = fmt.Sprintf("(%q,%s)", it.Key, fieldsString(it.Fields))
		}
		return "IngestBatch([" + strings.Join(ps, ",") + "])"
	case "put":
		return fmt.Sprintf("PutRule(%s,%s,%d,%d,%v)", o.rID, o.rField, o.rLo, o.rHi, o.rSev)
	case "drop":
		return fmt.Sprintf("DropRule(%q)", o.rID)
	case "reeval":
		return fmt.Sprintf("Reeval(%q)", o.key)
	case "reevalall":
		return "ReevalAll()"
	case "fix":
		return fmt.Sprintf("Fix(%q,%s)", o.key, fieldsString(o.fields))
	case "release":
		return fmt.Sprintf("Release(%q)", o.key)
	case "discard":
		return fmt.Sprintf("Discard(%q)", o.key)
	}
	return o.kind
}

// snapshot 是两系统可比较的全部可观察状态。
type snapshot struct {
	rv     int64
	seq    int64
	evals  int64
	total  int
	out    []naiveRec
	drop   []naiveRec
	queues map[string][]naiveEntry
	banned map[string]bool
}

var obsKeys = []string{"a", "b", "c", "zzz"}

func realSnapshot(g *Gate) snapshot {
	s := snapshot{
		rv: g.RV(), seq: g.NextSeq() - 1, evals: g.Evals(), total: g.Quarantined(),
		queues: map[string][]naiveEntry{}, banned: map[string]bool{},
	}
	for _, r := range g.Out() {
		s.out = append(s.out, naiveRec{
			seq: r.Seq, key: r.Key, fields: cloneM(r.Fields),
			rv: r.RV, violations: append([]string(nil), r.Violations...), forced: r.Forced,
		})
	}
	for _, r := range g.Dropped() {
		s.drop = append(s.drop, naiveRec{seq: r.Seq, key: r.Key, fields: cloneM(r.Fields)})
	}
	for _, key := range obsKeys {
		for i := 0; ; i++ {
			e, ok := g.zone.At(key, i)
			if !ok {
				break
			}
			st := heldState
			if e.State == 1 {
				st = qState
			}
			s.queues[key] = append(s.queues[key], naiveEntry{
				rec: naiveRec{
					seq: e.Seq, key: e.Key, fields: cloneM(e.Fields),
					rv: e.RV, violations: append([]string(nil), e.Violations...),
				},
				state: st,
			})
		}
		s.banned[key] = g.Banned(key)
	}
	return s
}

func naiveSnapshot(m *naiveModel) snapshot {
	s := snapshot{
		rv: m.rv, seq: m.seq, evals: m.evals, total: m.total(),
		queues: map[string][]naiveEntry{}, banned: map[string]bool{},
	}
	for _, r := range m.out {
		s.out = append(s.out, naiveRec{
			seq: r.seq, key: r.key, fields: cloneM(r.fields),
			rv: r.rv, violations: append([]string(nil), r.violations...), forced: r.forced,
		})
	}
	for _, r := range m.dropped {
		s.drop = append(s.drop, naiveRec{seq: r.seq, key: r.key, fields: cloneM(r.fields)})
	}
	for _, key := range obsKeys {
		for _, e := range m.queues[key] {
			s.queues[key] = append(s.queues[key], naiveEntry{
				rec: naiveRec{
					seq: e.rec.seq, key: e.rec.key, fields: cloneM(e.rec.fields),
					rv: e.rec.rv, violations: append([]string(nil), e.rec.violations...),
				},
				state: e.state,
			})
		}
		s.banned[key] = m.discard[key] >= m.dmax
	}
	return s
}

func recsEqual(a, b []naiveRec) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		x, y := a[i], b[i]
		if x.seq != y.seq || x.key != y.key || x.rv != y.rv || x.forced != y.forced ||
			!reflect.DeepEqual(x.fields, y.fields) || !reflect.DeepEqual(x.violations, y.violations) {
			return false
		}
	}
	return true
}

func queuesEqual(a, b map[string][]naiveEntry) bool {
	keys := map[string]bool{}
	for k := range a {
		keys[k] = true
	}
	for k := range b {
		keys[k] = true
	}
	for k := range keys {
		x, y := a[k], b[k]
		if len(x) != len(y) {
			return false
		}
		for i := range x {
			r1, r2 := x[i].rec, y[i].rec
			if r1.seq != r2.seq || r1.key != r2.key || r1.rv != r2.rv ||
				x[i].state != y[i].state ||
				!reflect.DeepEqual(r1.fields, r2.fields) ||
				!reflect.DeepEqual(r1.violations, r2.violations) {
				return false
			}
		}
	}
	return true
}

func snapshotsEqual(a, b snapshot) (bool, string) {
	if a.rv != b.rv {
		return false, fmt.Sprintf("rv %d != %d", a.rv, b.rv)
	}
	if a.seq != b.seq {
		return false, fmt.Sprintf("seq %d != %d", a.seq, b.seq)
	}
	if a.evals != b.evals {
		return false, fmt.Sprintf("evals %d != %d", a.evals, b.evals)
	}
	if a.total != b.total {
		return false, fmt.Sprintf("total %d != %d", a.total, b.total)
	}
	if !recsEqual(a.out, b.out) {
		return false, fmt.Sprintf("Out 不一致:\n real=%v\n naive=%v", a.out, b.out)
	}
	if !recsEqual(a.drop, b.drop) {
		return false, fmt.Sprintf("Dropped 不一致:\n real=%v\n naive=%v", a.drop, b.drop)
	}
	if !queuesEqual(a.queues, b.queues) {
		return false, fmt.Sprintf("队列不一致:\n real=%v\n naive=%v", a.queues, b.queues)
	}
	if !reflect.DeepEqual(a.banned, b.banned) {
		return false, fmt.Sprintf("banned %v != %v", a.banned, b.banned)
	}
	return true, ""
}

func runReal(g *Gate, o op) (string, error) {
	switch o.kind {
	case "ingest":
		st, err := g.Ingest(o.key, o.fields)
		return st.String(), err
	case "batch":
		st, err := g.IngestBatch(o.items)
		if err != nil {
			return "", err
		}
		names := make([]string, len(st))
		for i, s := range st {
			names[i] = s.String()
		}
		return strings.Join(names, ","), nil
	case "put":
		err := g.PutRule(rule.Rule{ID: o.rID, Field: o.rField, Lo: o.rLo, Hi: o.rHi, Severity: o.rSev})
		return "", err
	case "drop":
		return "", g.DropRule(o.rID)
	case "reeval":
		n, err := g.Reeval(o.key)
		return strconv.Itoa(n), err
	case "reevalall":
		return strconv.Itoa(g.ReevalAll()), nil
	case "fix":
		n, err := g.Fix(o.key, o.fields)
		return strconv.Itoa(n), err
	case "release":
		n, err := g.Release(o.key)
		return strconv.Itoa(n), err
	case "discard":
		n, err := g.Discard(o.key)
		return strconv.Itoa(n), err
	}
	return "", nil
}

type savedState struct {
	seq     int64
	evals   int64
	queues  map[string][]naiveEntry
	out     []naiveRec
	dropped []naiveRec
}

func (m *naiveModel) snapshotState() savedState {
	s := savedState{seq: m.seq, evals: m.evals,
		queues: map[string][]naiveEntry{}, out: m.out, dropped: m.dropped}
	for k, q := range m.queues {
		s.queues[k] = append([]naiveEntry(nil), q...)
	}
	return s
}

func (m *naiveModel) restoreState(s savedState) {
	m.seq, m.evals = s.seq, s.evals
	m.out, m.dropped = s.out, s.dropped
	m.queues = map[string][]naiveEntry{}
	for k, q := range s.queues {
		m.queues[k] = append([]naiveEntry(nil), q...)
	}
}

func (m *naiveModel) sortedKeys() []string {
	keys := make([]string, 0)
	for k, q := range m.queues {
		if len(q) > 0 {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	return keys
}

// runNaiveBatch 逐条试算并在失败时整体回滚，错误下标取最小。
func (m *naiveModel) runNaiveBatch(items []Item) (string, error) {
	saved := m.snapshotState()
	names := make([]string, 0, len(items))
	for i, it := range items {
		name, err := m.ingest(it.Key, it.Fields)
		if err != nil {
			m.restoreState(saved)
			return "", &BatchError{Index: i, Err: err}
		}
		names = append(names, name)
	}
	return strings.Join(names, ","), nil
}

func runNaive(m *naiveModel, o op) (string, error) {
	switch o.kind {
	case "ingest":
		return m.ingest(o.key, o.fields)
	case "batch":
		return m.runNaiveBatch(o.items)
	case "put":
		m.put(o.rID, o.rField, o.rLo, o.rHi, o.rSev)
		return "", nil
	case "drop":
		if !m.drop(o.rID) {
			return "", ErrRuleNotFound
		}
		return "", nil
	case "reeval":
		n, err := m.reeval(o.key)
		return strconv.Itoa(n), err
	case "reevalall":
		total := 0
		for _, key := range m.sortedKeys() {
			n, _ := m.reeval(key)
			total += n
		}
		return strconv.Itoa(total), nil
	case "fix":
		n, err := m.fix(o.key, o.fields)
		return strconv.Itoa(n), err
	case "release":
		n, err := m.release(o.key)
		return strconv.Itoa(n), err
	case "discard":
		n, err := m.discardRec(o.key)
		return strconv.Itoa(n), err
	}
	return "", nil
}

func sameError(a, b error) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return errorsIs(a, b) && errorsIs(b, a)
}

func errorsIs(err, target error) bool {
	type iser interface{ Is(error) bool }
	for err != nil {
		if err == target {
			return true
		}
		if x, ok := err.(iser); ok && x.Is(target) {
			return true
		}
		if u, ok := err.(interface{ Unwrap() error }); ok {
			err = u.Unwrap()
			continue
		}
		break
	}
	return false
}

func assertPerKeyOrder(t *testing.T, g *Gate, seed int64) {
	t.Helper()
	merged := map[string][]int64{}
	for _, ev := range g.Events() {
		merged[ev.Key] = append(merged[ev.Key], ev.Seq)
	}
	for key, seqs := range merged {
		for i := 1; i < len(seqs); i++ {
			if seqs[i] <= seqs[i-1] {
				t.Fatalf("seed=%d 键 %q 合并日志 seq 非严格递增: %v", seed, key, seqs)
			}
		}
	}
}

// TestRandomDifferential 1500 组随机操作序列：真实系统与逐步朴素模拟逐项对照。
// -v 时打印输入、输出与规则版本（判定依据）；失败时完整打印该用例日志。
func TestRandomDifferential(t *testing.T) {
	for seed := int64(0); seed < 1500; seed++ {
		rng := rand.New(rand.NewSource(seed))
		C := 1 + rng.Intn(8)
		K := 1 + rng.Intn(C)
		Dmax := 1 + rng.Intn(3)
		ops := genOps(rng)

		g, err := New(C, K, Dmax)
		if err != nil {
			t.Fatalf("seed=%d New: %v", seed, err)
		}
		m := newNaive(C, K, Dmax)

		var log strings.Builder
		fmt.Fprintf(&log, "seed=%d C=%d K=%d Dmax=%d\n", seed, C, K, Dmax)

		for step, o := range ops {
			realRes, realErr := runReal(g, o)
			naiveRes, naiveErr := runNaive(m, o)
			if realErr != nil {
				realRes = ""
			}
			if naiveErr != nil {
				naiveRes = ""
			}
			fmt.Fprintf(&log, "[%d] %s => real(%q,%v) naive(%q,%v)\n",
				step, describeOp(o), realRes, realErr, naiveRes, naiveErr)

			if be1, be2 := asBatch(realErr), asBatch(naiveErr); be1 != nil || be2 != nil {
				if be1 == nil || be2 == nil || be1.Index != be2.Index || !sameError(be1.Err, be2.Err) {
					t.Fatalf("seed=%d step=%d 批次返回不一致:\n%s", seed, step, log.String())
				}
			} else if !sameError(realErr, naiveErr) || realRes != naiveRes {
				t.Fatalf("seed=%d step=%d 返回不一致:\n%s", seed, step, log.String())
			}

			rs, ns := realSnapshot(g), naiveSnapshot(m)
			if ok, why := snapshotsEqual(rs, ns); !ok {
				t.Fatalf("seed=%d step=%d 状态不一致（%s）:\n%s", seed, step, why, log.String())
			}
		}

		assertPerKeyOrder(t, g, seed)
		if testing.Verbose() && seed < 3 {
			out := g.Out()
			seqs := make([]int64, len(out))
			for i, r := range out {
				seqs[i] = r.Seq
			}
			t.Logf("seed=%d 通过；最终 Out seqs=%v Dropped=%d evals=%d rv=%d\n%s",
				seed, seqs, len(g.Dropped()), g.Evals(), g.RV(), log.String())
		}
	}
}

func asBatch(err error) *BatchError {
	var be *BatchError
	if errorsAs(err, &be) {
		return be
	}
	return nil
}

func errorsAs(err error, target **BatchError) bool {
	for err != nil {
		if be, ok := err.(*BatchError); ok {
			*target = be
			return true
		}
		if u, ok := err.(interface{ Unwrap() error }); ok {
			err = u.Unwrap()
			continue
		}
		break
	}
	return false
}

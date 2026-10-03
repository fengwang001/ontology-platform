package repl_test

// 本文件用一份独立的“逐步朴素模型”与真实实现对照：
// 模型完全按题面规则用 map + slice 直接重写，不复用生产代码的任何判定逻辑
// （只共享 region.Version / VersionID 数据类型做比较）。
//
// 每组用例预先生成操作序列与每个 Send 调用的随机应答，
// 依次喂给真实系统与朴素模型，每步比较结果与全量快照，并用 t.Log 打印
// 输入、输出与判定依据。

import (
	"fmt"
	"math/rand"
	"sort"
	"strings"
	"testing"

	"ontology/link"
	"ontology/region"
	"ontology/repl"
)

func TestNaiveModel1500(t *testing.T) {
	const cases = 1500
	rng := rand.New(rand.NewSource(20261003))
	for ci := 0; ci < cases; ci++ {
		runModelCase(t, rng, ci)
	}
}

func errLabel(e error) string {
	switch {
	case e == nil:
		return "ok"
	case errorsIs(e, repl.ErrInvalidArg):
		return "invalid"
	case errorsIs(e, repl.ErrNoRegion):
		return "noRegion"
	case errorsIs(e, repl.ErrBacklog):
		return "backlog"
	case errorsIs(e, repl.ErrNotFound):
		return "notFound"
	default:
		return e.Error()
	}
}

func errorsIs(err, target error) bool {
	type iser interface{ Is(error) bool }
	if err == target {
		return true
	}
	if x, ok := err.(iser); ok {
		return x.Is(target)
	}
	return false
}

// execStep 在真实系统与朴素模型上执行同一操作，返回两边结果标签与判定依据，
// 并把输入/输出写入日志。
func execStep(
	t *testing.T, ci, step int,
	sys *repl.System, model *naiveModel,
	op testOp, bad bool, rng *rand.Rand, logb *strings.Builder,
) (modelLabel, sysLabel, why string) {
	t.Helper()
	switch op.kind {
	case opPut, opDelete:
		del := op.kind == opDelete
		size := op.size
		if del {
			size = 0
		}
		r, key, s, ts := op.r, op.key, size, op.ts
		var sysID, modelID region.VersionID
		var sysErr, modelErr error
		if del {
			sysID, sysErr = sys.Delete(r, key, ts)
		} else {
			sysID, sysErr = sys.Put(r, key, s, ts)
		}
		sysLabel = errLabel(sysErr)
		switch {
		case key == "" || s < 0 || s > 1e9 || ts < 0 || ts > 1e12:
			modelLabel = "invalid"
			why = "arg validation precedes region/backlog; nothing mutates"
		case !model.hasRegion(r):
			modelLabel = "noRegion"
			why = "region lookup precedes backlog; nothing mutates"
		default:
			modelID, modelErr = model.put(r, key, s, ts, del)
			modelLabel = errLabel(modelErr)
			if modelErr == nil {
				why = "created locally; per-link enqueue or relaxed-loss applied"
			} else {
				why = "strict pre-check: every candidate link within budget required, else atomic reject"
			}
		}
		if sysErr == nil && modelErr == nil && sysID != modelID {
			t.Fatalf("case %d step %d id mismatch sys=%v model=%v", ci, step, sysID, modelID)
		}
		verb := "PUT"
		if del {
			verb = "DELETE"
		}
		fmt.Fprintf(logb, "step %d %s r=%s key=%q size=%d ts=%d -> sys=%s model=%s [%s]\n",
			step, verb,
			r, key, s, ts, sysLabel, modelLabel, why)
		return modelLabel, sysLabel, why

	case opDeliver:
		src, dst, n := op.r, op.dst, op.n
		cursor := 0
		send := func(link.Item) (bool, error) {
			resp := op.responses[cursor]
			cursor++
			var e error
			if resp.fail {
				e = link.FailError{}
			}
			return resp.applied, e
		}
		sysN, sysErr := sys.Deliver(src, dst, n, send)
		sysLabel = errLabel(sysErr)
		valid := n >= 1 && n <= 1000 && src != dst && model.hasRegion(src) && model.hasRegion(dst)
		var modelN int
		if !valid {
			modelLabel = "invalid"
			why = "Deliver arg validation: n range, distinct known regions"
		} else {
			modelN = model.deliver(src, dst, op)
			modelLabel = "ok"
			why = "FIFO head-of-line: err blocks until R, then transfer and continue"
		}
		resp := ""
		for _, x := range op.responses[:min2(cursor, len(op.responses))] {
			resp += fmt.Sprintf("(applied=%v,fail=%v) ", x.applied, x.fail)
		}
		fmt.Fprintf(logb, "step %d DELIVER %s->%s n=%d sends(sys)=%d sends(model)=%d resp=[%s] -> sys=%s model=%s [%s]\n",
			step, src, dst, n, sysN, modelN, strings.TrimSpace(resp), sysLabel, modelLabel, why)
		if valid && sysN != modelN {
			t.Fatalf("case %d step %d send count sys=%d model=%d", ci, step, sysN, modelN)
		}
		return modelLabel, sysLabel, why

	case opRetry:
		src, dst := op.r, op.dst
		id := pickRetryID(model, src, dst, rng)
		sysErr := sys.Retry(src, dst, id)
		sysLabel = errLabel(sysErr)
		switch {
		case src == dst || !model.hasRegion(src) || !model.hasRegion(dst) || id.Origin == "" || id.Seq < 1:
			modelLabel = "invalid"
			why = "Retry order: invalid args first"
		default:
			modelErr := model.retry(src, dst, id)
			modelLabel = errLabel(modelErr)
			switch modelLabel {
			case "notFound":
				why = "id absent from failed list"
			case "backlog":
				why = "re-enqueue requires backlog+size<=C"
			default:
				why = "failed item reset and appended to tail"
			}
		}
		fmt.Fprintf(logb, "step %d RETRY %s->%s id=%s -> sys=%s model=%s [%s]\n",
			step, src, dst, fmt.Sprintf("(%s,%d)", id.Origin, id.Seq), sysLabel, modelLabel, why)
		return modelLabel, sysLabel, why
	}
	return modelLabel, sysLabel, why
}

func min2(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// pickRetryID 用同一确定性规则为两边选出尝试重试的标识：
// 优先从失败列表选（保证大量命中 notFound/backlog/ok 三条路径），
// 否则随机给一个不可能的标识。
func pickRetryID(model *naiveModel, src, dst string, rng *rand.Rand) region.VersionID {
	lk := model.links[[2]string{src, dst}]
	if lk != nil && len(lk.failed) > 0 && rng.Intn(100) < 70 {
		ids := make([]region.VersionID, 0, len(lk.failed))
		for id := range lk.failed {
			ids = append(ids, id)
		}
		sort.Slice(ids, func(i, j int) bool {
			if ids[i].Origin != ids[j].Origin {
				return ids[i].Origin < ids[j].Origin
			}
			return ids[i].Seq < ids[j].Seq
		})
		return ids[rng.Intn(len(ids))]
	}
	return region.VersionID{Origin: src, Seq: int64(500 + rng.Intn(500))}
}

func runModelCase(t *testing.T, rng *rand.Rand, ci int) {
	t.Helper()
	nReg := 2 + rng.Intn(3)
	allRegions := []string{"A", "B", "C", "D"}[:nReg]
	p := repl.Params{
		Regions:    append([]string(nil), allRegions...),
		MarkerRepl: rng.Intn(2) == 0,
		Mode:       []repl.Mode{repl.Strict, repl.Relaxed}[rng.Intn(2)],
		C:          int64(1 + rng.Intn(20)),
		R:          1 + rng.Intn(4),
	}
	sys, err := repl.New(p)
	if err != nil {
		t.Fatalf("case %d: New: %v", ci, err)
	}
	model := newNaiveModel(p)

	var logb strings.Builder
	fmt.Fprintf(&logb, "case %d regions=%v marker=%v mode=%v C=%d R=%d\n",
		ci, p.Regions, p.MarkerRepl, p.Mode, p.C, p.R)

	nOps := 30 + rng.Intn(50)
	keys := []string{"k1", "k2", "k3", "k4"}
	for step := 0; step < nOps; step++ {
		op := testOp{
			r:   allRegions[rng.Intn(nReg)],
			dst: allRegions[rng.Intn(nReg)],
			key: keys[rng.Intn(len(keys))],
			ts:  int64(rng.Intn(6)),
		}
		roll := rng.Intn(100)
		bad := false
		switch {
		case roll < 38:
			op.kind = opPut
			op.size = int64(rng.Intn(13))
			if rng.Intn(100) < 8 {
				bad = true
				switch rng.Intn(4) {
				case 0:
					op.key = ""
				case 1:
					op.size = -1
				case 2:
					op.ts = 1e12 + 1
				case 3:
					op.size = 1e9 + 1
				}
			}
		case roll < 52:
			op.kind = opDelete
			if rng.Intn(100) < 8 {
				bad = true
				if rng.Intn(2) == 0 {
					op.key = ""
				} else {
					op.ts = -1
				}
			}
		case roll < 94:
			op.kind = opDeliver
			for op.dst == op.r {
				op.dst = allRegions[rng.Intn(nReg)]
			}
			op.n = 1 + rng.Intn(6)
			op.responses = make([]sendResp, op.n)
			for i := range op.responses {
				op.responses[i] = sendResp{
					applied: rng.Intn(100) < 70,
					fail:    rng.Intn(100) < 35,
				}
			}
			if rng.Intn(100) < 8 {
				bad = true
				switch rng.Intn(3) {
				case 0:
					op.n = 0
				case 1:
					op.n = 1001
				case 2:
					op.dst = op.r
				}
				op.responses = nil
			}
		default:
			op.kind = opRetry
			for op.dst == op.r {
				op.dst = allRegions[rng.Intn(nReg)]
			}
			if rng.Intn(100) < 8 {
				bad = true
				if rng.Intn(2) == 0 {
					op.dst = op.r
				} else {
					op.dst = "Z"
				}
			}
		}

		ml, sl, why := execStep(t, ci, step, sys, model, op, bad, rng, &logb)
		if ml != sl {
			fmt.Fprintf(&logb, "STEP %d MISMATCH model=%s sys=%s (%s)\n", step, ml, sl, why)
			t.Fatalf("case %d step %d result mismatch\n%s", ci, step, logb.String())
		}
		if d := diffSnapshots(sys.Snapshot(), modelSnapshot(model)); d != "" {
			fmt.Fprintf(&logb, "STEP %d SNAPSHOT MISMATCH: %s\n", step, d)
			t.Fatalf("case %d step %d snapshot: %s\n%s", ci, step, d, logb.String())
		}
	}

	for _, key := range keys {
		for _, r := range model.regions {
			got, gerr := sys.Get(r, key)
			mv, mok := model.current(r, key)
			if gerr != nil {
				t.Fatalf("case %d Get(%s,%s): %v\n%s", ci, r, key, gerr, logb.String())
			}
			if got.Exists != mok {
				t.Fatalf("case %d exists mismatch %s/%s sys=%v model=%v\n%s", ci, r, key, got.Exists, mok, logb.String())
			}
			if mok && (got.Deleted != mv.Delete || got.Version.ID != mv.ID) {
				t.Fatalf("case %d current mismatch %s/%s sys=%+v model=%+v\n%s", ci, r, key, got, mv, logb.String())
			}
		}
		if sys.Diverged(key) != model.diverged(key) {
			t.Fatalf("case %d Diverged(%s) sys=%v model=%v\n%s",
				ci, key, sys.Diverged(key), model.diverged(key), logb.String())
		}
	}
	for k, l := range sys.Snapshot().Links {
		if l.Stats.Enqueued != l.Stats.Delivered+l.Stats.Transfers+l.Stats.Queued {
			t.Fatalf("case %d invariant %v: %+v\n%s", ci, k, l.Stats, logb.String())
		}
		var sum int64
		for _, q := range l.Queue {
			sum += q.Ver.Size
		}
		if sum != l.Stats.Backlog {
			t.Fatalf("case %d backlog bytes %v: %d vs %d\n%s", ci, k, sum, l.Stats.Backlog, logb.String())
		}
	}
	if ci < 5 {
		t.Logf("----\n%s", logb.String())
	}
}

type opKind int

const (
	opPut opKind = iota
	opDelete
	opDeliver
	opRetry
)

type testOp struct {
	kind opKind
	r    string
	dst  string
	key  string
	size int64
	ts   int64
	n    int
	// 该次 Deliver 每次 Send 的预生成应答（applied, fail）。
	responses []sendResp
}

type sendResp struct {
	applied bool
	fail    bool
}

type modelVer struct {
	v region.Version
}

type modelItem struct {
	v     region.Version
	tries int
}

type modelLink struct {
	queue     []modelItem
	failed    map[region.VersionID]region.Version
	backlog   int64
	enqueued  int64
	delivered int64
	transfers int64
	lost      int64
	dup       int64
}

type naiveModel struct {
	regions    []string
	mode       repl.Mode
	markerRepl bool
	c          int64
	r          int
	versions   map[string]map[region.VersionID]region.Version
	keys       map[string]map[string]map[region.VersionID]struct{}
	seq        map[string]int64
	links      map[[2]string]*modelLink
}

func newNaiveModel(p repl.Params) *naiveModel {
	m := &naiveModel{
		regions:    append([]string(nil), p.Regions...),
		mode:       p.Mode,
		markerRepl: p.MarkerRepl,
		c:          p.C,
		r:          p.R,
		versions:   map[string]map[region.VersionID]region.Version{},
		keys:       map[string]map[string]map[region.VersionID]struct{}{},
		seq:        map[string]int64{},
		links:      map[[2]string]*modelLink{},
	}
	for _, r := range p.Regions {
		m.versions[r] = map[region.VersionID]region.Version{}
		m.keys[r] = map[string]map[region.VersionID]struct{}{}
	}
	for _, src := range p.Regions {
		for _, dst := range p.Regions {
			if src != dst {
				m.links[[2]string{src, dst}] = &modelLink{
					failed: map[region.VersionID]region.Version{},
				}
			}
		}
	}
	return m
}

func (m *naiveModel) hasRegion(r string) bool {
	_, ok := m.versions[r]
	return ok
}

func (m *naiveModel) put(origin, key string, size, ts int64, del bool) (region.VersionID, error) {
	targets := m.targets(origin, del)
	if m.mode == repl.Strict {
		for _, k := range targets {
			lk := m.links[k]
			if lk.backlog+size > m.c {
				return region.VersionID{}, repl.ErrBacklog
			}
		}
	}
	m.seq[origin]++
	v := region.Version{
		ID:     region.VersionID{Origin: origin, Seq: m.seq[origin]},
		Key:    key,
		Size:   size,
		TS:     ts,
		Delete: del,
	}
	m.versions[origin][v.ID] = v
	set := m.keys[origin][key]
	if set == nil {
		set = map[region.VersionID]struct{}{}
		m.keys[origin][key] = set
	}
	set[v.ID] = struct{}{}
	for _, k := range targets {
		lk := m.links[k]
		if lk.backlog+size <= m.c {
			lk.queue = append(lk.queue, modelItem{v: v})
			lk.backlog += size
			lk.enqueued++
		} else {
			lk.lost++
		}
	}
	return v.ID, nil
}

func (m *naiveModel) targets(origin string, del bool) [][2]string {
	if del && !m.markerRepl {
		return nil
	}
	var out [][2]string
	for _, dst := range m.regions {
		if dst != origin {
			out = append(out, [2]string{origin, dst})
		}
	}
	return out
}

func (m *naiveModel) apply(dst string, v region.Version) bool {
	if _, ok := m.versions[dst][v.ID]; ok {
		return true
	}
	v.Replica = v.ID.Origin != dst
	m.versions[dst][v.ID] = v
	set := m.keys[dst][v.Key]
	if set == nil {
		set = map[region.VersionID]struct{}{}
		m.keys[dst][v.Key] = set
	}
	set[v.ID] = struct{}{}
	return false
}

func (m *naiveModel) deliver(src, dst string, op testOp) int {
	lk := m.links[[2]string{src, dst}]
	sends := 0
	for sends < op.n && sends < len(op.responses) {
		if len(lk.queue) == 0 {
			break
		}
		head := &lk.queue[0]
		resp := op.responses[sends]
		sends++
		if resp.applied || !resp.fail {
			if m.apply(dst, head.v) {
				lk.dup++
			}
		}
		if !resp.fail {
			lk.backlog -= head.v.Size
			lk.delivered++
			lk.queue = lk.queue[1:]
			continue
		}
		head.tries++
		if head.tries < m.r {
			break
		}
		fv := head.v
		lk.queue = lk.queue[1:]
		lk.backlog -= fv.Size
		lk.transfers++
		lk.failed[fv.ID] = fv
	}
	return sends
}

func (m *naiveModel) retry(src, dst string, id region.VersionID) error {
	lk := m.links[[2]string{src, dst}]
	v, ok := lk.failed[id]
	if !ok {
		return repl.ErrNotFound
	}
	if lk.backlog+v.Size > m.c {
		return repl.ErrBacklog
	}
	delete(lk.failed, id)
	lk.queue = append(lk.queue, modelItem{v: v})
	lk.backlog += v.Size
	lk.enqueued++
	return nil
}

func (m *naiveModel) current(r, key string) (region.Version, bool) {
	var cur region.Version
	found := false
	for id := range m.keys[r][key] {
		v := m.versions[r][id]
		if !found {
			cur, found = v, true
			continue
		}
		if v.TS > cur.TS ||
			(v.TS == cur.TS && v.ID.Origin > cur.ID.Origin) ||
			(v.TS == cur.TS && v.ID.Origin == cur.ID.Origin && v.ID.Seq > cur.ID.Seq) {
			cur = v
		}
	}
	return cur, found
}

func (m *naiveModel) diverged(key string) bool {
	var first region.VersionID
	have := false
	for _, r := range m.regions {
		v, ok := m.current(r, key)
		if !ok {
			if have {
				return true
			}
			continue
		}
		if !have {
			first, have = v.ID, true
		} else if v.ID != first {
			return true
		}
	}
	return false
}

func modelSnapshot(m *naiveModel) repl.Snapshot {
	snap := repl.Snapshot{
		Regions:  append([]string(nil), m.regions...),
		Versions: map[string][]region.Version{},
		NextSeq:  map[string]int64{},
		Links:    map[[2]string]repl.LinkSnapshot{},
	}
	for _, r := range m.regions {
		vs := make([]region.Version, 0, len(m.versions[r]))
		for _, v := range m.versions[r] {
			vs = append(vs, v)
		}
		sort.Slice(vs, func(i, j int) bool {
			if vs[i].Key != vs[j].Key {
				return vs[i].Key < vs[j].Key
			}
			if vs[i].ID.Origin != vs[j].ID.Origin {
				return vs[i].ID.Origin < vs[j].ID.Origin
			}
			return vs[i].ID.Seq < vs[j].ID.Seq
		})
		snap.Versions[r] = vs
		snap.NextSeq[r] = m.seq[r] + 1
	}
	for key, lk := range m.links {
		queue := make([]repl.QueueItem, len(lk.queue))
		for i, it := range lk.queue {
			queue[i] = repl.QueueItem{Ver: it.v, Tries: it.tries}
		}
		failed := make([]region.Version, 0, len(lk.failed))
		for _, v := range lk.failed {
			failed = append(failed, v)
		}
		sort.Slice(failed, func(i, j int) bool {
			if failed[i].ID.Origin != failed[j].ID.Origin {
				return failed[i].ID.Origin < failed[j].ID.Origin
			}
			return failed[i].ID.Seq < failed[j].ID.Seq
		})
		snap.Links[key] = repl.LinkSnapshot{
			Stats: repl.LinkStats{
				Enqueued:  lk.enqueued,
				Delivered: lk.delivered,
				Transfers: lk.transfers,
				Queued:    int64(len(lk.queue)),
				Backlog:   lk.backlog,
				Lost:      lk.lost,
				Dup:       lk.dup,
			},
			Queue:  queue,
			Failed: failed,
		}
	}
	return snap
}

func diffSnapshots(a, b repl.Snapshot) string {
	if len(a.Regions) != len(b.Regions) {
		return "region list length differs"
	}
	for i, r := range a.Regions {
		if r != b.Regions[i] {
			return fmt.Sprintf("region order %d: %s vs %s", i, r, b.Regions[i])
		}
		if a.NextSeq[r] != b.NextSeq[r] {
			return fmt.Sprintf("region %s nextSeq %d vs %d", r, a.NextSeq[r], b.NextSeq[r])
		}
		if len(a.Versions[r]) != len(b.Versions[r]) {
			return fmt.Sprintf("region %s versions %d vs %d", r, len(a.Versions[r]), len(b.Versions[r]))
		}
		for i := range a.Versions[r] {
			if a.Versions[r][i] != b.Versions[r][i] {
				return fmt.Sprintf("region %s version[%d] %+v vs %+v", r, i, a.Versions[r][i], b.Versions[r][i])
			}
		}
	}
	for k, la := range a.Links {
		lb, ok := b.Links[k]
		if !ok {
			return fmt.Sprintf("link %v missing in model snapshot", k)
		}
		if la.Stats != lb.Stats {
			return fmt.Sprintf("link %v stats %+v vs %+v", k, la.Stats, lb.Stats)
		}
		if len(la.Queue) != len(lb.Queue) {
			return fmt.Sprintf("link %v queue len %d vs %d", k, len(la.Queue), len(lb.Queue))
		}
		for i := range la.Queue {
			if la.Queue[i] != lb.Queue[i] {
				return fmt.Sprintf("link %v queue[%d] %+v vs %+v", k, i, la.Queue[i], lb.Queue[i])
			}
		}
		if len(la.Failed) != len(lb.Failed) {
			return fmt.Sprintf("link %v failed len %d vs %d", k, len(la.Failed), len(lb.Failed))
		}
		for i := range la.Failed {
			if la.Failed[i] != lb.Failed[i] {
				return fmt.Sprintf("link %v failed[%d] %+v vs %+v", k, i, la.Failed[i], lb.Failed[i])
			}
		}
	}
	return ""
}

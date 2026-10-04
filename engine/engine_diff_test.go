package engine

// 1500 组随机序列差分测试：在序列的每个位置插入 Crash、Recover，
// 引擎结果与按题述规则写成的逐步朴素模型逐项对照。

import (
	"errors"
	"fmt"
	"math/rand"
	"sort"
	"strings"
	"testing"
)

type modelOpKind int

const (
	opIndex modelOpKind = iota
	opDelete
	opRefresh
	opSync
	opFlush
	opSetRequest
	opSetAsync
	opCrash
	opRecover
)

type modelOp struct {
	kind  modelOpKind
	id    string
	body  string
	ifSeq int64
	seq   int64
}

type modelDoc struct {
	body    string
	seq     int64
	deleted bool
}

// naiveModel 按题述规则朴素实现：ops 保存全部已落盘写操作（seq 1..synced），
// commitPoint 为提交点，versions/view 为易失状态。
type naiveModel struct {
	durability  Durability
	crashed     bool
	ops         []modelOp // 当前代全部已接受写操作
	syncedN     int       // ops 中前 syncedN 条已落盘
	commit      int64     // 提交点 seq
	maxSeq      int64
	versions    map[string]modelDoc
	commitPoint map[string]modelDoc
	view        map[string]modelDoc // 搜索视图存活文档
}

func newNaiveModel(durability Durability) *naiveModel {
	return &naiveModel{
		durability:  durability,
		versions:    map[string]modelDoc{},
		commitPoint: map[string]modelDoc{},
		view:        map[string]modelDoc{},
	}
}

func validModelID(id string) bool { return len(id) >= 1 && len(id) <= 512 }

// live 返回实时状态：(doc, true) 存活；(_, false) 不存活。
func (m *naiveModel) live(id string) (modelDoc, bool) {
	if v, ok := m.versions[id]; ok {
		return modelDoc{body: v.body, seq: v.seq}, !v.deleted
	}
	if d, ok := m.view[id]; ok {
		return d, true
	}
	return modelDoc{}, false
}

func (m *naiveModel) synced() int64 { return int64(len(m.ops)) }

type modelResult struct {
	seq int64
	err error
	n   int // Recover 重放条数
}

func errCode(err error) error {
	if err == nil {
		return nil
	}
	for _, target := range []error{ErrInvalidArgument, ErrNotRecovered, ErrInvalidState, ErrConflict, ErrNotFound} {
		if errors.Is(err, target) {
			return target
		}
	}
	return nil
}

func sameErr(a, b error) bool { return errCode(a) == errCode(b) }

func (m *naiveModel) apply(op modelOp) modelResult {
	switch op.kind {
	case opIndex:
		if !validModelID(op.id) || len(op.body) > 65536 || (op.ifSeq != -1 && op.ifSeq < 1) {
			return modelResult{err: ErrInvalidArgument}
		}
		if m.crashed {
			return modelResult{err: ErrNotRecovered}
		}
		if op.ifSeq != -1 {
			cur, ok := m.live(op.id)
			if !ok || cur.seq != op.ifSeq {
				return modelResult{err: ErrConflict}
			}
		}
		m.maxSeq++
		seq := m.maxSeq
		op.seq = seq
		m.versions[op.id] = modelDoc{body: op.body, seq: seq}
		if m.durability == Request {
			m.ops = append(m.ops, op)
			m.syncedN = len(m.ops)
		} else {
			m.ops = append(m.ops, op)
		}
		return modelResult{seq: seq}
	case opDelete:
		if !validModelID(op.id) {
			return modelResult{err: ErrInvalidArgument}
		}
		if m.crashed {
			return modelResult{err: ErrNotRecovered}
		}
		if _, ok := m.live(op.id); !ok {
			return modelResult{err: ErrNotFound}
		}
		m.maxSeq++
		seq := m.maxSeq
		op.seq = seq
		m.versions[op.id] = modelDoc{seq: seq, deleted: true}
		if m.durability == Request {
			m.ops = append(m.ops, op)
			m.syncedN = len(m.ops)
		} else {
			m.ops = append(m.ops, op)
		}
		return modelResult{seq: seq}
	case opRefresh:
		if m.crashed {
			return modelResult{err: ErrNotRecovered}
		}
		m.refresh()
		return modelResult{}
	case opSync:
		if m.crashed {
			return modelResult{err: ErrNotRecovered}
		}
		m.syncedN = len(m.ops)
		return modelResult{}
	case opFlush:
		if m.crashed {
			return modelResult{err: ErrNotRecovered}
		}
		m.refresh()
		m.commitPoint = map[string]modelDoc{}
		for id, d := range m.view {
			m.commitPoint[id] = d
		}
		m.commit = m.maxSeq
		m.syncedN = 0
		m.ops = nil
		return modelResult{}
	case opSetRequest:
		if m.crashed {
			return modelResult{err: ErrNotRecovered}
		}
		m.durability = Request
		m.syncedN = len(m.ops)
		return modelResult{}
	case opSetAsync:
		if m.crashed {
			return modelResult{err: ErrNotRecovered}
		}
		m.durability = Async
		return modelResult{}
	case opCrash:
		if m.crashed {
			return modelResult{err: ErrNotRecovered}
		}
		m.ops = m.ops[:m.syncedN]
		m.versions = map[string]modelDoc{}
		m.view = nil
		m.maxSeq = m.commit + int64(len(m.ops))
		m.crashed = true
		return modelResult{}
	case opRecover:
		if !m.crashed {
			return modelResult{err: ErrInvalidState}
		}
		m.view = map[string]modelDoc{}
		for id, d := range m.commitPoint {
			m.view[id] = d
		}
		m.versions = map[string]modelDoc{}
		for _, w := range m.ops {
			switch w.kind {
			case opIndex:
				m.versions[w.id] = modelDoc{body: w.body, seq: w.seq}
			case opDelete:
				m.versions[w.id] = modelDoc{seq: w.seq, deleted: true}
			}
		}
		m.refresh()
		m.maxSeq = m.commit + int64(len(m.ops))
		m.crashed = false
		return modelResult{n: len(m.ops)}
	}
	return modelResult{}
}

func (m *naiveModel) refresh() {
	for id, v := range m.versions {
		if v.deleted {
			delete(m.view, id)
		} else {
			m.view[id] = modelDoc{body: v.body, seq: v.seq}
		}
	}
	m.versions = map[string]modelDoc{}
}

func (m *naiveModel) get(id string) (modelDoc, bool, error) {
	if !validModelID(id) {
		return modelDoc{}, false, ErrInvalidArgument
	}
	if m.crashed {
		return modelDoc{}, false, ErrNotRecovered
	}
	if v, ok := m.versions[id]; ok {
		if v.deleted {
			return modelDoc{}, false, ErrNotFound
		}
		return modelDoc{body: v.body, seq: v.seq}, true, nil
	}
	if d, ok := m.view[id]; ok {
		return d, true, nil
	}
	return modelDoc{}, false, ErrNotFound
}

type modelKV struct {
	id string
	d  modelDoc
}

func (m *naiveModel) search() ([]modelKV, error) {
	if m.crashed {
		return nil, ErrNotRecovered
	}
	ids := make([]string, 0, len(m.view))
	for id := range m.view {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	out := make([]modelKV, 0, len(ids))
	for _, id := range ids {
		out = append(out, modelKV{id, m.view[id]})
	}
	return out, nil
}

func (m *naiveModel) syncedSeq() int64 { return m.commit + int64(m.syncedN) }

// engineSide 用真实引擎执行同一操作。
func execEngine(t *testing.T, e *Engine, op modelOp) modelResult {
	t.Helper()
	switch op.kind {
	case opIndex:
		seq, err := e.Index(op.id, []byte(op.body), op.ifSeq)
		return modelResult{seq: seq, err: err}
	case opDelete:
		seq, err := e.Delete(op.id)
		return modelResult{seq: seq, err: err}
	case opRefresh:
		return modelResult{err: e.Refresh()}
	case opSync:
		return modelResult{err: e.Sync()}
	case opFlush:
		return modelResult{err: e.Flush()}
	case opSetRequest:
		return modelResult{err: e.SetDurability(Request)}
	case opSetAsync:
		return modelResult{err: e.SetDurability(Async)}
	case opCrash:
		return modelResult{err: e.Crash()}
	case opRecover:
		n, err := e.Recover()
		return modelResult{err: err, n: n}
	}
	return modelResult{}
}

func opName(k modelOpKind) string {
	names := []string{"Index", "Delete", "Refresh", "Sync", "Flush",
		"SetRequest", "SetAsync", "Crash", "Recover"}
	return names[k]
}

func describeOp(op modelOp) string {
	switch op.kind {
	case opIndex:
		return fmt.Sprintf("Index(%q,%q,ifSeq=%d)", op.id, op.body, op.ifSeq)
	case opDelete:
		return fmt.Sprintf("Delete(%q)", op.id)
	default:
		return opName(op.kind)
	}
}

func describeResult(r modelResult) string {
	var b strings.Builder
	if r.err != nil {
		fmt.Fprintf(&b, "err=%v", errCode(r.err))
	}
	if r.seq != 0 || r.err == nil && r.n == 0 && r.seq == 0 {
		if r.seq != 0 {
			fmt.Fprintf(&b, " seq=%d", r.seq)
		}
	}
	if r.n != 0 {
		fmt.Fprintf(&b, " replayed=%d", r.n)
	}
	return b.String()
}

// snapshotState 读出引擎的全部可观察状态，供与模型对照。
func snapshotState(t *testing.T, e *Engine) string {
	t.Helper()
	var b strings.Builder
	fmt.Fprintf(&b, "max=%d synced=%d committed=%d uncommitted=%d",
		e.MaxSeq(), e.Synced(), e.Committed(), e.Uncommitted())
	docs, err := e.Search()
	if err != nil {
		fmt.Fprintf(&b, " searchErr=%v", errCode(err))
	} else {
		parts := make([]string, 0, len(docs))
		for _, d := range docs {
			parts = append(parts, fmt.Sprintf("%s@%d=%s", d.ID, d.Seq, d.Body))
		}
		fmt.Fprintf(&b, " search=[%s]", strings.Join(parts, ","))
	}
	return b.String()
}

func modelState(m *naiveModel) string {
	var b strings.Builder
	fmt.Fprintf(&b, "max=%d synced=%d committed=%d uncommitted=%d",
		m.maxSeq, m.syncedSeq(), m.commit, int64(len(m.ops)))
	docs, err := m.search()
	if err != nil {
		fmt.Fprintf(&b, " searchErr=%v", errCode(err))
	} else {
		parts := make([]string, 0, len(docs))
		for _, kv := range docs {
			parts = append(parts, fmt.Sprintf("%s@%d=%s", kv.id, kv.d.seq, kv.d.body))
		}
		fmt.Fprintf(&b, " search=[%s]", strings.Join(parts, ","))
	}
	return b.String()
}

func generateOps(r *rand.Rand, maxLen int) []modelOp {
	n := 5 + r.Intn(maxLen)
	ids := []string{"a", "b", "c", "d", "A", "ab", "a\xff", "x"}
	ops := make([]modelOp, 0, n)
	for i := 0; i < n; i++ {
		k := modelOpKind(r.Intn(int(opRecover) + 1))
		op := modelOp{kind: k}
		switch k {
		case opIndex:
			op.id = ids[r.Intn(len(ids))]
			op.body = fmt.Sprintf("v%d", r.Intn(4))
			switch r.Intn(3) {
			case 0:
				op.ifSeq = -1
			case 1:
				op.ifSeq = int64(r.Intn(4)) // 含 0：参数非法
			default:
				op.ifSeq = int64(1 + r.Intn(6))
			}
		case opDelete:
			op.id = ids[r.Intn(len(ids))]
		}
		ops = append(ops, op)
	}
	return ops
}

// runPrefix 在引擎与模型上按序执行 ops[:cut]，逐步对照结果与可观察状态。
func runPrefix(t *testing.T, rngSeed int64, ops []modelOp, cut int, dur Durability) (engineErr error) {
	t.Helper()
	e := New(dur)
	m := newNaiveModel(dur)
	var log strings.Builder
	fmt.Fprintf(&log, "seed=%d durability=%v cut=%d\n", rngSeed, dur, cut)
	fail := func(at int, msg string) {
		t.Fatalf("差分不一致 @step=%d (%s)\n%s\n%s", at, msg, log.String(), msg)
	}
	for i := 0; i < cut; i++ {
		op := ops[i]
		er := execEngine(t, e, op)
		mr := m.apply(op)
		fmt.Fprintf(&log, "step %d: %s -> engine{%s} model{%s}\n",
			i, describeOp(op), describeResult(er), describeResult(mr))
		if !sameErr(er.err, mr.err) {
			fail(i, fmt.Sprintf("错误不一致 engine=%v model=%v", er.err, mr.err))
		}
		if er.err == nil {
			if er.seq != mr.seq || er.n != mr.n {
				fail(i, fmt.Sprintf("输出不一致 engine={%s} model={%s}",
					describeResult(er), describeResult(mr)))
			}
		}
		es, ms := snapshotState(t, e), modelState(m)
		fmt.Fprintf(&log, "         engine state: %s\n         model  state: %s\n", es, ms)
		if es != ms {
			fail(i, "状态不一致")
		}
		// 实时 Get 逐个 id 对照（引擎未崩溃时）
		if !m.crashed {
			for _, id := range []string{"a", "b", "c", "d", "A", "ab", "a\xff", "x", "missing"} {
				ed, eerr := e.Get(id)
				md, mok, merr := m.get(id)
				if !sameErr(eerr, merr) {
					fail(i, fmt.Sprintf("Get(%s) 错误不一致 engine=%v model=%v", id, eerr, merr))
				}
				if eerr == nil {
					if !mok || ed.Seq != md.seq || string(ed.Body) != md.body {
						fail(i, fmt.Sprintf("Get(%s) 数据不一致 engine=%+v model=%+v", id, ed, md))
					}
				}
			}
		}
	}
	return nil
}

// runCrashRecover 从 cut 处插入 Crash、Recover，对照重放结果、水位、搜索与实时状态，
// 并再做一轮 Crash、Recover 验证幂等。
func runCrashRecover(t *testing.T, seed int64, ops []modelOp, cut int, dur Durability) {
	t.Helper()
	e := New(dur)
	m := newNaiveModel(dur)
	for i := 0; i < cut; i++ {
		execEngine(t, e, ops[i])
		m.apply(ops[i])
	}
	var log strings.Builder
	fmt.Fprintf(&log, "seed=%d durability=%v cut=%d 前缀状态 engine: %s\n model: %s\n",
		seed, dur, cut, snapshotState(t, e), modelState(m))

	// 若当前已处于崩溃态（前缀最后一个操作是 Crash），先 Recover 再开始本轮对比；
	// 为保证两侧起点一致，直接要求 cut 前缀结束于非崩溃态：若崩溃则先各做一次恢复。
	if m.crashed {
		er := execEngine(t, e, modelOp{kind: opRecover})
		mr := m.apply(modelOp{kind: opRecover})
		fmt.Fprintf(&log, "前缀已崩溃，先 Recover: engine replayed=%d model=%d\n", er.n, mr.n)
		if er.n != mr.n {
			t.Fatalf("前置恢复条数不一致\n%s", log.String())
		}
	}

	checkStates := func(stage string) {
		es, ms := snapshotState(t, e), modelState(m)
		fmt.Fprintf(&log, "[%s] engine: %s\n[%s] model:  %s\n", stage, es, stage, ms)
		if es != ms {
			t.Fatalf("[%s] 状态不一致\n%s", stage, log.String())
		}
		for _, id := range []string{"a", "b", "c", "d", "A", "ab", "a\xff", "x"} {
			ed, eerr := e.Get(id)
			md, _, merr := m.get(id)
			if !sameErr(eerr, merr) {
				t.Fatalf("[%s] Get(%s) 错误不一致 engine=%v model=%v\n%s",
					stage, id, eerr, merr, log.String())
			}
			if eerr == nil && (ed.Seq != md.seq || string(ed.Body) != md.body) {
				t.Fatalf("[%s] Get(%s) 数据不一致 engine=%+v model=%+v\n%s",
					stage, id, ed, md, log.String())
			}
		}
	}

	// 第一轮 Crash/Recover
	cer := execEngine(t, e, modelOp{kind: opCrash})
	cmr := m.apply(modelOp{kind: opCrash})
	if !sameErr(cer.err, cmr.err) {
		t.Fatalf("Crash 错误不一致\n%s", log.String())
	}
	rer := execEngine(t, e, modelOp{kind: opRecover})
	rmr := m.apply(modelOp{kind: opRecover})
	fmt.Fprintf(&log, "Recover#1 engine replayed=%d model replayed=%d (synced-committed=%d)\n",
		rer.n, rmr.n, e.Synced()-e.Committed())
	if rer.err != nil || rmr.err != nil || rer.n != rmr.n {
		t.Fatalf("Recover#1 不一致 engine=%+v model=%+v\n%s", rer, rmr, log.String())
	}
	if rer.n != int(e.Synced()-e.Committed()) {
		t.Fatalf("重放条数不等于 synced-committed: n=%d delta=%d\n%s",
			rer.n, e.Synced()-e.Committed(), log.String())
	}
	checkStates("after recover#1")

	// 第二轮 Crash/Recover：必须幂等
	execEngine(t, e, modelOp{kind: opCrash})
	m.apply(modelOp{kind: opCrash})
	rer2 := execEngine(t, e, modelOp{kind: opRecover})
	rmr2 := m.apply(modelOp{kind: opRecover})
	if rer2.n != rer.n || rmr2.n != rmr.n {
		t.Fatalf("Recover#2 条数变化 engine=%d->%d model=%d->%d\n%s",
			rer.n, rer2.n, rmr.n, rmr2.n, log.String())
	}
	checkStates("after recover#2")

	// 恢复后序号重用：再写一条，检查序号为 maxSeq+1
	probe := modelOp{kind: opIndex, id: "probe", body: "p", ifSeq: -1}
	per := execEngine(t, e, probe)
	pmr := m.apply(probe)
	if per.err != nil || per.seq != pmr.seq {
		t.Fatalf("恢复后写入不一致 engine=%+v model=%+v\n%s", per, pmr, log.String())
	}
	checkStates("after probe")
}

func TestRandomDifferential(t *testing.T) {
	if testing.Verbose() {
		t.Log("差分判定依据：每步比对错误类型、返回 seq/重放数、水位四元组、Search 全量与逐 id Get；")
		t.Log("输入为带种子的伪随机序列，在 cut 处插入两轮 Crash/Recover 验证幂等与序号重用。")
	}
	const groups = 1500
	for g := 0; g < groups; g++ {
		seed := int64(1000 + g)
		r := rand.New(rand.NewSource(seed))
		dur := Async
		if g%2 == 1 {
			dur = Request
		}
		ops := generateOps(r, 20)
		// 1) 全序列逐步对照（覆盖正常路径与序列内部自带的崩溃恢复）
		runPrefix(t, seed, ops, len(ops), dur)
		// 2) 在每个位置 cut 插入 Crash/Recover 对照
		for cut := 0; cut <= len(ops); cut++ {
			runCrashRecover(t, seed, ops, cut, dur)
		}
		if testing.Verbose() && g%300 == 0 {
			t.Logf("group %d/%d seed=%d dur=%v ops=%d OK", g, groups, seed, dur, len(ops))
		}
	}
}

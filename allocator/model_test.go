package allocator

import (
	"errors"
	"fmt"
	"math/rand"
	"sort"
	"strings"
	"testing"
)

// naive 是按规格逐条写成的朴素模拟：全量扫描、无索引，
// 作为分配器的独立对照实现。
type naive struct {
	r, a   int
	splits map[int64]*nsplit
	alive  []bool
	lt     int64
	done   int64
	sealed bool
}

type nsplit struct {
	state  State
	owner  int
	at, ft int64
	ret    int
}

func newNaive(r, a int) *naive {
	n := &naive{r: r, a: a, splits: map[int64]*nsplit{}, alive: make([]bool, r)}
	for i := range n.alive {
		n.alive[i] = true
	}
	return n
}

func (n *naive) addSplits(ids []int64) error {
	if len(ids) < 1 || len(ids) > 1000 {
		return ErrInvalidArgument
	}
	for _, id := range ids {
		if id < 0 || id > maxSplitID {
			return ErrInvalidArgument
		}
	}
	if n.sealed {
		return ErrSealed
	}
	batch := map[int64]bool{}
	for _, id := range ids {
		if batch[id] {
			return ErrDuplicateSplit
		}
		if _, ok := n.splits[id]; ok {
			return ErrDuplicateSplit
		}
		batch[id] = true
	}
	for _, id := range ids {
		n.splits[id] = &nsplit{state: StateUnassigned, owner: -1}
	}
	return nil
}

func (n *naive) seal() { n.sealed = true }

func (n *naive) checkpoint(cp int64) error {
	if cp != n.lt+1 {
		return ErrCheckpointOutOfOrder
	}
	n.lt = cp
	return nil
}

func (n *naive) complete(cp int64) error {
	if cp < 1 {
		return ErrInvalidArgument
	}
	if cp > n.lt {
		return ErrCompleteAhead
	}
	if cp <= n.done {
		return ErrCompleteStale
	}
	n.done = cp
	return nil
}

func (n *naive) request(r int) (Reply, error) {
	if r < 0 || r >= n.r {
		return Reply{}, ErrInvalidArgument
	}
	if !n.alive[r] {
		return Reply{}, ErrReaderFailed
	}
	e := n.lt + 1
	best := int64(-1)
	for id, sp := range n.splits {
		if sp.state == StateUnassigned && int(id%int64(n.r)) == r && (best < 0 || id < best) {
			best = id
		}
	}
	if best < 0 {
		for id, sp := range n.splits {
			if sp.state == StateUnassigned && !n.alive[int(id%int64(n.r))] && (best < 0 || id < best) {
				best = id
			}
		}
	}
	if best >= 0 {
		sp := n.splits[best]
		sp.state = StateAssigned
		sp.owner = r
		sp.at = e
		return Reply{Kind: ReplyAssigned, Split: best}, nil
	}
	hasUnassigned, hasAssigned := false, false
	for _, sp := range n.splits {
		if sp.state == StateUnassigned {
			hasUnassigned = true
		}
		if sp.state == StateAssigned {
			hasAssigned = true
		}
	}
	if n.sealed && !hasUnassigned && !hasAssigned {
		return Reply{Kind: ReplyNoMore}, nil
	}
	return Reply{Kind: ReplyWait}, nil
}

func (n *naive) finish(r int, s int64) error {
	if r < 0 || r >= n.r || s < 0 || s > maxSplitID {
		return ErrInvalidArgument
	}
	if !n.alive[r] {
		return ErrReaderFailed
	}
	sp, ok := n.splits[s]
	if !ok || sp.state != StateAssigned || sp.owner != r {
		return ErrNotAssignedToReader
	}
	sp.state = StateFinished
	sp.ft = n.lt + 1
	return nil
}

func (n *naive) fail(r int) (Failure, error) {
	res := Failure{Returned: []int64{}, Quarantined: []int64{}, Revoked: []int64{}}
	if r < 0 || r >= n.r {
		return res, ErrInvalidArgument
	}
	if !n.alive[r] {
		return res, ErrReaderFailed
	}
	n.alive[r] = false
	var ids []int64
	for id, sp := range n.splits {
		if sp.owner == r && (sp.state == StateAssigned || sp.state == StateFinished) {
			ids = append(ids, id)
		}
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	for _, id := range ids {
		sp := n.splits[id]
		if sp.at > n.done {
			sp.owner = -1
			sp.ret++
			if sp.ret >= n.a {
				sp.state = StateQuarantined
				res.Quarantined = append(res.Quarantined, id)
			} else {
				sp.state = StateUnassigned
				res.Returned = append(res.Returned, id)
			}
		} else if sp.state == StateFinished && sp.ft > n.done {
			sp.state = StateAssigned
			res.Revoked = append(res.Revoked, id)
		}
	}
	return res, nil
}

func (n *naive) restart(r int) error {
	if r < 0 || r >= n.r {
		return ErrInvalidArgument
	}
	if n.alive[r] {
		return ErrReaderNotFailed
	}
	n.alive[r] = true
	return nil
}

func (n *naive) state(s int64) (SplitInfo, bool) {
	sp, ok := n.splits[s]
	if !ok {
		return SplitInfo{}, false
	}
	return SplitInfo{State: sp.state, Owner: sp.owner, Ret: sp.ret}, true
}

func (n *naive) quarantined() []int64 {
	out := []int64{}
	for id, sp := range n.splits {
		if sp.state == StateQuarantined {
			out = append(out, id)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func (n *naive) counts() Counts {
	var c Counts
	for _, sp := range n.splits {
		switch sp.state {
		case StateUnassigned:
			c.Unassigned++
		case StateAssigned:
			c.Assigned++
		case StateFinished:
			c.Finished++
		case StateQuarantined:
			c.Quarantined++
		}
	}
	return c
}

// ---- 随机操作序列对照 ----

type opKind int

const (
	opAdd opKind = iota
	opSeal
	opCheckpoint
	opComplete
	opRequest
	opFinish
	opFail
	opRestart
	opState
	opCounts
	opQuarantined
)

type op struct {
	kind opKind
	r    int
	s    int64
	ids  []int64
	cp   int64
}

func (o op) String() string {
	switch o.kind {
	case opAdd:
		return fmt.Sprintf("AddSplits(%v)", o.ids)
	case opSeal:
		return "Seal()"
	case opCheckpoint:
		return fmt.Sprintf("Checkpoint(%d)", o.cp)
	case opComplete:
		return fmt.Sprintf("Complete(%d)", o.cp)
	case opRequest:
		return fmt.Sprintf("RequestSplit(%d)", o.r)
	case opFinish:
		return fmt.Sprintf("Finished(%d, %d)", o.r, o.s)
	case opFail:
		return fmt.Sprintf("ReaderFailed(%d)", o.r)
	case opRestart:
		return fmt.Sprintf("ReaderRestarted(%d)", o.r)
	case opState:
		return fmt.Sprintf("State(%d)", o.s)
	case opCounts:
		return "Counts()"
	case opQuarantined:
		return "Quarantined()"
	}
	return "?"
}

func errTag(err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, ErrInvalidArgument):
		return "invalid-argument"
	case errors.Is(err, ErrSealed):
		return "sealed"
	case errors.Is(err, ErrDuplicateSplit):
		return "duplicate-split"
	case errors.Is(err, ErrReaderFailed):
		return "reader-failed"
	case errors.Is(err, ErrReaderNotFailed):
		return "reader-not-failed"
	case errors.Is(err, ErrNotAssignedToReader):
		return "not-assigned-to-reader"
	case errors.Is(err, ErrCheckpointOutOfOrder):
		return "checkpoint-out-of-order"
	case errors.Is(err, ErrCompleteAhead):
		return "complete-ahead"
	case errors.Is(err, ErrCompleteStale):
		return "complete-stale"
	}
	return "unknown"
}

func errRes(err error) string {
	if err == nil {
		return "OK"
	}
	return "ERR:" + errTag(err)
}

func applyReal(a *Allocator, o op) string {
	switch o.kind {
	case opAdd:
		return errRes(a.AddSplits(o.ids))
	case opSeal:
		a.Seal()
		return "OK"
	case opCheckpoint:
		return errRes(a.Checkpoint(o.cp))
	case opComplete:
		return errRes(a.Complete(o.cp))
	case opRequest:
		rep, err := a.RequestSplit(o.r)
		if err != nil {
			return "ERR:" + errTag(err)
		}
		return fmt.Sprintf("%v:%d", rep.Kind, rep.Split)
	case opFinish:
		return errRes(a.Finished(o.r, o.s))
	case opFail:
		f, err := a.ReaderFailed(o.r)
		if err != nil {
			return "ERR:" + errTag(err)
		}
		return fmt.Sprintf("R=%v Q=%v V=%v", f.Returned, f.Quarantined, f.Revoked)
	case opRestart:
		return errRes(a.ReaderRestarted(o.r))
	case opState:
		info, ok := a.State(o.s)
		if !ok {
			return "NONE"
		}
		return fmt.Sprintf("%v,%d,%d", info.State, info.Owner, info.Ret)
	case opCounts:
		return fmt.Sprintf("%v", a.Counts())
	case opQuarantined:
		return fmt.Sprintf("%v", a.Quarantined())
	}
	return "?"
}

func applyModel(n *naive, o op) string {
	switch o.kind {
	case opAdd:
		return errRes(n.addSplits(o.ids))
	case opSeal:
		n.seal()
		return "OK"
	case opCheckpoint:
		return errRes(n.checkpoint(o.cp))
	case opComplete:
		return errRes(n.complete(o.cp))
	case opRequest:
		rep, err := n.request(o.r)
		if err != nil {
			return "ERR:" + errTag(err)
		}
		return fmt.Sprintf("%v:%d", rep.Kind, rep.Split)
	case opFinish:
		return errRes(n.finish(o.r, o.s))
	case opFail:
		f, err := n.fail(o.r)
		if err != nil {
			return "ERR:" + errTag(err)
		}
		return fmt.Sprintf("R=%v Q=%v V=%v", f.Returned, f.Quarantined, f.Revoked)
	case opRestart:
		return errRes(n.restart(o.r))
	case opState:
		info, ok := n.state(o.s)
		if !ok {
			return "NONE"
		}
		return fmt.Sprintf("%v,%d,%d", info.State, info.Owner, info.Ret)
	case opCounts:
		return fmt.Sprintf("%v", n.counts())
	case opQuarantined:
		return fmt.Sprintf("%v", n.quarantined())
	}
	return "?"
}

// TestRandomAgainstModel 用 2000 组随机操作序列对照朴素模拟。
// 日志打印每组的输入（操作序列）、输出（返回值）与判定依据
// （与朴素模拟逐步一致 + 重放一致）。
func TestRandomAgainstModel(t *testing.T) {
	const sequences = 2000
	coverage := map[string]int{}
	for seq := 0; seq < sequences; seq++ {
		rng := rand.New(rand.NewSource(int64(seq)*1000003 + 17))
		r := 1 + rng.Intn(8)
		qa := 1 + rng.Intn(4)
		real, err := New(r, qa)
		if err != nil {
			t.Fatal(err)
		}
		model := newNaive(r, qa)

		var (
			trace   []string
			ops     []op
			results []string
			nextID  int64
		)
		steps := 40 + rng.Intn(40)
		fail := func(format string, args ...any) {
			t.Logf("sequence %d: R=%d A=%d", seq, r, qa)
			for _, line := range trace {
				t.Log(line)
			}
			t.Fatalf(format, args...)
		}

		for step := 0; step < steps; step++ {
			o := genOp(rng, model, &nextID)
			ops = append(ops, o)
			got := applyReal(real, o)
			want := applyModel(model, o)
			results = append(results, got)
			classify(o, got, coverage)
			trace = append(trace, fmt.Sprintf("  #%03d %-24s -> %s", step, o, got))
			if got != want {
				fail("step %d op %s: real=%q model=%q", step, o, got, want)
			}
		}

		// 判定依据一：与朴素模拟逐步一致（上面已逐步断言）。
		// 判定依据二：相同操作序列在全新实例上重放，返回值完全一致。
		if seq%10 == 0 {
			replay, err := New(r, qa)
			if err != nil {
				t.Fatal(err)
			}
			for i, o := range ops {
				if again := applyReal(replay, o); again != results[i] {
					fail("replay step %d op %s: replay=%q first=%q", i, o, again, results[i])
				}
			}
		}

		c := real.Counts()
		t.Logf("seq=%d R=%d A=%d ops=%d final=%v quar=%v 判定: 逐步与朴素模拟一致",
			seq, r, qa, steps, c, real.Quarantined())
		_ = c
		if seq < 2 {
			for _, line := range trace {
				t.Log(line)
			}
		}
	}
	t.Logf("结果类别覆盖: %v", coverage)
	// 关键路径必须被随机序列真实命中。
	for _, key := range []string{
		"assigned", "wait", "no-more",
		"returned", "quarantined", "revoked",
		"err:reader-failed", "err:duplicate-split", "err:sealed",
		"err:checkpoint-out-of-order", "err:complete-ahead", "err:complete-stale",
		"err:not-assigned-to-reader", "err:reader-not-failed", "err:invalid-argument",
	} {
		if coverage[key] == 0 {
			t.Errorf("关键路径 %q 未被 2000 组随机序列覆盖", key)
		}
	}
}

// classify 按操作结果归类，用于覆盖统计。
func classify(o op, res string, cov map[string]int) {
	if len(res) >= 4 && res[:4] == "ERR:" {
		cov["err:"+res[4:]]++
		return
	}
	switch o.kind {
	case opRequest:
		switch {
		case len(res) >= 8 && res[:8] == "ASSIGNED":
			cov["assigned"]++
		case res == "WAIT:0":
			cov["wait"]++
		case res == "NO_MORE:0":
			cov["no-more"]++
		}
	case opFail:
		parts := strings.Split(res, " ")
		if len(parts) == 3 {
			if parts[0] != "R=[]" {
				cov["returned"]++
			}
			if parts[1] != "Q=[]" {
				cov["quarantined"]++
			}
			if parts[2] != "V=[]" {
				cov["revoked"]++
			}
		}
	}
}

// genOp 依据朴素模拟的当前状态生成一个随机操作（含合法与非法参数）。
func genOp(rng *rand.Rand, m *naive, nextID *int64) op {
	roll := rng.Intn(100)
	switch {
	case roll < 14: // AddSplits
		var ids []int64
		size := 1 + rng.Intn(5)
		switch rng.Intn(20) {
		case 0:
			size = 0 // 非法：空列表
		case 1:
			size = 1001 // 非法：超长
		}
		for i := 0; i < size; i++ {
			switch rng.Intn(20) {
			case 0:
				ids = append(ids, -1) // 非法编号
			case 1:
				ids = append(ids, maxSplitID+1) // 非法编号
			case 2:
				ids = append(ids, maxSplitID) // 边界合法编号
			case 3, 4:
				if *nextID > 0 { // 与已登记编号重复
					ids = append(ids, rng.Int63n(*nextID))
				} else {
					ids = append(ids, *nextID)
					*nextID++
				}
			case 5:
				if len(ids) > 0 { // 列表内重复
					ids = append(ids, ids[0])
				} else {
					ids = append(ids, *nextID)
					*nextID++
				}
			default:
				ids = append(ids, *nextID)
				*nextID++
			}
		}
		return op{kind: opAdd, ids: ids}
	case roll < 17:
		return op{kind: opSeal}
	case roll < 27: // Checkpoint
		switch rng.Intn(10) {
		case 0:
			return op{kind: opCheckpoint, cp: m.lt} // 乱序
		case 1:
			return op{kind: opCheckpoint, cp: m.lt + 2} // 乱序
		case 2:
			return op{kind: opCheckpoint, cp: 0} // 乱序
		default:
			return op{kind: opCheckpoint, cp: m.lt + 1}
		}
	case roll < 35: // Complete
		switch rng.Intn(10) {
		case 0:
			return op{kind: opComplete, cp: 0} // 非法
		case 1:
			return op{kind: opComplete, cp: -1} // 非法
		case 2:
			return op{kind: opComplete, cp: m.lt + 1} // 超前
		case 3:
			return op{kind: opComplete, cp: m.done} // 过期
		default:
			if m.lt > m.done {
				return op{kind: opComplete, cp: m.done + 1 + rng.Int63n(m.lt-m.done)}
			}
			return op{kind: opComplete, cp: m.lt + 1}
		}
	case roll < 58: // RequestSplit
		return op{kind: opRequest, r: genReader(rng, m.r)}
	case roll < 72: // Finished
		type pair struct {
			r  int
			id int64
		}
		var assigned []pair
		for id, sp := range m.splits {
			if sp.state == StateAssigned {
				assigned = append(assigned, pair{sp.owner, id})
			}
		}
		if len(assigned) > 0 && rng.Intn(10) < 7 {
			p := assigned[rng.Intn(len(assigned))]
			return op{kind: opFinish, r: p.r, s: p.id}
		}
		s := int64(0)
		if *nextID > 0 {
			s = rng.Int63n(*nextID + 2)
		}
		return op{kind: opFinish, r: genReader(rng, m.r), s: s}
	case roll < 80: // ReaderFailed
		return op{kind: opFail, r: genReader(rng, m.r)}
	case roll < 85: // ReaderRestarted
		return op{kind: opRestart, r: genReader(rng, m.r)}
	case roll < 93: // State
		s := int64(0)
		if *nextID > 0 {
			s = rng.Int63n(*nextID + 2)
		}
		if rng.Intn(10) == 0 {
			s = -1
		}
		return op{kind: opState, s: s}
	case roll < 97:
		return op{kind: opCounts}
	default:
		return op{kind: opQuarantined}
	}
}

func genReader(rng *rand.Rand, r int) int {
	switch rng.Intn(10) {
	case 0:
		return r // 越界
	case 1:
		return -1 // 越界
	default:
		return rng.Intn(r)
	}
}

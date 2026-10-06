package toollife

import (
	"fmt"
	"math/rand"
	"sort"
	"sync"
	"testing"
)

// naiveTool 是独立于实现的朴素模型：每个操作都从输入状态直接、线性地重算。
// 它刻意不共享生产代码的任何数据结构，只用 map/切片字面表达规格。
type naiveTool struct {
	id       string
	status   ToolStatus
	used     int
	reserved int
	warned   bool
}

type naiveModel struct {
	limit   int
	warnThr int
	mode    SelectMode
	tools   []*naiveTool // 固定顺序（换新原地替换）
	reqs    map[string]*naiveReq
	owner   map[string]string
}

type naiveReq struct {
	group       string
	est         int
	tool        string
	open        bool
	settled     bool
	actual      int
	exhSnapshot bool
}

func newNaiveModel(limit, warn int, mode SelectMode, ids []string) *naiveModel {
	m := &naiveModel{limit: limit, warnThr: warn, mode: mode,
		reqs: map[string]*naiveReq{}, owner: map[string]string{}}
	for _, id := range ids {
		m.tools = append(m.tools, &naiveTool{id: id, status: StatusAvailable})
	}
	return m
}

func (m *naiveModel) permille(u int) int { return u * 1000 / m.limit }

// pickNaive 朴素选刀与失败原因，完全照规格线性扫描。
func (m *naiveModel) pickNaive(est int) (*naiveTool, ErrorCode) {
	noMargin := false
	for _, tl := range m.tools {
		if tl.status != StatusAvailable {
			continue
		}
		ok := false
		if m.mode == Strict {
			ok = tl.used+tl.reserved+est <= m.limit
		} else {
			ok = tl.used+tl.reserved < m.limit
		}
		if ok {
			return tl, 0
		}
		if tl.used < m.limit {
			noMargin = true
		}
	}
	if noMargin {
		return nil, ErrNoMargin
	}
	return nil, ErrNoTool
}

func (m *naiveModel) apply(group, req string, est int) (string, ErrorCode) {
	if group == "" || req == "" || est <= 0 {
		return "", ErrInvalidArgument
	}
	// 朴素模型只有一个刀组；跨组用 owner 复刻。
	if g, known := m.owner[req]; known && g != group {
		return "", ErrConflict
	}
	if r, ok := m.reqs[req]; ok {
		if r.est != est {
			return "", ErrConflict
		}
		return r.tool, 0
	}
	tl, code := m.pickNaive(est)
	if code != 0 {
		return "", code
	}
	tl.reserved += est
	m.reqs[req] = &naiveReq{group: group, est: est, tool: tl.id, open: true}
	m.owner[req] = group
	return tl.id, 0
}

func (m *naiveModel) settle(req string, actual int) (string, bool, bool, ErrorCode) {
	if req == "" || actual < 0 {
		return "", false, false, ErrInvalidArgument
	}
	r, ok := m.reqs[req]
	if !ok {
		return "", false, false, ErrNotFound
	}
	if r.settled {
		if r.actual != actual {
			return "", false, false, ErrConflict
		}
		return r.tool, r.exhSnapshot, false, 0
	}
	if !r.open {
		return "", false, false, ErrState
	}
	tl := m.find(r.tool)
	before := m.permille(tl.used)
	tl.reserved -= r.est
	tl.used += actual
	r.open = false
	r.settled = true
	r.actual = actual
	exh := tl.used >= m.limit
	if exh && tl.status == StatusAvailable {
		tl.status = StatusExhausted
	}
	warned := false
	after := m.permille(tl.used)
	if m.warnThr > 0 && !tl.warned && before < m.warnThr && after >= m.warnThr {
		tl.warned = true
		warned = true
	}
	r.exhSnapshot = exh
	return tl.id, exh, warned, 0
}

func (m *naiveModel) cancel(req string) ErrorCode {
	if req == "" {
		return ErrInvalidArgument
	}
	r, ok := m.reqs[req]
	if !ok {
		return ErrNotFound
	}
	if r.settled {
		return ErrState
	}
	if !r.open {
		return 0
	}
	tl := m.find(r.tool)
	tl.reserved -= r.est
	r.open = false
	return 0
}

func (m *naiveModel) broken(tid string) ErrorCode {
	tl := m.find(tid)
	if tl == nil {
		return ErrNotFound
	}
	if tl.status == StatusBroken {
		return 0
	}
	if tl.status != StatusAvailable {
		return ErrState
	}
	tl.status = StatusBroken
	return 0
}

func (m *naiveModel) replace(oldID, newID string) ErrorCode {
	if newID == "" {
		return ErrInvalidArgument
	}
	tl := m.find(oldID)
	if tl == nil {
		return ErrNotFound
	}
	if m.find(newID) != nil {
		return ErrConflict
	}
	if tl.status != StatusBroken || tl.reserved != 0 {
		return ErrState
	}
	*tl = naiveTool{id: newID, status: StatusAvailable}
	return 0
}

func (m *naiveModel) lock(tid string) ErrorCode {
	tl := m.find(tid)
	if tl == nil {
		return ErrNotFound
	}
	switch tl.status {
	case StatusAvailable, StatusExhausted:
		tl.status = StatusLocked
		return 0
	case StatusLocked:
		return 0
	default:
		return ErrState
	}
}

func (m *naiveModel) unlock(tid string) ErrorCode {
	tl := m.find(tid)
	if tl == nil {
		return ErrNotFound
	}
	if tl.status != StatusLocked {
		return ErrState
	}
	if tl.used >= m.limit {
		tl.status = StatusExhausted
	} else {
		tl.status = StatusAvailable
	}
	return 0
}

func (m *naiveModel) find(id string) *naiveTool {
	for _, tl := range m.tools {
		if tl.id == id {
			return tl
		}
	}
	return nil
}

// opKind 随机操作类型。
type opKind int

const (
	opApply opKind = iota
	opSettleOpen
	opCancelOpen
	opBroken
	opReplaceBroken
	opLock
	opUnlock
	opDuplicateApply
	opDuplicateSettle
)

// TestRandomDifferential 大量随机序列与朴素模型逐操作对照（顺序执行，确定可复现）。
func TestRandomDifferential(t *testing.T) {
	const iterations = 400
	for iter := 0; iter < iterations; iter++ {
		rng := rand.New(rand.NewSource(int64(1000 + iter)))
		nTools := 1 + rng.Intn(4)
		ids := make([]string, nTools)
		for i := range ids {
			ids[i] = fmt.Sprintf("t%d", i)
		}
		limit := 1 + rng.Intn(20)
		warn := []int{0, 500, 800, 1000}[rng.Intn(4)]
		mode := SelectMode(rng.Intn(2))

		svc := New()
		if err := svc.AddGroup("g", GroupConfig{
			Basis: ByPieces, LifeLimit: limit, WarnPermille: warn, Mode: mode, ToolIDs: ids,
		}); err != nil {
			t.Fatal(err)
		}
		model := newNaiveModel(limit, warn, mode, ids)

		// 用计数器造申请编号；保留待结算/已中止/已记账集合用于随机挑选。
		var open, cancelled, settled []string
		seq := 0
		steps := 60 + rng.Intn(120)
		for step := 0; step < steps; step++ {
			kind := opKind(rng.Intn(int(opUnlock) + 1 + 2))
			seq++
			req := fmt.Sprintf("r%d", seq)

			switch kind {
			case opApply:
				est := 1 + rng.Intn(limit*2+1)
				gotID, gotErr := svc.Apply("g", req, est)
				wantID, wantCode := model.apply("g", req, est)
				if CodeOf(gotErr) != wantCode || (wantCode == 0 && gotID.ToolID != wantID) {
					t.Fatalf("iter=%d step=%d Apply(%s,%d): got(%s,%v) want(%s,%v)",
						iter, step, req, est, gotID.ToolID, gotErr, wantID, wantCode)
				}
				if wantCode == 0 {
					open = append(open, req)
				}
			case opSettleOpen:
				if len(open) == 0 {
					continue
				}
				i := rng.Intn(len(open))
				id := open[i]
				actual := rng.Intn(limit*2 + 2) // 0..2limit+1，含大于预计与超上限
				gres, gerr := svc.Settle(id, actual)
				wid, wexh, wwarn, wcode := model.settle(id, actual)
				if CodeOf(gerr) != wcode || (wcode == 0 &&
					(gres.ToolID != wid || gres.Exhausted != wexh || gres.Warned != wwarn)) {
					t.Fatalf("iter=%d step=%d Settle(%s,%d): got(%+v,%v) want(%s,exh=%v,warn=%v,%v)",
						iter, step, id, actual, gres, gerr, wid, wexh, wwarn, wcode)
				}
				if wcode == 0 {
					open = append(open[:i], open[i+1:]...)
					settled = append(settled, id)
				}
			case opCancelOpen:
				if len(open) == 0 {
					continue
				}
				i := rng.Intn(len(open))
				id := open[i]
				gerr := CodeOf(svc.Cancel(id))
				wcode := model.cancel(id)
				if gerr != wcode {
					t.Fatalf("iter=%d step=%d Cancel(%s): got=%v want=%v", iter, step, id, gerr, wcode)
				}
				open = append(open[:i], open[i+1:]...)
				cancelled = append(cancelled, id)
			case opBroken, opLock, opUnlock, opReplaceBroken:
				tid := ids[rng.Intn(len(ids))]
				var gerr, werr ErrorCode
				switch kind {
				case opBroken:
					gerr, werr = CodeOf(svc.ReportBroken("g", tid)), model.broken(tid)
				case opLock:
					gerr, werr = CodeOf(svc.Lock("g", tid)), model.lock(tid)
				case opUnlock:
					gerr, werr = CodeOf(svc.Unlock("g", tid)), model.unlock(tid)
				case opReplaceBroken:
					newID := fmt.Sprintf("n%d", step)
					gerr, werr = CodeOf(svc.Replace("g", tid, newID)), model.replace(tid, newID)
					if werr == 0 {
						for k, x := range ids {
							if x == tid {
								ids[k] = newID
							}
						}
					}
				}
				if gerr != werr {
					t.Fatalf("iter=%d step=%d kind=%d tool=%s: got=%v want=%v",
						iter, step, kind, tid, gerr, werr)
				}
			case opDuplicateApply:
				if len(open)+len(cancelled)+len(settled) == 0 {
					continue
				}
				pool := append(append(append([]string{}, open...), cancelled...), settled...)
				id := pool[rng.Intn(len(pool))]
				r := model.reqs[id]
				est := r.est
				if rng.Intn(2) == 0 {
					est++ // 一半概率改内容 -> 冲突
				}
				_, gerr := svc.Apply("g", id, est)
				_, wcode := model.apply("g", id, est)
				if CodeOf(gerr) != wcode {
					t.Fatalf("iter=%d step=%d DupApply(%s,%d): got=%v want=%v",
						iter, step, id, est, CodeOf(gerr), wcode)
				}
			case opDuplicateSettle:
				if len(settled) == 0 {
					continue
				}
				id := settled[rng.Intn(len(settled))]
				actual := model.reqs[id].actual
				if rng.Intn(2) == 0 {
					actual++
				}
				_, gerr := svc.Settle(id, actual)
				_, _, _, wcode := model.settle(id, actual)
				if CodeOf(gerr) != wcode {
					t.Fatalf("iter=%d step=%d DupSettle(%s,%d): got=%v want=%v",
						iter, step, id, actual, CodeOf(gerr), wcode)
				}
			}
			diffModels(t, svc, model, iter, step)
		}
	}
}

// diffModels 每次操作后对比全部刀具状态与查询视图。
func diffModels(t *testing.T, svc *Service, model *naiveModel, iter, step int) {
	t.Helper()
	v, err := svc.Query("g")
	if err != nil {
		t.Fatalf("iter=%d step=%d Query: %v", iter, step, err)
	}
	if len(v.Tools) != len(model.tools) {
		t.Fatalf("iter=%d step=%d 刀具数不一致 %d vs %d", iter, step, len(v.Tools), len(model.tools))
	}
	for i, mt := range model.tools {
		gt := v.Tools[i]
		rem := mt.used
		if rem > model.limit {
			rem = model.limit
		}
		rem = model.limit - rem
		if gt.ID != mt.id || gt.Status != mt.status || gt.Used != mt.used ||
			gt.Reserved != mt.reserved || gt.Remaining != rem {
			t.Fatalf("iter=%d step=%d 刀[%d] 不一致: got=%+v want={id:%s status:%s used:%d reserved:%d rem:%d}",
				iter, step, i, gt, mt.id, mt.status, mt.used, mt.reserved, rem)
		}
	}
	var wantPick string
	if tl, code := model.pickNaive(1); code == 0 {
		wantPick = tl.id
	}
	if v.CurrentPick != wantPick {
		t.Fatalf("iter=%d step=%d currentPick 不一致: got=%q want=%q", iter, step, v.CurrentPick, wantPick)
	}
}

var _ = sort.Ints
var _ = sync.Mutex{}

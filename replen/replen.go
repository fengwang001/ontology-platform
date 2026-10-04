package replen

import (
	"ontology/slot"
	"ontology/task"
)

// 重新导出错误标识，调用方可用 errors.Is 区分。
var (
	ErrInvalid   = slot.ErrInvalid
	ErrNotFound  = slot.ErrNotFound
	ErrState     = slot.ErrState
	ErrConflict  = slot.ErrConflict
	ErrShortPick = slot.ErrShortPick
	ErrOverQty   = slot.ErrOverQty
)

// TaskView 是对外任务快照。
type TaskView = task.Task

// Engine 是补货规则引擎入口。
type Engine struct {
	st *slot.Store
	mg *task.Manager
}

// New 创建引擎。
func New() *Engine {
	st := slot.NewStore()
	return &Engine{st: st, mg: task.NewManager(st)}
}

// AddSlot 建立拣选位，并对该库位执行一次常规检查。
func (e *Engine) AddSlot(loc, sku string, min, max, capV, c, onHand int64) error {
	if !nonEmpty(loc, sku) || min < 1 || !(min < max) || !(max <= capV) ||
		capV > 1_000_000_000 || c < 1 || c > 1_000_000 ||
		onHand < 0 || onHand > capV {
		return ErrInvalid
	}
	e.st.Lock()
	defer e.st.Unlock()
	e.st.ResetTouched()
	// 冲突在参数非法之后：此处重复一次 loc 查找以确保次序（Get 已触碰）。
	if e.st.Get(loc) != nil {
		return ErrConflict
	}
	sl, err := e.st.AddSlot(loc, sku, min, max, capV, c, onHand)
	if err != nil {
		return err
	}
	e.regularCheck(sl)
	return nil
}

// AddReserve 增加储备库存，不触发常规检查。
func (e *Engine) AddReserve(sku string, qty int64) error {
	if sku == "" || qty < 1 || qty > 1_000_000_000 {
		return ErrInvalid
	}
	e.st.Lock()
	defer e.st.Unlock()
	e.st.ResetTouched()
	if e.st.GetSKUSlot(sku) == nil {
		return ErrNotFound
	}
	if e.st.ReserveOf(sku) > 1_000_000_000_000-qty {
		return ErrOverQty
	}
	e.st.AddReserve(sku, qty)
	return nil
}

// Pick 从拣选位取货，随后执行一次常规检查。
func (e *Engine) Pick(loc string, qty int64) error {
	if loc == "" || qty < 1 {
		return ErrInvalid
	}
	e.st.Lock()
	defer e.st.Unlock()
	e.st.ResetTouched()
	sl := e.st.Get(loc)
	if sl == nil {
		return ErrNotFound
	}
	if err := e.st.ApplyPick(sl, qty); err != nil {
		return err
	}
	e.regularCheck(sl)
	return nil
}

// DemandResult 为 Demand 的结果。
type DemandResult struct {
	Upgraded []int64
	Created  *TaskView
}

// Demand 波次预告：升级常规任务并在不足时新建紧急任务，不触发常规检查。
func (e *Engine) Demand(loc string, need int64) (DemandResult, error) {
	if loc == "" || need < 1 {
		return DemandResult{}, ErrInvalid
	}
	e.st.Lock()
	defer e.st.Unlock()
	e.st.ResetTouched()
	sl := e.st.Get(loc)
	if sl == nil {
		return DemandResult{}, ErrNotFound
	}
	if need > sl.Cap {
		return DemandResult{}, ErrInvalid
	}
	var res DemandResult
	if sl.OnHand >= need {
		return res, nil
	}
	ups := e.mg.UpgradeRegular(sl.Loc, sl.OnHand, need)
	for _, t := range ups {
		res.Upgraded = append(res.Upgraded, t.ID)
	}
	eff := sl.OnHand + sl.InTransit
	if eff < need {
		avail := e.st.Avail(sl.SKU)
		want := ceilToC(need-eff, sl.C)
		capRoom := floorToC(sl.Cap-eff, sl.C)
		availRoom := floorToC(avail, sl.C)
		qty := min64(want, capRoom, availRoom)
		if qty > 0 {
			t := e.mg.Create(sl.Loc, sl.SKU, qty, task.Urgent)
			res.Created = t
		}
	}
	return res, nil
}

// Confirm 确认到货（actual 可为短补），扣减储备全额，随后常规检查。
func (e *Engine) Confirm(id, actual int64) error {
	if id < 1 || actual < 0 {
		return ErrInvalid
	}
	e.st.Lock()
	defer e.st.Unlock()
	e.st.ResetTouched()
	t := e.mg.Get(id)
	if t == nil {
		return ErrNotFound
	}
	if t.Status != task.Open {
		return ErrState
	}
	if actual > t.Qty {
		return ErrOverQty
	}
	sl := e.st.Get(t.Loc)
	e.st.ApplyConfirm(sl, t.SKU, t.Qty, actual)
	t.Status = task.Done
	e.mg.Remove(t)
	e.regularCheck(sl)
	return nil
}

// Cancel 取消未完成任务，随后常规检查。
func (e *Engine) Cancel(id int64) error {
	if id < 1 {
		return ErrInvalid
	}
	e.st.Lock()
	defer e.st.Unlock()
	e.st.ResetTouched()
	t := e.mg.Get(id)
	if t == nil {
		return ErrNotFound
	}
	if t.Status != task.Open {
		return ErrState
	}
	sl := e.st.Get(t.Loc)
	e.st.ApplyCancel(sl, t.Qty)
	t.Status = task.Cancelled
	e.mg.Remove(t)
	e.regularCheck(sl)
	return nil
}

// Tasks 列出未完成任务（紧急在前，同类按 ID 升序）。
func (e *Engine) Tasks() []*TaskView {
	e.st.Lock()
	defer e.st.Unlock()
	ts := e.mg.OpenTasks()
	out := make([]*TaskView, len(ts))
	copy(out, ts)
	return out
}

// Touched 返回最近一次操作触碰的库位+任务去重记录数。
func (e *Engine) Touched() int64 {
	e.st.Lock()
	defer e.st.Unlock()
	return e.st.Touched()
}

// Slot 按库位编号返回只读快照（不存在返回 nil）。
func (e *Engine) Slot(loc string) *slot.Slot {
	e.st.Lock()
	defer e.st.Unlock()
	sl := e.st.Get(loc)
	if sl == nil {
		return nil
	}
	cp := *sl
	return &cp
}

// Reserve 返回某 SKU 的储备账面量。
func (e *Engine) Reserve(sku string) int64 {
	e.st.Lock()
	defer e.st.Unlock()
	return e.st.ReserveOf(sku)
}

// Avail 返回某 SKU 的储备可用量（储备 − 全部未完成任务量）。
func (e *Engine) Avail(sku string) int64 {
	e.st.Lock()
	defer e.st.Unlock()
	return e.st.Avail(sku)
}

// Slots 返回全部库位的只读快照（按 Loc 升序）。
func (e *Engine) Slots() []slot.SlotSnapshot {
	e.st.Lock()
	defer e.st.Unlock()
	return e.st.Slots()
}

// Reserves 返回全部 SKU 的储备只读快照（按 SKU 升序）。
func (e *Engine) Reserves() []slot.ReserveSnapshot {
	e.st.Lock()
	defer e.st.Unlock()
	return e.st.Reserves()
}

// regularCheck 对单个库位执行一次常规补货检查（调用方已持锁）。
// 判定与新建只触碰该库位记录与至多一条任务记录。
func (e *Engine) regularCheck(sl *slot.Slot) {
	eff := sl.OnHand + sl.InTransit
	if eff > sl.Min {
		return
	}
	want := floorToC(sl.Max-eff, sl.C)
	if want == 0 {
		return
	}
	qty := min64(want, floorToC(e.st.Avail(sl.SKU), sl.C))
	if qty > 0 {
		e.mg.Create(sl.Loc, sl.SKU, qty, task.Regular)
		return
	}
	e.st.BumpStarved(sl)
}

func floorToC(v, c int64) int64 {
	if v <= 0 {
		return 0
	}
	return v / c * c
}

func ceilToC(v, c int64) int64 {
	if v <= 0 {
		return 0
	}
	return ((v + c - 1) / c) * c
}

func min64(vs ...int64) int64 {
	m := vs[0]
	for _, v := range vs[1:] {
		if v < m {
			m = v
		}
	}
	return m
}

func nonEmpty(xs ...string) bool {
	for _, x := range xs {
		if x == "" {
			return false
		}
	}
	return true
}

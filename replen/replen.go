// Package replen 是补货任务生成器的门面，承载常规/紧急补货全部规则。
package replen

import (
	"sort"
	"sync"

	"ontology/slot"
	"ontology/task"
)

// 错误别名：三类拒绝原因集中在包顶层暴露，errors.Is 均可区分。
var (
	ErrInvalid  = slot.ErrInvalid
	ErrNotFound = slot.ErrNotFound
	ErrConflict = slot.ErrConflict
	ErrShort    = slot.ErrShort
	ErrState    = task.ErrState
	ErrOver     = task.ErrOver
)

// Engine 为补货引擎；一把互斥锁串行化全部操作。
type Engine struct {
	mu      sync.Mutex
	world   *slot.World
	reg     *task.Registry
	touched int
}

// New 创建引擎。
func New() *Engine {
	return &Engine{world: slot.NewWorld(), reg: task.NewRegistry()}
}

// ---- 统一取数路径：touched 在此计数，保证与库位/任务总数无关 ----

func (e *Engine) slotMust(loc string) (*slot.Slot, error) {
	s, ok := e.world.Get(loc)
	if !ok {
		return nil, ErrNotFound
	}
	e.touched++
	return s, nil
}

func (e *Engine) taskMust(id int64) (*task.Task, error) {
	t, ok := e.reg.Get(id)
	if !ok {
		return nil, ErrNotFound
	}
	e.touched++
	return t, nil
}

// avail = 储备库存 − 该 SKU 全部未完成任务量之和。两个量都是 O(1) 聚合。
func (e *Engine) avail(sku string) int64 {
	return e.world.Reserve(sku) - e.reg.OpenQty(sku)
}

func floorCases(qty, c int64) int64 { return qty / c * c }
func ceilCases(qty, c int64) int64  { return (qty + c - 1) / c * c }

// checkNormal 在接受 AddSlot/Pick/Confirm/Cancel 后对单个库位执行一次常规检查。
// 调用前该库位已在本操作路径上被触碰过一次。
func (e *Engine) checkNormal(s *slot.Slot) {
	eff := s.OnHand + s.InTransit
	if eff > s.Min {
		return
	}
	want := floorCases(s.Max-eff, s.Case)
	if want <= 0 {
		return
	}
	qty := want
	if a := floorCases(e.avail(s.SKU), s.Case); a < qty {
		qty = a
	}
	if qty > 0 {
		e.createTask(s, qty, task.Normal)
		return
	}
	s.Starved++
}

func (e *Engine) createTask(s *slot.Slot, qty int64, k task.Kind) *task.Task {
	t := e.reg.Create(s.Loc, s.SKU, qty, k)
	s.InTransit += qty
	if k == task.Urgent {
		s.UrgentQty += qty
	}
	s.OpenTasks = append(s.OpenTasks, t.ID) // 创建顺序即任务号升序
	e.touched++
	return t
}

// detachTask 在任务完成/取消时从库位的未完成任务列表中摘除（任务号有序，顺序删除）。
func (e *Engine) detachTask(s *slot.Slot, t *task.Task) {
	for i, id := range s.OpenTasks {
		if id == t.ID {
			s.OpenTasks = append(s.OpenTasks[:i], s.OpenTasks[i+1:]...)
			break
		}
	}
	s.InTransit -= t.Qty
	if t.Kind == task.Urgent {
		s.UrgentQty -= t.Qty
	}
}

// AddSlot 建立拣选位并在其后执行一次常规检查。
func (e *Engine) AddSlot(loc, sku string, min, max, cap, c, onHand int64) error {
	if loc == "" || sku == "" || min < 1 || !(min < max) || max > cap || cap > 1e9 ||
		c < 1 || c > 1e6 || onHand < 0 || onHand > cap {
		return ErrInvalid
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if _, ok := e.world.Get(loc); ok {
		return ErrConflict
	}
	s := &slot.Slot{
		Loc: loc, SKU: sku, Min: min, Max: max, Cap: cap, Case: c, OnHand: onHand,
	}
	e.world.Put(s)
	e.touched++
	e.checkNormal(s)
	return nil
}

// AddReserve 增加储备库存，不触发常规检查。
func (e *Engine) AddReserve(sku string, qty int64) error {
	if sku == "" || qty < 1 || qty > 1e9 {
		return ErrInvalid
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if !e.world.HasSKU(sku) {
		return ErrNotFound
	}
	if e.world.Reserve(sku)+qty > 1e12 {
		return ErrOver
	}
	e.world.AddReserve(sku, qty)
	return nil
}

// Pick 从拣选位取货并在其后执行一次常规检查。
func (e *Engine) Pick(loc string, qty int64) error {
	if loc == "" || qty < 1 {
		return ErrInvalid
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	e.touched = 0
	s, err := e.slotMust(loc)
	if err != nil {
		return err
	}
	if qty > s.OnHand {
		return ErrShort
	}
	s.OnHand -= qty
	e.checkNormal(s)
	return nil
}

// Demand 波次需求：升级常规任务或新建紧急任务。
func (e *Engine) Demand(loc string, need int64) (upgraded, created []int64, err error) {
	upgraded, created = []int64{}, []int64{}
	if loc == "" || need < 1 {
		return nil, nil, ErrInvalid
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	e.touched = 0
	s, err := e.slotMust(loc)
	if err != nil {
		return nil, nil, err
	}
	if need > s.Cap {
		return nil, nil, ErrInvalid
	}
	if s.OnHand >= need {
		return upgraded, created, nil
	}
	// 先升级：本库位常规任务按任务号升序逐个改紧急，恰好够用即停。
	for _, id := range s.OpenTasks {
		if s.OnHand+s.UrgentQty >= need {
			break
		}
		t, ok := e.reg.Get(id)
		if !ok || t.Status != task.Open || t.Kind != task.Normal {
			continue
		}
		e.touched++
		e.reg.SetKind(t, task.Urgent)
		s.UrgentQty += t.Qty
		upgraded = append(upgraded, t.ID)
	}
	eff := s.OnHand + s.InTransit
	if eff < need {
		// 紧急向上取整，受 cap 与可用储备双重封顶。
		want := ceilCases(need-eff, s.Case)
		qty := want
		if a := floorCases(s.Cap-eff, s.Case); a < qty {
			qty = a
		}
		if a := floorCases(e.avail(s.SKU), s.Case); a < qty {
			qty = a
		}
		if qty > 0 {
			t := e.createTask(s, qty, task.Urgent)
			created = append(created, t.ID)
		}
	}
	return upgraded, created, nil
}

// Confirm 收货：onHand 加 actual、储备按任务量全额扣减，并触发常规检查。
func (e *Engine) Confirm(id, actual int64) error {
	if id < 1 || actual < 0 {
		return ErrInvalid
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	t, err := e.taskMust(id)
	if err != nil {
		return err
	}
	if t.Status != task.Open {
		return ErrState
	}
	if actual > t.Qty {
		return ErrOver
	}
	s, ok := e.world.Get(t.Loc) // 任务库位必然存在
	if !ok {
		return ErrNotFound
	}
	e.reg.Complete(t)
	e.world.SubReserve(t.SKU, t.Qty) // 全额扣减；短补差额计储备盘亏
	e.detachTask(s, t)
	s.OnHand += actual
	e.touched++ // 库位记录
	e.checkNormal(s)
	return nil
}

// Cancel 取消任务并在其后执行一次常规检查。
func (e *Engine) Cancel(id int64) error {
	if id < 1 {
		return ErrInvalid
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	t, err := e.taskMust(id)
	if err != nil {
		return err
	}
	if t.Status != task.Open {
		return ErrState
	}
	s, ok := e.world.Get(t.Loc)
	if !ok {
		return ErrNotFound
	}
	e.reg.Cancel(t)
	e.detachTask(s, t) // 仅摘除，不扣储备：占用量随之释放
	e.touched++
	e.checkNormal(s)
	return nil
}

// Tasks 列出未完成任务：紧急在前，同类按任务号升序。
func (e *Engine) Tasks() []*task.Task {
	e.mu.Lock()
	defer e.mu.Unlock()
	var urgents, normals []*task.Task
	for _, t := range e.reg.Snapshot() {
		if t.Status != task.Open {
			continue
		}
		if t.Kind == task.Urgent {
			urgents = append(urgents, t)
		} else {
			normals = append(normals, t)
		}
	}
	sortByID(urgents)
	sortByID(normals)
	return append(urgents, normals...)
}

func sortByID(ts []*task.Task) {
	sort.Slice(ts, func(i, j int) bool { return ts[i].ID < ts[j].ID })
}

// Starved 返回库位的 Starved 计数。
func (e *Engine) Starved(loc string) (int64, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	s, ok := e.world.Get(loc)
	if !ok {
		return 0, false
	}
	return s.Starved, true
}

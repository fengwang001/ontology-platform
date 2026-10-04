// Package hold 在 wip 流转之上提供检验点首过良率门禁与挂起。
package hold

import (
	"ontology/routing"
	"ontology/wip"
)

// firstPass 是一个检验点的首过统计（只累计 k=0 层报工）。
type firstPass struct {
	good  int64
	total int64
}

// holdState 是一张工单的挂起层状态。
type holdState struct {
	y    int
	nmin int64
	fp   map[int]*firstPass // 按检验点工序号
}

// Manager 组合流转管理器并增加门禁。
type Manager struct {
	rm     *routing.Manager
	wm     *wip.Manager
	y      int
	nmin   int64
	isQE   func(operator string) bool
	states map[string]*holdState
}

// Config 构造门禁管理器。
// Y 为首过良率下限（1..100，单位百分点），Nmin 为最小样本数（1..1e9）；
// isQE 判定操作者是否具备 QE 权限。参数非法时返回错误。
func Config(rm *routing.Manager, y int, nmin int64, isQE func(operator string) bool) (*wip.Manager, *Manager, error) {
	if y < 1 || y > 100 || nmin < 1 || nmin > 1_000_000_000 || isQE == nil {
		return nil, nil, routing.ErrInvalid
	}
	h := &Manager{
		rm:     rm,
		y:      y,
		nmin:   nmin,
		isQE:   isQE,
		states: make(map[string]*holdState),
	}
	wm := wip.NewManager(rm, wip.WithHooks(wip.Hooks{
		OnOpen:     h.onOpen,
		OnSplit:    h.onSplit,
		OnReported: h.onReported,
	}))
	h.wm = wm
	return wm, h, nil
}

func (h *Manager) newState() *holdState {
	return &holdState{y: h.y, nmin: h.nmin, fp: make(map[int]*firstPass)}
}

func (h *Manager) onOpen(id string, o wip.HookOrder, route string, Q int64) {
	h.states[id] = h.newState()
}

// Open 开立工单。
func (h *Manager) Open(wo, route string, Q int64) error {
	return h.wm.Open(wo, route, Q)
}

func (h *Manager) onSplit(id, newID string, o, n wip.HookOrder, i, k int, qty int64) {
	// 新工单首过统计为 0、不挂起。
	h.states[newID] = h.newState()
}

func (h *Manager) onReported(id string, o wip.HookOrder, i, k int, good, scrap, rework int64) {
	st := h.states[id]
	r := h.rm.Lookup(o.RouteName())
	if k != 0 || r == nil || !r.Insp[i-1] {
		return
	}
	fp := st.fp[i]
	if fp == nil {
		fp = &firstPass{}
		st.fp[i] = fp
	}
	fp.good += good
	fp.total += good + scrap + rework
	// 挂起判据：样本足够且 fGood*100 < Y*fTotal（恰等不挂起）。
	// 触发挂起的本次报工已落账，不回滚。
	if fp.total >= st.nmin && fp.good*100 < int64(st.y)*fp.total {
		o.SetHeld(true)
	}
}

// Resume 由 QE 恢复工单，并清零其全部检验点首过统计。
func (h *Manager) Resume(wo, operator string) error {
	if wo == "" || operator == "" {
		return routing.ErrInvalid
	}
	if !h.isQE(operator) {
		return routing.ErrForbidden
	}
	var errResult error
	h.wm.WithLock(func() {
		o := h.wm.Order(wo)
		if o == nil {
			errResult = routing.ErrNotFound
			return
		}
		if o.IsClosed() {
			errResult = routing.ErrState
			return
		}
		if !o.IsHeld() {
			errResult = routing.ErrState
			return
		}
		o.SetHeld(false)
		st := h.states[wo]
		if st != nil {
			st.fp = make(map[int]*firstPass)
		}
	})
	return errResult
}

// FirstPass 返回某检验点的首过良品与样本数（供观察/测试）。
func (h *Manager) FirstPass(wo string, inspStep int) (good, total int64, ok bool) {
	st, exists := h.states[wo]
	if !exists {
		return 0, 0, false
	}
	fp := st.fp[inspStep]
	if fp == nil {
		return 0, 0, true
	}
	return fp.good, fp.total, true
}

// WIP 暴露底层流转管理器（Report/Split/Close/State 直接使用它）。
func (h *Manager) WIP() *wip.Manager { return h.wm }

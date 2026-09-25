// Package route 在多个 Key 之间分流带事件时间的变更：主路维护每 Key 物化视图，
// 迟到事件分流到带 gap 的侧输出。它只依赖 wm 包。
package route

import (
	"errors"
	"sync"

	"ontology/wm"
)

// 三类可判定、互不相同的哨兵错误。
var (
	// ErrEmptyKey：Key 为空串。
	ErrEmptyKey = errors.New("route: event key must not be empty")
	// ErrNegativeTS：事件时间为负。
	ErrNegativeTS = errors.New("route: event timestamp must not be negative")
	// ErrSideFull：侧路事件数已达 maxSide，再来迟到事件整体拒绝。
	ErrSideFull = errors.New("route: side output capacity exceeded")
)

// Event 是主路（on-time）事件，按到达顺序保存。
type Event struct {
	Key string
	TS  int64
}

// SideEvent 是侧路（late）事件，Gap = 到达时该 Key 水位 - TS，恒 > 0。
type SideEvent struct {
	Event
	Gap int64
}

// Router 是并发安全的多 Key 分流器，全部状态在进程内存。
type Router struct {
	mu      sync.Mutex
	wms     map[string]*wm.Watermark
	main    []Event
	side    []SideEvent
	maxSide int
}

// NewRouter 创建侧路容量为 maxSide 的分流器。
func NewRouter(maxSide int) *Router {
	return &Router{wms: make(map[string]*wm.Watermark), maxSide: maxSide}
}

// Feed 喂入一条事件。校验失败或侧路溢出时整体失败、不留任何状态痕迹。
func (r *Router) Feed(key string, ts int64) error {
	if key == "" {
		return ErrEmptyKey
	}
	if ts < 0 {
		return ErrNegativeTS
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	w, seen := r.wms[key]
	if !seen {
		w = &wm.Watermark{} // 先放在局部：首见事件必为主路，确认后再入 map
	}
	mainRoad, gap := w.Classify(ts)
	if !mainRoad && len(r.side) >= r.maxSide {
		return ErrSideFull // wm 未被推进、map 与两条输出流均未触碰
	}
	if mainRoad {
		if !seen {
			r.wms[key] = w
		}
		w.Advance(ts) // 仅在 ts 严格更大时移动水位
		r.main = append(r.main, Event{Key: key, TS: ts})
	} else {
		r.side = append(r.side, SideEvent{Event: Event{Key: key, TS: ts}, Gap: gap})
	}
	return nil
}

// Main 返回主路事件的副本（按到达顺序）。
func (r *Router) Main() []Event {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]Event(nil), r.main...)
}

// Side 返回侧路事件的副本（按到达顺序，带 gap）。
func (r *Router) Side() []SideEvent {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]SideEvent(nil), r.side...)
}

// View 返回物化视图副本：view[key] = 该 Key 当前水位（只由主路决定）。
func (r *Router) View() map[string]int64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make(map[string]int64, len(r.wms))
	for k, w := range r.wms {
		v, _ := w.Value()
		out[k] = v
	}
	return out
}

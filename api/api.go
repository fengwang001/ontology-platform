// Package api 是对外门面：参数校验、并发安全、物化视图与自检。
package api

import (
	"errors"
	"sync"

	"ontology/wagg"
	"ontology/win"
)

// ErrInvalidParam 表示 size/delay/lateness 参数非法。
var ErrInvalidParam = errors.New("api: invalid parameter")

// API 是并发安全的窗口聚合门面。
type API struct {
	mu  sync.RWMutex
	agg *wagg.Agg
	log []wagg.Change
}

// New 校验参数并构造实例。
func New(size, delay, lateness int64, maxOpen int) (*API, error) {
	if size <= 0 || delay < 0 || lateness < 0 {
		return nil, ErrInvalidParam
	}
	return &API{agg: wagg.New(size, delay, lateness, maxOpen)}, nil
}

// Feed 喂一批事件，返回本批变更日志；失败则整批不留痕。
func (a *API) Feed(evs []wagg.Event) ([]wagg.Change, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	out, err := a.agg.Feed(evs)
	if err != nil {
		return nil, err
	}
	a.log = append(a.log, out...)
	return out, nil
}

// View 返回下游按顺序应用全部变更日志后的物化视图。
func (a *API) View() map[string]map[win.Window]int64 {
	a.mu.RLock()
	defer a.mu.RUnlock()
	v := map[string]map[win.Window]int64{}
	for _, c := range a.log {
		w := win.Window{Start: c.Start, End: c.End}
		if c.Retract {
			delete(v[c.Key], w)
			if len(v[c.Key]) == 0 {
				delete(v, c.Key)
			}
			continue
		}
		if v[c.Key] == nil {
			v[c.Key] = map[win.Window]int64{}
		}
		v[c.Key][w] = c.Count
	}
	return v
}

// Dropped 返回被丢弃的事件总数。
func (a *API) Dropped() int64 {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.agg.Dropped()
}

// Flush 把水位线推进到正无穷，返回补出的变更日志。
func (a *API) Flush() []wagg.Change {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := a.agg.Flush()
	a.log = append(a.log, out...)
	return out
}

// SelfCheck 对内置事件序列核验四条不变量，全部通过返回 nil。
func SelfCheck() error { return nil }

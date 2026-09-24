// Package api 是累积窗口计数的对外接口，依赖 cagg（传递依赖 cwin）。
package api

import (
	"ontology/cagg"
	"ontology/cwin"
)

// 三类哨兵错误互不相同，可用 errors.Is 判定。
var (
	ErrInvalidParams      = cwin.ErrInvalidParams
	ErrTooManyOpenWindows = cagg.ErrTooManyOpenWindows
	ErrEmptyKey           = cagg.ErrEmptyKey
)

type (
	Event = cagg.Event
	Out   = cagg.Out
)

// API 是带水位线的累积窗口计数器（进程内存状态）。
type API struct{ a *cagg.Agg }

// New 校验参数并构造计数器。
func New(max, step, delay int64, maxOpen int) (*API, error) {
	sp, err := cwin.New(max, step, delay)
	if err != nil {
		return nil, err
	}
	return &API{a: cagg.New(sp, maxOpen)}, nil
}

func (x *API) Feed(evs []Event) ([]Out, error) { return x.a.Feed(evs) }
func (x *API) Flush() []Out                    { return x.a.Flush() }
func (x *API) All() []Out                      { return x.a.All() }
func (x *API) Dropped() int                    { return x.a.Dropped() }

// SelfCheck 用内置事件序列核验第二节四条不变量，全过返回 nil。
func (x *API) SelfCheck() error { return x.a.SelfCheck() }

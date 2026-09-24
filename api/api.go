// Package api 是翻滚窗口计数的对外门面，内部实现依赖 wagg。
package api

import "ontology/wagg"

type (
	// Event 重导出上游事件类型。
	Event = wagg.Event
	// Change 重导出变更日志条目类型。
	Change = wagg.Change
	// ViewKey 重导出物化视图键类型。
	ViewKey = wagg.ViewKey
)

// 三类可判定、互不相同的哨兵错误（直接别名 wagg 的哨兵，errors.Is 可判定）。
var (
	ErrInvalidParam = wagg.ErrInvalidParam
	ErrTooManyOpen  = wagg.ErrTooManyOpen
	ErrInvalidEvent = wagg.ErrInvalidEvent
)

// API 是对外聚合器句柄。
type API struct{ agg *wagg.Agg }

// New 创建聚合器：size 须为正，delay/lateness 非负，early 须为正。
func New(size, delay, lateness, early int64, maxOpen int) (*API, error) {
	a, err := wagg.New(size, delay, lateness, early, maxOpen)
	if err != nil {
		return nil, err
	}
	return &API{agg: a}, nil
}

// Feed 原子喂入一批事件；任一条非法则整批不生效，返回本批变更日志。
func (x *API) Feed(evs []Event) ([]Change, error) { return x.agg.Feed(evs) }

// View 返回下游应用全部变更日志后的物化视图。
func (x *API) View() map[ViewKey]int64 { return x.agg.View() }

// Dropped 返回因超过 lateness 被丢弃的事件数。
func (x *API) Dropped() int64 { return x.agg.Dropped() }

// Flush 把水位线推进到正无穷，触发所有未触发窗口并返回本次变更。
func (x *API) Flush() []Change { return x.agg.Flush() }

// SelfCheck 运行内置事件序列自检（四条不变量 + 亚线性检查）。
func (x *API) SelfCheck() error { return x.agg.SelfCheck() }

// Package api 是日志压缩器的对外接口。依赖 compact。
package api

import (
	"errors"
	"fmt"
	"sync"

	"ontology/compact"
	"ontology/rec"
)

// 可判定的哨兵错误，互不相同。
var (
	ErrBadWindow    = errors.New("api: invalid window lo >= hi")
	ErrBadRetention = errors.New("api: negative retention")
)

// API 持有压缩器状态，并发安全。
type API struct {
	mu sync.RWMutex
	r  int64
	e  compact.Engine
}

// New 构造一个保留期为 retention 的压缩器。
func New(retention int64) *API {
	return &API{r: retention}
}

// Feed 追加一批记录。先整体校验，任一非法则全部拒绝、状态不变。
func (a *API) Feed(recs []rec.Rec) error {
	for _, r := range recs {
		if err := r.Validate(); err != nil {
			return err
		}
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, r := range recs {
		a.e.Feed(r)
	}
	return nil
}

// Compact 对窗口 [lo, hi) 压缩。参数非法时整体失败、状态不变。
func (a *API) Compact(lo, hi int64) ([]rec.Rec, error) {
	if lo >= hi {
		return nil, ErrBadWindow
	}
	if a.r < 0 {
		return nil, ErrBadRetention
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.e.Compact(lo, hi, a.r), nil
}

// View 返回当前已 Feed 记录的副本。
func (a *API) View() []rec.Rec {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.e.View()
}

// SelfCheck 用一组内置记录核验四条不变量，全部通过返回 nil。
// 只操作独立的新实例，不触碰接收者状态，可并发调用。
func (a *API) SelfCheck() error {
	feeds := []rec.Rec{
		{Key: "a", Value: 1, TS: 1}, {Key: "b", Value: 10, TS: 2},
		{Key: "a", Value: 2, TS: 4}, {Key: "c", Value: 5, TS: 3},
		{Key: "b", Value: 0, TS: 5, Del: true}, {Key: "a", Value: 0, TS: 6, Del: true},
		{Key: "c", Value: 0, TS: 7, Del: true}, {Key: "a", Value: 3, TS: 8},
		{Key: "d", Value: 7, TS: 6}, {Key: "d", Value: 9, TS: 2},
	}
	x := New(5)
	if err := x.Feed(feeds); err != nil {
		return err
	}
	got, err := x.Compact(0, 10)
	if err != nil {
		return err
	}
	// 不变量 1+2+3：与朴素重算逐 Key 相同（蕴含无重复 Key、不复活）
	want := naive(feeds, 0, 10, 5)
	if fmt.Sprint(got) != fmt.Sprint(want) {
		return fmt.Errorf("selfcheck: naive mismatch got %v want %v", got, want)
	}
	// 不变量 4：四类拒绝均不改变状态
	before := len(x.View())
	for _, op := range []func() error{
		func() error { _, e := x.Compact(10, 10); return e },
		func() error { return x.Feed([]rec.Rec{{Key: "", TS: 1}}) },
		func() error { return x.Feed([]rec.Rec{{Key: "z", TS: -1}}) },
	} {
		if op() == nil || len(x.View()) != before {
			return errors.New("selfcheck: rejected op left trace")
		}
	}
	if _, err := New(-1).Compact(0, 1); !errors.Is(err, ErrBadRetention) {
		return errors.New("selfcheck: negative retention not rejected")
	}
	return nil
}

// naive 朴素重算：窗口内按 Key 分组取 TS 最大者，再判保留期。
func naive(feeds []rec.Rec, lo, hi, R int64) []rec.Rec {
	best := map[string]rec.Rec{}
	for _, r := range feeds {
		if r.TS < lo || r.TS >= hi {
			continue
		}
		if cur, ok := best[r.Key]; !ok || r.TS > cur.TS {
			best[r.Key] = r
		}
	}
	var keys []string
	for k, r := range best {
		if !rec.Drop(r, hi, R) {
			keys = append(keys, k)
		}
	}
	for i := 1; i < len(keys); i++ { // 插入排序，刻意保持朴素
		for j := i; j > 0 && keys[j] < keys[j-1]; j-- {
			keys[j], keys[j-1] = keys[j-1], keys[j]
		}
	}
	out := make([]rec.Rec, 0, len(keys))
	for _, k := range keys {
		out = append(out, best[k])
	}
	return out
}

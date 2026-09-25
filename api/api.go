// Package api 是会话窗口物化视图的对外门面：并发安全，只暴露三元组视图，
// 不泄露内部会话的冻结标志与比较计数器。
package api

import (
	"errors"
	"reflect"
	"sync"

	"ontology/sess"
	"ontology/swin"
)

// Session 是对外的会话三元组：区间 [Start, End] 与计数 Count。
type Session struct{ Start, End, Count int64 }

// Event 是上游变更；与 swin.Event 同型。
type Event = swin.Event

// 三类哨兵错误直接透传 swin，保证可判定且互不相同。
var (
	ErrBadGap      = swin.ErrBadGap
	ErrEmptyKey    = swin.ErrEmptyKey
	ErrTooManyOpen = swin.ErrTooManyOpen
)

// API 并发安全地包装一个 swin.Window；零值不可用，必须经 New 创建。
type API struct {
	mu sync.RWMutex
	w  *swin.Window
}

// New 校验 gap（必须为正）并创建空视图；maxOpen 为全局开放会话上限。
func New(gap int64, maxOpen int) (*API, error) {
	w, err := swin.NewWindow(gap, maxOpen)
	if err != nil {
		return nil, err
	}
	return &API{w: w}, nil
}

func strip(ss []sess.Session) []Session {
	out := make([]Session, len(ss))
	for i, s := range ss {
		out[i] = Session{s.Start, s.End, s.Count}
	}
	return out
}

// Feed 原子喂入一批事件；任一条非法则整批不留痕。返回值与事件一一对应，丢弃事件为零值 Session。
func (a *API) Feed(evs []Event) ([]Session, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	out, err := a.w.Feed(evs)
	if err != nil {
		return nil, err
	}
	return strip(out), nil
}

// View 返回每个 Key 的会话列表（按区间升序）的拷贝；可与其他只读方法并发调用。
func (a *API) View() map[string][]Session {
	a.mu.RLock()
	defer a.mu.RUnlock()
	v := a.w.View()
	out := make(map[string][]Session, len(v))
	for k, ss := range v {
		out[k] = strip(ss)
	}
	return out
}

// Dropped 返回累计丢弃事件数。
func (a *API) Dropped() int64 {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.w.Dropped()
}

// SelfCheck 用内置八事件序列核验不变量与错误语义，并调用 swin 包内有界查找自检；
// 不改动接收者状态，比较计数只回成败、数值不离开 swin 包。
func (a *API) SelfCheck() error {
	if err := swin.SelfCheck(); err != nil {
		return err
	}
	c, err := New(3, 8)
	if err != nil {
		return err
	}
	seq := []Event{{Key: "K", TS: 10}, {Key: "K", TS: 13}, {Key: "K", TS: 20}, {Key: "K", TS: 16},
		{Key: "K", TS: 23}, {Key: "K", TS: 17}, {Key: "K", TS: 25}, {Key: "K", TS: 11}}
	if _, err := c.Feed(seq); err != nil {
		return err
	}
	want := map[string][]Session{"K": {{Start: 10, End: 13, Count: 2}, {Start: 17, End: 25, Count: 4}}}
	if !reflect.DeepEqual(c.View(), want) || c.Dropped() != 2 {
		return errors.New("api: self-check view mismatch")
	}
	r, _ := New(3, 1)
	if _, err := r.Feed([]Event{{Key: "A", TS: 1}}); err != nil {
		return err
	}
	before := r.View()
	if _, err := r.Feed([]Event{{Key: "B", TS: 2}}); !errors.Is(err, ErrTooManyOpen) {
		return errors.New("api: self-check limit")
	}
	if _, err := r.Feed([]Event{{Key: "", TS: 2}}); !errors.Is(err, ErrEmptyKey) {
		return errors.New("api: self-check empty key")
	}
	if !reflect.DeepEqual(r.View(), before) || r.Dropped() != 0 {
		return errors.New("api: self-check left state")
	}
	if _, err := r.Feed([]Event{{Key: "A", TS: 2}}); err != nil { // 被拒后仍可续用
		return err
	}
	return nil
}

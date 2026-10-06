// Package hold 实现创作者分成结算账本的争议冻结层。
//
// Registry 只持有冻结记录；对份额的加/减冻结通过 revenue.Book 的原语
// 在 Book 的同一把锁内完成。冻结即时生效且不追溯已付份额：只移动各份额
// 剩余的 Remaining；一个份额被多条冻结覆盖时，全部解除后才解冻。
package hold

import (
	"errors"

	"ontology/revenue"
)

// 冻结层的哨兵错误，调用方用 errors.Is 区分。
var (
	ErrHoldExists   = errors.New("hold: hold already exists")
	ErrHoldNotFound = errors.New("hold: hold not found")
)

// Hold 是一条冻结：覆盖 content 中事件时刻落在半开区间 [From, To) 的份额。
type Hold struct {
	ID      string
	Content string
	From    int64
	To      int64
}

// Registry 是冻结注册表；其方法必须在 book 的锁内串行化（自身不加锁）。
type Registry struct {
	book      *revenue.Book
	holds     map[string]*Hold
	byContent map[string][]*Hold
	cmp       int // 非导出：count 中的区间比较次数，供复杂度测试断言
}

// NewRegistry 创建冻结注册表，并向 book 注入 Earn 路径的覆盖计数钩子。
func NewRegistry(book *revenue.Book) *Registry {
	r := &Registry{
		book:      book,
		holds:     make(map[string]*Hold),
		byContent: make(map[string][]*Hold),
	}
	book.SetHoldCounter(r.count)
	return r
}

// count 统计 eventTime 时刻 content 的活动冻结覆盖数（在 book 锁内被 Earn 调用）。
// 比较次数只与该内容的冻结条数有关。
func (r *Registry) count(content string, eventTime int64) int {
	n := 0
	for _, h := range r.byContent[content] {
		r.cmp++
		if h.From <= eventTime && eventTime < h.To {
			n++
		}
	}
	return n
}

// Hold 冻结 content 中事件时刻落在 [from, to) 内的全部份额的 Remaining，
// 包括此后才入账、时刻落在区间内的事件；已付出的部分不追溯。
// 拒绝次序：参数非法 > 时钟回退 > 内容无分成表 > 冻结已存在。
func (r *Registry) Hold(now int64, holdID, content string, from, to int64) error {
	if holdID == "" || content == "" || from < 0 || from >= to || now < 0 || now > 1_000_000_000_000 {
		return revenue.ErrInvalidParam
	}
	return r.book.Do(func() error {
		if err := r.book.CheckClock(now); err != nil {
			return err
		}
		if !r.book.HasSplit(content) {
			return revenue.ErrNoSplit
		}
		if _, ok := r.holds[holdID]; ok {
			return ErrHoldExists
		}
		h := &Hold{ID: holdID, Content: content, From: from, To: to}
		r.holds[holdID] = h
		r.byContent[content] = append(r.byContent[content], h)
		r.apply(h, now, true)
		r.book.AcceptClock(now)
		return nil
	})
}

// Release 解除一条冻结；一个份额被多条冻结覆盖时，全部解除后才解冻。
// 拒绝次序：参数非法 > 时钟回退 > 冻结不存在。
func (r *Registry) Release(now int64, holdID string) error {
	if holdID == "" || now < 0 || now > 1_000_000_000_000 {
		return revenue.ErrInvalidParam
	}
	return r.book.Do(func() error {
		if err := r.book.CheckClock(now); err != nil {
			return err
		}
		h, ok := r.holds[holdID]
		if !ok {
			return ErrHoldNotFound
		}
		delete(r.holds, holdID)
		list := r.byContent[h.Content]
		for i, x := range list {
			if x == h {
				r.byContent[h.Content] = append(list[:i], list[i+1:]...)
				break
			}
		}
		r.apply(h, now, false)
		r.book.AcceptClock(now)
		return nil
	})
}

// apply 对区间覆盖到的事件份额加/减一层冻结，并同步创作者三维聚合。
// 已付清或已退款的份额（Remaining==0）不在活跃表中，跳过即不追溯。
func (r *Registry) apply(h *Hold, now int64, freeze bool) {
	promoted := make(map[*revenue.Creator]bool)
	for _, ev := range r.book.EventsOf(h.Content) {
		if ev.Time < h.From || ev.Time >= h.To {
			continue
		}
		for _, s := range ev.Shares {
			if s.Remaining == 0 {
				continue
			}
			c := s.Owner
			if !promoted[c] {
				c.Promote(now)
				promoted[c] = true
			}
			if freeze {
				if s.Holds == 0 {
					c.Freeze(s, now)
				}
				s.Holds++
			} else {
				s.Holds--
				if s.Holds == 0 {
					c.Unfreeze(s, now)
				}
			}
		}
	}
}

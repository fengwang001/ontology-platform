// Package slot 实现线性频道的常规节目段排入、取消与时间线存储。
package slot

import (
	"errors"
	"fmt"
	"slices"
	"sort"
	"sync"
)

const (
	// MaxTime 时间线上界（秒）。
	MaxTime int64 = 1_000_000_000_000
	// MaxDur 单个段的最大时长（秒）。
	MaxDur int64 = 1_000_000_000
	// MaxFiller 垫片素材长度上界（秒）。
	MaxFiller int64 = 1_000_000
)

var (
	ErrInvalid    = errors.New("slot: invalid argument")
	ErrClock      = errors.New("slot: clock regression")
	ErrIDExists   = errors.New("slot: id already exists")
	ErrIDNotFound = errors.New("slot: id not found")
	ErrPast       = errors.New("slot: start already past")
	ErrOverlap    = errors.New("slot: segment overlap")
	ErrEnded      = errors.New("slot: already ended")
)

// Kind 段类型。
type Kind int8

const (
	Program Kind = iota // 常规节目段
	Shift               // 顺延式插播段
	Preempt             // 抢占式插播段
)

// Segment 时间线上的一段。Offset 为段首对应的节目内偏移（仅常规节目段使用）。
type Segment struct {
	ID     string
	Kind   Kind
	Start  int64
	End    int64
	Offset int64
	Fixed  bool
}

// State 时间线的内部状态，仅在 Update/View 回调内有效。
type State struct {
	Now     int64
	Filler  int64
	Layout  []*Segment // 常规节目段+顺延式插播段，按起点有序、两两不相交
	Preempt []*Segment // 抢占式插播段，按起点有序、两两不相交
	Shifts  []*Segment // Layout 中顺延段的辅助索引（共享指针），按起点有序
	IDs     map[string]bool
}

// Timeline 线性频道时间线，所有操作可并发调用。
type Timeline struct {
	mu sync.RWMutex
	st State
}

// New 构造时间线，filler 为垫片素材长度，须在 [1, MaxFiller] 内。
func New(filler int64) (*Timeline, error) {
	if filler < 1 || filler > MaxFiller {
		return nil, fmt.Errorf("%w: filler length %d", ErrInvalid, filler)
	}
	return &Timeline{st: State{Filler: filler, IDs: make(map[string]bool)}}, nil
}

// Update 在写锁内执行 fn；fn 返回错误时丢弃对 State 的全部结构变更。
// fn 只有在确定成功之后才允许就地修改 Segment 字段或 IDs。
func (tl *Timeline) Update(fn func(*State) error) error {
	tl.mu.Lock()
	defer tl.mu.Unlock()
	st := tl.st
	if err := fn(&st); err != nil {
		return err
	}
	tl.st = st
	return nil
}

// View 在读锁内执行 fn；fn 不得修改 State。
func (tl *Timeline) View(fn func(*State)) {
	tl.mu.RLock()
	defer tl.mu.RUnlock()
	fn(&tl.st)
}

// ValidateSpan 校验排入/插播类操作的公共参数。
func ValidateSpan(id string, now, start, dur int64) error {
	switch {
	case id == "":
		return ErrInvalid
	case now < 0 || now > MaxTime:
		return ErrInvalid
	case start < 0 || start > MaxTime:
		return ErrInvalid
	case dur < 1 || dur > MaxDur || start+dur > MaxTime:
		return ErrInvalid
	}
	return nil
}

// ValidateIDNow 校验取消类操作的公共参数。
func ValidateIDNow(id string, now int64) error {
	if id == "" || now < 0 || now > MaxTime {
		return ErrInvalid
	}
	return nil
}

// Schedule 排入常规节目段 [start, start+dur)。
// 拒绝次序：参数非法 > 时钟回退 > 标识已存在 > 已过去 > 重叠。
func (tl *Timeline) Schedule(now int64, id string, start, dur int64, fixed bool) error {
	if err := ValidateSpan(id, now, start, dur); err != nil {
		return err
	}
	end := start + dur
	return tl.Update(func(st *State) error {
		if now < st.Now {
			return ErrClock
		}
		if st.IDs[id] {
			return ErrIDExists
		}
		if start < now {
			return ErrPast
		}
		// Layout 两两不相交且端点有序，第一个 End > start 的段是唯一可能相交者。
		i := sort.Search(len(st.Layout), func(k int) bool { return st.Layout[k].End > start })
		if i < len(st.Layout) && st.Layout[i].Start < end {
			return ErrOverlap
		}
		st.Layout = slices.Insert(st.Layout, i, &Segment{
			ID: id, Kind: Program, Start: start, End: end, Fixed: fixed,
		})
		st.IDs[id] = true
		st.Now = now
		return nil
	})
}

// Cancel 把 id 的每段终点截到 min(终点, now)，长度不为正的段删除。
// 拒绝次序：参数非法 > 时钟回退 > 标识不存在 > 已结束。
func (tl *Timeline) Cancel(now int64, id string) error {
	if err := ValidateIDNow(id, now); err != nil {
		return err
	}
	return tl.Update(func(st *State) error {
		if now < st.Now {
			return ErrClock
		}
		if !st.IDs[id] {
			return ErrIDNotFound
		}
		alive := false
		for _, segs := range [][]*Segment{st.Layout, st.Preempt} {
			for _, seg := range segs {
				if seg.ID == id && seg.End > now {
					alive = true
				}
			}
		}
		if !alive {
			return ErrEnded
		}
		st.Layout = cancelIn(st.Layout, id, now)
		st.Preempt = cancelIn(st.Preempt, id, now)
		st.Shifts = cancelIn(st.Shifts, id, now)
		st.Now = now
		return nil
	})
}

// cancelIn 截断 id 的各段并删除长度不为正的段，其余段保持原位（不回填、不退回）。
func cancelIn(segs []*Segment, id string, now int64) []*Segment {
	for _, seg := range segs {
		if seg.ID == id && now < seg.End {
			seg.End = now
		}
	}
	return slices.DeleteFunc(segs, func(seg *Segment) bool { return seg.Start >= seg.End })
}

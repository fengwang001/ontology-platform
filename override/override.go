// Package override 实现紧急插播：抢占式与顺延式。
package override

import (
	"errors"
	"slices"
	"sort"
	"sync/atomic"

	"ontology/slot"
)

var (
	ErrConflict      = errors.New("override: override segment conflict")
	ErrFixedInterior = errors.New("override: insertion point inside fixed program")
	ErrFixedSqueeze  = errors.New("override: shift would displace fixed program")
)

// Mode 插播方式。
type Mode int8

const (
	Preempt Mode = iota // 抢占式：覆盖但不移动任何段
	Shift               // 顺延式：把其后内容向后推
)

// moved 统计一次顺延式插播在扫描阶段顺序查看的段数（定位二分不计入）。
var moved atomic.Int64

// Override 在 [start, start+dur) 插入插播段。
// 拒绝次序：参数非法 > 时钟回退 > 标识已存在 > 已过去 > 插播冲突 > 落在固定节目内 > 挤占固定节目。
func Override(tl *slot.Timeline, now int64, id string, start, dur int64, mode Mode) error {
	if mode != Preempt && mode != Shift {
		return slot.ErrInvalid
	}
	if err := slot.ValidateSpan(id, now, start, dur); err != nil {
		return err
	}
	if mode == Preempt {
		return preempt(tl, now, id, start, dur)
	}
	return shift(tl, now, id, start, dur)
}

// preempt 抢占式：覆盖 [start, start+dur)，不移动任何段。
func preempt(tl *slot.Timeline, now int64, id string, start, dur int64) error {
	end := start + dur
	return tl.Update(func(st *slot.State) error {
		if now < st.Now {
			return slot.ErrClock
		}
		if st.IDs[id] {
			return slot.ErrIDExists
		}
		if start < now {
			return slot.ErrPast
		}
		if intersects(st.Preempt, start, end) || intersects(st.Shifts, start, end) {
			return ErrConflict
		}
		i := sort.Search(len(st.Preempt), func(k int) bool { return st.Preempt[k].Start >= start })
		st.Preempt = slices.Insert(st.Preempt, i, &slot.Segment{
			ID: id, Kind: slot.Preempt, Start: start, End: end,
		})
		st.IDs[id] = true
		st.Now = now
		return nil
	})
}

// intersects 报告有序不相交段集合中是否存在与 [start, end) 相交（相接不算）的段。
func intersects(segs []*slot.Segment, start, end int64) bool {
	i := sort.Search(len(segs), func(k int) bool { return segs[k].End > start })
	return i < len(segs) && segs[i].Start < end
}

// anyEndAfter 报告有序不相交段集合中是否存在终点大于 s 的段。
func anyEndAfter(segs []*slot.Segment, s int64) bool {
	i := sort.Search(len(segs), func(k int) bool { return segs[k].End > s })
	return i < len(segs)
}

// shift 顺延式：在 s 处插入长度 d 的插播段，把其后内容向后推，全有或全无。
func shift(tl *slot.Timeline, now int64, id string, s, d int64) error {
	moved.Store(0)
	return tl.Update(func(st *slot.State) error {
		if now < st.Now {
			return slot.ErrClock
		}
		if st.IDs[id] {
			return slot.ErrIDExists
		}
		if s < now {
			return slot.ErrPast
		}
		// 插播冲突：任一既有插播段（抢占或顺延）终点大于 s。
		if anyEndAfter(st.Preempt, s) || anyEndAfter(st.Shifts, s) {
			return ErrConflict
		}
		// 定位：i 为 Layout 中最后一个 Start <= s 的下标。
		i := sort.Search(len(st.Layout), func(k int) bool { return st.Layout[k].Start > s }) - 1
		var containing *slot.Segment
		if i >= 0 && st.Layout[i].End > s {
			containing = st.Layout[i]
		}
		split := false
		j := i + 1 // 顺延扫描起点
		prevEnd := s
		if containing != nil {
			if containing.Fixed {
				return ErrFixedInterior
			}
			if containing.Start < s {
				// s 严格落在浮动节目段内部：切分，续段后移 d。
				split = true
				prevEnd = containing.End
			} else {
				// s 恰等于浮动段起点：该段整体参与后移。
				j = i
			}
		}
		// 只读模拟：空隙吸收顺延量，r>0 时浮动段整段后移 r。
		type mv struct {
			seg *slot.Segment
			by  int64
		}
		var moves []mv
		r := d
		for k := j; k < len(st.Layout) && r > 0; k++ {
			seg := st.Layout[k]
			gap := seg.Start - prevEnd
			if gap > r {
				gap = r
			}
			r -= gap
			moved.Add(1)
			if r == 0 {
				break
			}
			if seg.Fixed {
				return ErrFixedSqueeze
			}
			moves = append(moves, mv{seg, r})
			prevEnd = seg.End
		}
		if split {
			moved.Add(1)
		}
		// 应用：先移动受影响段，再插入续段与新插播段（位置均为 j）。
		for _, m := range moves {
			m.seg.Start += m.by
			m.seg.End += m.by
		}
		if split {
			cont := &slot.Segment{
				ID: containing.ID, Kind: slot.Program,
				Start: s + d, End: containing.End + d,
				Offset: containing.Offset + (s - containing.Start),
			}
			containing.End = s
			st.Layout = slices.Insert(st.Layout, j, cont)
		}
		newSeg := &slot.Segment{ID: id, Kind: slot.Shift, Start: s, End: s + d}
		st.Layout = slices.Insert(st.Layout, j, newSeg)
		si := sort.Search(len(st.Shifts), func(k int) bool { return st.Shifts[k].Start >= s })
		st.Shifts = slices.Insert(st.Shifts, si, newSeg)
		st.IDs[id] = true
		st.Now = now
		return nil
	})
}

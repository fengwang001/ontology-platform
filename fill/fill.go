// Package fill 维护单键「上一个点」状态并做 LOCF 间隙填充。依赖 grid。
package fill

import (
	"errors"

	"ontology/grid"
)

// ErrEmptyKey 点的 Key 为空串。
var ErrEmptyKey = errors.New("fill: empty key")

// errFillRange 防御性哨兵：填充值落在 [prevVal, Val] 之外（题面保证不会发生）。
var errFillRange = errors.New("fill: fill value out of [prevVal, Val]")

type series struct {
	lastTS  int64
	lastVal int64
	view    []grid.Point // 首点到末点的完整填充序列
}

// Filler 按键维护各自的填充序列。probes 非导出，不出现在任何公开接口。
type Filler struct {
	step   int64
	seqs   map[string]*series
	probes int // 最近一次 Feed 中为定位键的「上一个点」而检查的键个数
}

func NewFiller(step int64) *Filler { return &Filler{step: step, seqs: map[string]*series{}} }

// staging 是一批点中某键待落盘的追加内容。
type staging struct {
	lastTS, lastVal int64
	add             []grid.Point
}

// Feed 两阶段：先对整批点全部校验并预计算填充，任一非法则整体拒绝、不留痕。
func (f *Filler) Feed(pts []grid.Point) error {
	f.probes = 0
	st := map[string]*staging{}
	lastOf := func(key string) (int64, int64, bool) {
		if s, ok := st[key]; ok {
			return s.lastTS, s.lastVal, true
		}
		f.probes++ // 哈希定位该键的上一个点，只查一次
		if s, ok := f.seqs[key]; ok {
			return s.lastTS, s.lastVal, true
		}
		return 0, 0, false
	}
	for _, p := range pts {
		if p.Key == "" {
			return ErrEmptyKey
		}
		prevTS, prevVal, hasPrev := lastOf(p.Key)
		if hasPrev {
			if err := grid.CheckPoint(f.step, prevTS, p); err != nil {
				return err
			}
		} else if !grid.OnGrid(f.step, p.TS) {
			return grid.ErrOffGrid
		}
		s := st[p.Key]
		if s == nil {
			s = &staging{}
			st[p.Key] = s
		}
		if hasPrev {
			for _, g := range grid.Gap(f.step, prevTS, p.TS) {
				fv := prevVal                   // LOCF：填充值 == 上一个真实点的值
				if fv < prevVal || fv > p.Val { // 必须落在 [prevVal, Val]
					return errFillRange
				}
				s.add = append(s.add, grid.Point{Key: p.Key, TS: g, Val: fv})
			}
		}
		s.add = append(s.add, p)
		s.lastTS, s.lastVal = p.TS, p.Val
	}
	for key, s := range st { // 阶段二：全部合法，落盘
		sr := f.seqs[key]
		if sr == nil {
			sr = &series{}
			f.seqs[key] = sr
		}
		sr.view = append(sr.view, s.add...)
		sr.lastTS, sr.lastVal = s.lastTS, s.lastVal
	}
	return nil
}

// View 返回该键填充后的完整序列的副本；未知键返回 nil。
func (f *Filler) View(key string) []grid.Point {
	sr := f.seqs[key]
	if sr == nil {
		return nil
	}
	out := make([]grid.Point, len(sr.view))
	copy(out, sr.view)
	return out
}

// Last 返回该键最后一个真实点。
func (f *Filler) Last(key string) (grid.Point, bool) {
	sr := f.seqs[key]
	if sr == nil {
		return grid.Point{}, false
	}
	return grid.Point{Key: key, TS: sr.lastTS, Val: sr.lastVal}, true
}

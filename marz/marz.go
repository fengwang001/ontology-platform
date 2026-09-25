// Package marz 实现单时钟区间的端点生成、扫描线累加与共识区间判定。
// 本包不依赖任何其他包。
package marz

import "sort"

// Event 是扫描线上的一个端点：Delta=+1 表示下端点 L（进入），-1 表示上端点 R（离开）。
type Event struct {
	Pos   int64
	Delta int8
}

// ErrInverted 表示 error<0 导致 lo>hi 的倒置区间。
type sentinelError string

func (e sentinelError) Error() string { return string(e) }

// ErrInverted 是倒置区间的可判定哨兵错误。
var ErrInverted = sentinelError("marz: negative error inverts interval")

// MakeEvents 生成一台时钟 [offset-err, offset+err] 的 L/R 两个端点；err<0 判定为倒置。
func MakeEvents(offset, err int64) (lo, hi Event, e error) {
	if err < 0 {
		return Event{}, Event{}, ErrInverted
	}
	return Event{Pos: offset - err, Delta: +1}, Event{Pos: offset + err, Delta: -1}, nil
}

// SortEvents 按位置升序原地排序；同一位置 L（Delta=+1）排在 R（Delta=-1）之前，
// 从而保证闭区间端点上的共识不丢失。
func SortEvents(ev []Event) {
	sort.SliceStable(ev, func(i, j int) bool {
		if ev[i].Pos != ev[j].Pos {
			return ev[i].Pos < ev[j].Pos
		}
		return ev[i].Delta > ev[j].Delta // 同位 +1 在 -1 前
	})
}

// Sweep 对已排序端点跑扫描线，返回 count>=need（need 必须 >=1）覆盖的最短闭区间；
// 不存在任何候选段时 ok=false。闭区间语义：离开事件 R 的位置本身仍属于该时钟。
func Sweep(ev []Event, need int) (lo, hi int64, ok bool) {
	count := 0
	inCand := false
	candLo := int64(0)
	bestLo, bestHi, bestLen := int64(0), int64(0), int64(0)
	closeAt := func(candHi int64) {
		l := candHi - candLo
		if !ok || l < bestLen {
			bestLo, bestHi, bestLen = candLo, candHi, l
		}
		ok = true
	}
	for _, e := range ev {
		prev := count
		count += int(e.Delta)
		switch {
		case !inCand && prev < need && count >= need:
			candLo, inCand = e.Pos, true // 进入候选：L 位置计入
		case inCand && count < need:
			closeAt(e.Pos) // 离开候选：R 位置（闭端点）仍是上界
			inCand = false
		}
	}
	if !ok {
		return 0, 0, false
	}
	return bestLo, bestHi, true
}

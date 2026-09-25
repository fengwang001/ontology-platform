// Package grid 负责网格归属与合法性判定、间隙缺失时刻枚举。不依赖其他包。
package grid

import "errors"

var (
	// ErrOffGrid 时间戳不在网格上（TS % step != 0）。
	ErrOffGrid = errors.New("grid: timestamp off grid")
	// ErrNotIncreasing 时间戳不严格递增。
	ErrNotIncreasing = errors.New("grid: timestamps not strictly increasing")
)

// Point 是一个指标点。Key 为空串由上层判定，grid 不管键的语义。
type Point struct {
	Key string
	TS  int64
	Val int64
}

// OnGrid 判定 ts 是否落在步长为 step 的网格上。step 必须为正。
func OnGrid(step, ts int64) bool { return ts%step == 0 }

// CheckPoint 校验 p 相对上一个时间戳 prevTS 的合法性（on-grid 且严格递增）。
func CheckPoint(step, prevTS int64, p Point) error {
	if !OnGrid(step, p.TS) {
		return ErrOffGrid
	}
	if p.TS <= prevTS {
		return ErrNotIncreasing
	}
	return nil
}

// Gap 枚举开区间 (prevTS, ts) 内的全部网格时刻（升序）。相邻两点返回空。
func Gap(step, prevTS, ts int64) []int64 {
	var out []int64
	for g := prevTS + step; g < ts; g += step {
		out = append(out, g)
	}
	return out
}

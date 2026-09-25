// Package period 维护单任务的周期性状态并按模式推进。
package period

import "ontology/fire"

// Task 单任务周期状态：上次结束时刻与已执行次数。
type Task struct {
	mode     fire.Mode
	interval int64
	lastEnd  int64
	runs     int64
}

// New 创建任务。调用方保证 mode 合法、interval > 0。
func New(mode fire.Mode, interval int64) *Task {
	return &Task{mode: mode, interval: interval}
}

// Run 执行一次，返回本次 (开始, 结束)。调用方保证 duration >= 0。
// rate：开始 = 第一个 ≥ 上次结束的网格点；delay：首次为 0，其后 = 上次结束 + interval。
func (t *Task) Run(duration int64) (start, end int64) {
	if t.mode == fire.Rate {
		start = fire.NextRate(t.lastEnd, t.interval)
	} else if t.runs == 0 {
		start = 0
	} else {
		start = fire.NextDelay(t.lastEnd, t.interval)
	}
	end = start + duration
	t.lastEnd = end
	t.runs++
	return start, end
}

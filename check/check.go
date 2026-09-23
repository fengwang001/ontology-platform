// Package check 提供影子模型与轨迹断言，用于验证 exec.Executor 的语义。
// 所有测试都放在本包。时间只走注入时钟 Clock，测试不使用 time.Sleep。
package check

import (
	"errors"
	"fmt"
	"sort"
	"sync"

	"ontology/exec"
)

// Clock 是单调递增的逻辑时钟（注入时钟，非墙上时间）。
type Clock struct {
	mu sync.Mutex
	t  int64
}

func (c *Clock) Now() int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t++
	return c.t
}

// Event 记录单个任务的执行轨迹：键、键内序号、半开区间 [Start,End)。
type Event struct {
	Key        string
	Seq        int
	Start, End int64
}

// Trace 是并发安全的轨迹收集器，并实时统计全局并发上界。
type Trace struct {
	mu       sync.Mutex
	Events   []Event
	cur, max int
}

func (tr *Trace) record(ev Event) {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	tr.Events = append(tr.Events, ev)
}

// Begin/End 由任务调用，cur/max 用逻辑区间在 Assert 中复核，这里记录实时上界。
func (tr *Trace) Begin() {
	tr.mu.Lock()
	tr.cur++
	if tr.cur > tr.max {
		tr.max = tr.cur
	}
	tr.mu.Unlock()
}

func (tr *Trace) End() {
	tr.mu.Lock()
	tr.cur--
	tr.mu.Unlock()
}

// Snapshot 返回轨迹副本与实时观测到的最大并发。
func (tr *Trace) Snapshot() ([]Event, int) {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	out := append([]Event(nil), tr.Events...)
	return out, tr.max
}

// Assert 用影子模型核对：同键序号严格递增且区间不重叠、全局并发 ≤ n、不丢不重。
// want 是「键 -> 期望任务序号集合」的影子模型。
func Assert(events []Event, want map[string]int, n int, observedMax int) error {
	byKey := map[string][]Event{}
	seen := map[string]map[int]bool{}
	for _, ev := range events {
		if ev.End <= ev.Start {
			return fmt.Errorf("非正区间 %+v", ev)
		}
		if seen[ev.Key] == nil {
			seen[ev.Key] = map[int]bool{}
		}
		if seen[ev.Key][ev.Seq] {
			return fmt.Errorf("键 %s 序号 %d 重复执行", ev.Key, ev.Seq)
		}
		seen[ev.Key][ev.Seq] = true
		byKey[ev.Key] = append(byKey[ev.Key], ev)
	}
	for key, count := range want {
		if len(byKey[key]) != count {
			return fmt.Errorf("键 %s 任务数 %d != 期望 %d（丢失/多余）", key, len(byKey[key]), count)
		}
	}
	for key, evs := range byKey {
		sort.Slice(evs, func(i, j int) bool { return evs[i].Start < evs[j].Start })
		for i, ev := range evs {
			if ev.Seq != i {
				return fmt.Errorf("键 %s 序号非严格递增/不连续: %+v", key, evs)
			}
			if i > 0 && ev.Start < evs[i-1].End {
				return fmt.Errorf("键 %s 任务区间重叠", key)
			}
		}
	}
	maxConc := maxOverlap(events)
	if maxConc > n || observedMax > n {
		return fmt.Errorf("全局并发 %d(区间)/%d(实时) 超过 n=%d", maxConc, observedMax, n)
	}
	return nil
}

func maxOverlap(events []Event) int {
	type pt struct {
		t int64
		d int
	}
	var pts []pt
	for _, ev := range events {
		pts = append(pts, pt{ev.Start, 1}, pt{ev.End, -1})
	}
	sort.Slice(pts, func(i, j int) bool {
		if pts[i].t != pts[j].t {
			return pts[i].t < pts[j].t
		}
		return pts[i].d < pts[j].d // 同一点先结束后开始，半开区间
	})
	cur, max := 0, 0
	for _, p := range pts {
		cur += p.d
		if cur > max {
			max = cur
		}
	}
	return max
}

// New 是便捷构造，避免调用方直接依赖 exec 的签名变化。
func New(n int) *exec.Executor { return exec.New(n) }

var ErrSentinel = errors.New("check: marker")

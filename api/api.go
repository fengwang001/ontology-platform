// Package api 是迟到事件侧输出分流的对外入口，仅依赖 route 包。
package api

import (
	"errors"
	"fmt"

	"ontology/route"
)

// 事件类型直接复用 route 的定义：Event 主路，SideEvent 带 gap 的侧路。
type (
	Event     = route.Event
	SideEvent = route.SideEvent
)

// 三类可判定、互不相同的哨兵错误。
var (
	// ErrEmptyKey：Key 为空串。
	ErrEmptyKey = errors.New("api: event key must not be empty")
	// ErrNegativeTS：事件时间为负。
	ErrNegativeTS = errors.New("api: event timestamp must not be negative")
	// ErrSideFull：侧路事件数已达 maxSide。
	ErrSideFull = errors.New("api: side output capacity exceeded")
)

// API 是并发安全的分流器句柄，全部状态在进程内存。
type API struct {
	r *route.Router
}

// New 创建侧路容量为 maxSide 的分流器。
func New(maxSide int) *API { return &API{r: route.NewRouter(maxSide)} }

// Feed 喂入一条事件；非法输入或侧路溢出时整体失败、不留状态痕迹。
func (a *API) Feed(key string, ts int64) error {
	err := a.r.Feed(key, ts)
	switch {
	case errors.Is(err, route.ErrEmptyKey):
		return ErrEmptyKey
	case errors.Is(err, route.ErrNegativeTS):
		return ErrNegativeTS
	case errors.Is(err, route.ErrSideFull):
		return ErrSideFull
	default:
		return err
	}
}

// Main 返回主路事件副本（按到达顺序）。
func (a *API) Main() []Event { return a.r.Main() }

// Side 返回侧路事件副本（按到达顺序，带 gap）。
func (a *API) Side() []SideEvent { return a.r.Side() }

// View 返回 view[key]=该 Key 当前水位的副本（只由主路决定）。
func (a *API) View() map[string]int64 { return a.r.View() }

// SelfCheck 用第三节的内置八事件序列核验四条不变量，并额外核验
// 「失败不留痕、拒绝后仍可正常使用」。它在内部新建分流器上执行，
// 不改变接收者状态。
func (a *API) SelfCheck() error {
	seq := []struct {
		k  string
		ts int64
	}{{"A", 10}, {"B", 20}, {"A", 15}, {"A", 15}, {"B", 20}, {"B", 25}, {"A", 12}, {"A", 10}}
	c := New(8)
	ow := map[string]int64{}       // 独立重放的每 Key 水位
	batchMax := map[string]int64{} // 含迟到事件的批量最大 TS
	nMain, nSide := 0, 0
	for _, e := range seq {
		v, seen := ow[e.k]
		wantMain := !seen || e.ts >= v
		wantGap := int64(0)
		if !wantMain {
			wantGap = v - e.ts
		}
		if err := c.Feed(e.k, e.ts); err != nil {
			return fmt.Errorf("selfcheck feed %v: %w", e, err)
		}
		if wantMain {
			nMain++
			if !seen || e.ts > v {
				ow[e.k] = e.ts
			}
			last := c.Main()[nMain-1]
			if last.Key != e.k || last.TS != e.ts {
				return fmt.Errorf("selfcheck main mismatch at %v", e)
			}
		} else {
			nSide++
			last := c.Side()[nSide-1]
			if last.Key != e.k || last.TS != e.ts || last.Gap != wantGap || last.Gap <= 0 {
				return fmt.Errorf("selfcheck side mismatch at %v", e)
			}
		}
		if m, ok := batchMax[e.k]; !ok || e.ts > m {
			batchMax[e.k] = e.ts
		}
		if len(c.Main()) != nMain || len(c.Side()) != nSide {
			return fmt.Errorf("selfcheck counts mismatch at %v", e)
		}
	}
	for k, want := range batchMax { // 不变量 1：视图 == 批量最大
		if got := c.View()[k]; got != want {
			return fmt.Errorf("selfcheck view[%s]=%d want batch max %d", k, got, want)
		}
	}
	return checkRejection()
}

// checkRejection 核验三类拒绝互异、不留痕、拒绝后仍可继续。
func checkRejection() error {
	d := New(8)
	snap := func() string { return fmt.Sprintf("%v|%v|%v", d.View(), d.Main(), d.Side()) }
	for _, c := range []struct {
		key  string
		ts   int64
		want error
	}{{"", 1, ErrEmptyKey}, {"k", -1, ErrNegativeTS}} {
		before := snap()
		if err := d.Feed(c.key, c.ts); !errors.Is(err, c.want) {
			return fmt.Errorf("reject (%q,%d): got %v want %v", c.key, c.ts, err, c.want)
		}
		if after := snap(); after != before {
			return fmt.Errorf("reject (%q,%d) changed state: %s -> %s", c.key, c.ts, before, after)
		}
	}
	d2 := New(1) // maxSide=1：第二条迟到须被拒
	for _, f := range []struct {
		key string
		ts  int64
	}{{"X", 5}, {"X", 3}} {
		if err := d2.Feed(f.key, f.ts); err != nil {
			return err
		}
	}
	before := fmt.Sprintf("%v|%v|%v", d2.View(), d2.Main(), d2.Side())
	if err := d2.Feed("X", 1); !errors.Is(err, ErrSideFull) {
		return fmt.Errorf("overflow: got %v want ErrSideFull", err)
	}
	if after := fmt.Sprintf("%v|%v|%v", d2.View(), d2.Main(), d2.Side()); after != before {
		return fmt.Errorf("overflow changed state: %s -> %s", before, after)
	}
	if err := d2.Feed("Z", 7); err != nil || d2.View()["Z"] != 7 {
		return fmt.Errorf("router unusable after rejection: %v view=%v", err, d2.View())
	}
	return nil
}

// Package api 是流式 SUM 聚合的对外入口：New/Feed/View/Spills/Loads/SelfCheck。
// 所有方法并发安全；依赖方向 api → agg → spill，不反向依赖。
package api

import (
	"errors"
	"math"
	"reflect"
	"sync"

	"ontology/agg"
)

// 三类互不相同的哨兵错误：构造期、事件非法、求和溢出。
var (
	ErrInvalidLimit = errors.New("api: limit must be positive")
	ErrEmptyKey     = agg.ErrEmptyKey
	ErrOverflow     = agg.ErrOverflow
)

// Event 是上游的一个 (Key, Val) 事件。
type Event struct {
	Key string
	Val int64
}

// Aggregator 以固定常驻上限做流式求和聚合。
type Aggregator struct {
	mu    sync.RWMutex
	inner *agg.Aggregator
}

// New 创建常驻上限为 limit 的聚合器；limit 非正整体拒绝（ErrInvalidLimit）。
func New(limit int) (*Aggregator, error) {
	if limit <= 0 {
		return nil, ErrInvalidLimit
	}
	return &Aggregator{inner: agg.New(limit)}, nil
}

func toAgg(evs []Event) []agg.Event {
	out := make([]agg.Event, len(evs))
	for i, e := range evs {
		out[i] = agg.Event{Key: e.Key, Val: e.Val}
	}
	return out
}

// Feed 应用一批事件；任一条非法则整批不生效，返回可判定的哨兵错误。
func (a *Aggregator) Feed(evs []Event) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.inner.Feed(toAgg(evs))
}

// View 返回每个 Key 的累加和（独立副本）。
func (a *Aggregator) View() map[string]int64 {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.inner.View()
}

// Spills 返回累计溢写次数。
func (a *Aggregator) Spills() int {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.inner.Spills()
}

// Loads 返回累计回载次数。
func (a *Aggregator) Loads() int {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.inner.Loads()
}

func builtinEvents() []Event {
	return []Event{
		{"a", 5}, {"b", 3}, {"c", 7}, {"d", 2},
		{"b", 4}, {"a", 1}, {"c", 2}, {"d", 5},
	}
}

func batchSum(evs []Event) map[string]int64 {
	m := map[string]int64{}
	for _, e := range evs {
		m[e.Key] += e.Val
	}
	return m
}

// SelfCheck 对内置八步序列核验第二节四条不变量；全部成立返回 nil。
// 它不触碰接收者状态，可被多个 goroutine 并发调用。
func (a *Aggregator) SelfCheck() error {
	evs := builtinEvents()

	// 不变量2（阈值）+「任一 Key 至多一处」：逐步检查。
	tr := agg.StepTrace(3, toAgg(evs))
	for _, st := range tr {
		if len(st.Resident) > 3 {
			return errors.New("SelfCheck: resident groups exceed limit")
		}
		for k := range st.Resident {
			if _, dup := st.Spilled[k]; dup {
				return errors.New("SelfCheck: key in both resident and spilled")
			}
		}
	}
	// 不变量3（回载合并不丢不重）：第6步 (a,1) 后 a 常驻值必须为 5+1=6。
	if tr[5].Resident["a"] != 6 {
		return errors.New("SelfCheck: reload lost spilled partial sum")
	}
	// 不变量1（与批量重算一致）。
	fresh, _ := New(3)
	if err := fresh.Feed(evs); err != nil {
		return err
	}
	if !reflect.DeepEqual(fresh.View(), batchSum(evs)) {
		return errors.New("SelfCheck: view differs from batch recomputation")
	}
	// 不变量4（失败不留痕）：三类哨兵互不相同，拒绝后状态与计数不变。
	if _, err := New(0); !errors.Is(err, ErrInvalidLimit) {
		return errors.New("SelfCheck: invalid limit not rejected")
	}
	bad := []struct {
		evs  []Event
		want error
	}{
		{evs: []Event{{Key: "", Val: 1}}, want: ErrEmptyKey},
		{evs: []Event{{Key: "z", Val: math.MaxInt64}, {Key: "z", Val: 1}}, want: ErrOverflow},
	}
	b, _ := New(3)
	_ = b.Feed([]Event{{"a", 1}, {"b", 2}, {"c", 3}, {"d", 4}})
	snap, sp, ld := b.View(), b.Spills(), b.Loads()
	for _, c := range bad {
		if err := b.Feed(c.evs); !errors.Is(err, c.want) {
			return errors.New("SelfCheck: rejected event not surfaced as sentinel")
		}
	}
	if !reflect.DeepEqual(b.View(), snap) || b.Spills() != sp || b.Loads() != ld {
		return errors.New("SelfCheck: rejected batch left state behind")
	}
	return nil
}

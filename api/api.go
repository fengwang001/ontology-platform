// Package api 是对外接口层：New 构建实例、Feed 喂事件、SelfCheck 自检。依赖 pipe。
package api

import (
	"fmt"
	"slices"
	"sync"

	"ontology/pipe"
	"ontology/pred"
)

// 对外的类型别名。
type (
	Op    = pipe.Op
	Event = pred.Event
	Out   = pred.Event
)

// API 是一个已规划的流水线实例。Feed 之外的只读接口可并发调用。
type API struct {
	mu   sync.RWMutex
	pl   *pipe.Pipeline
	last []Out // 最近一次 Feed 的输出
}

// New 校验全部谓词并构建下推计划；任一谓词非法则整体失败。
func New(ops []Op) (*API, error) {
	p, err := new(pipe.Planner).Build(ops)
	if err != nil {
		return nil, err
	}
	return &API{pl: p}, nil
}

// Feed 喂一批事件：任一事件非法则整批不生效（失败不留痕）。
func (a *API) Feed(evs []Event) ([]Out, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	out, err := a.pl.Feed(evs)
	if err != nil {
		return nil, err
	}
	a.last = out
	return out, nil
}

// Out 返回最近一次 Feed 的输出副本，可并发调用。
func (a *API) Out() []Out {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return slices.Clone(a.last)
}

// demoOps 返回全新的演示链 F1→S→F2（含独立状态）。
func demoOps() []Op {
	return []Op{{Name: "F1", Pred: pred.Even()}, {Name: "S", State: &pred.RecordHigh{}},
		{Name: "F2", Pred: pred.KindIs("A")}}
}

// selfSeqs 是内置事件序列：第三节六事件 + 两组确定性生成序列。
func selfSeqs() [][]Event {
	seqs := [][]Event{{
		{Seq: 1, Val: 10, Kind: "A"}, {Seq: 2, Val: 25, Kind: "A"}, {Seq: 3, Val: 18, Kind: "A"},
		{Seq: 4, Val: 30, Kind: "B"}, {Seq: 5, Val: 22, Kind: "A"}, {Seq: 6, Val: 40, Kind: "A"},
	}}
	for n := 0; n < 2; n++ {
		var evs []Event
		for i := 1; i <= 40; i++ {
			evs = append(evs, Event{Seq: int64(i), Val: int64((i*7 + n*13) % 50),
				Kind: string(rune('A' + (i+n)%2))})
		}
		seqs = append(seqs, evs)
	}
	return seqs
}

// feedNew 新建一条优化链并喂入 evs。
func feedNew(ops []Op, evs []Event) ([]Out, error) {
	a, err := New(ops)
	if err != nil {
		return nil, err
	}
	return a.Feed(evs)
}

// SelfCheck 对内置事件序列核验四条不变量，全部通过返回 nil。
// 只使用局部新建的实例，可并发调用。
func SelfCheck() error {
	// 不变量 1：优化链与朴素链逐事件一致。
	for i, evs := range selfSeqs() {
		naive, err := pipe.Naive(demoOps())
		if err != nil {
			return err
		}
		want, err := naive.Feed(evs)
		if err != nil {
			return err
		}
		got, err := feedNew(demoOps(), evs)
		if err != nil {
			return err
		}
		if !slices.Equal(want, got) {
			return fmt.Errorf("不变量1 序列%d: 优化链 %v != 朴素链 %v", i, got, want)
		}
	}
	// 不变量 2：同段无状态过滤器重排/合并后输出不变。
	seg := func(ps ...pred.Pred) []Op {
		ops := []Op{{Name: "S", State: &pred.RecordHigh{}}}
		for _, p := range ps {
			ops = append([]Op{{Name: p.Name, Pred: p}}, ops...)
		}
		return ops
	}
	for i, evs := range selfSeqs() {
		ga, err := feedNew(seg(pred.Even(), pred.KindIs("A")), evs)
		if err != nil {
			return err
		}
		gb, err := feedNew(seg(pred.KindIs("A"), pred.Even()), evs)
		if err != nil {
			return err
		}
		if !slices.Equal(ga, gb) {
			return fmt.Errorf("不变量2 序列%d: 重排后输出不同", i)
		}
	}
	// 不变量 3：跨屏障移动被拒；状态过滤器不可移动。
	if ops := demoOps(); pipe.ValidateMove(ops, 2, 0) == nil || pipe.ValidateMove(ops, 1, 0) == nil {
		return fmt.Errorf("不变量3: 跨屏障/状态过滤器移动未被拒绝")
	}
	// 不变量 4：被拒操作不留痕——非法事件整批拒绝，状态与输出不变。
	a, err := New([]Op{{Name: "S", State: &pred.RecordHigh{}}})
	if err != nil {
		return err
	}
	if _, err = a.Feed([]Event{{Seq: 1, Val: 10, Kind: "A"}}); err != nil {
		return err
	}
	before := a.Out()
	bad := []Event{{Seq: 2, Val: 100, Kind: "A"}, {Seq: 0, Val: 1, Kind: "A"}}
	if _, err = a.Feed(bad); err == nil || !slices.Equal(a.Out(), before) {
		return fmt.Errorf("不变量4: 非法事件未被拒绝或留下了痕迹")
	}
	if got, err := a.Feed([]Event{{Seq: 3, Val: 50, Kind: "A"}}); err != nil || len(got) != 1 {
		return fmt.Errorf("不变量4: 被拒批次改动了状态")
	}
	return nil
}

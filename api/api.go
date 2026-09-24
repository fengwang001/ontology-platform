// Package api 是异步检查点屏障快照算子的对外门面：
// New / Feed / Sum / Snapshot / SelfCheck，全部并发安全，状态只在进程内存。
package api

import (
	"errors"
	"fmt"
	"strings"
	"sync"

	"ontology/snap"
)

// Kind 区分两类事件。
type Kind int

const (
	// Rec 是一条增量记录，Value 为有符号 delta。
	Rec Kind = iota
	// Bar 是一条检查点屏障，Value 为在同一通道上严格递增的正整数 id。
	Bar
)

// Event 是流上的一个事件。
type Event struct {
	Kind  Kind
	Ch    int
	Value int
}

// 四类可判定、互不相同的哨兵错误（直接复用 snap 层的同一组哨兵）。
var (
	ErrInvalidChannels    = snap.ErrInvalidChannels
	ErrChannelOutOfRange  = snap.ErrChannelOutOfRange
	ErrBarrierNonPositive = snap.ErrBarrierNonPositive
	ErrBarrierOutOfOrder  = snap.ErrBarrierOutOfOrder
	// ErrSelfCheck 表示内置序列未能满足某条不变量。
	ErrSelfCheck = errors.New("api: self-check failed")
)

// API 是并发安全的算子句柄。
type API struct {
	mu sync.RWMutex
	e  *snap.Engine
}

// New 创建 channels 条输入通道的算子；channels 非正返回 ErrInvalidChannels。
func New(channels int) (*API, error) {
	e, err := snap.NewEngine(channels)
	if err != nil {
		return nil, err
	}
	return &API{e: e}, nil
}

// Feed 按全局到达顺序整批喂入事件；任一条非法则整批不生效。
func (a *API) Feed(evs []Event) error {
	inner := make([]snap.Event, len(evs))
	for i, ev := range evs {
		if ev.Kind != Rec && ev.Kind != Bar {
			return ErrChannelOutOfRange
		}
		inner[i] = snap.Event{Kind: snap.Kind(ev.Kind), Ch: ev.Ch, V: ev.Value}
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.e.Feed(inner)
}

// Sum 返回当前累加器值（只读，并发安全）。
func (a *API) Sum() int {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.e.Sum()
}

// Snapshot 返回 id 的快照；未对齐完成时 ok 为 false（只读，并发安全）。
func (a *API) Snapshot(id int) (sum int, ok bool) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.e.Snapshot(id)
}

// SelfCheck 对一组内置事件序列核验第二节四条不变量，全部满足返回 nil。
// 它在独立引擎上运行，不改动接收者状态；并发只读安全。
func (a *API) SelfCheck() error {
	a.mu.RLock()
	defer a.mu.RUnlock()

	var fails []string
	// 第三节内置八事件。
	seq := []snap.Event{
		{Kind: snap.Rec, Ch: 0, V: 5},
		{Kind: snap.Rec, Ch: 1, V: 10},
		{Kind: snap.Bar, Ch: 0, V: 1},
		{Kind: snap.Rec, Ch: 0, V: 7},
		{Kind: snap.Rec, Ch: 1, V: 3},
		{Kind: snap.Bar, Ch: 1, V: 1},
		{Kind: snap.Rec, Ch: 0, V: 2},
		{Kind: snap.Rec, Ch: 1, V: 1},
	}
	e, _ := snap.NewEngine(2)
	sums := []int{5, 15, 15, 15, 18, 25, 27, 28}
	total := 0
	for i, ev := range seq {
		if err := e.Feed([]snap.Event{ev}); err != nil {
			fails = append(fails, fmt.Sprintf("step %d unexpected error: %v", i+1, err))
		}
		if e.Sum() != sums[i] {
			fails = append(fails, fmt.Sprintf("invariant 2/3: step %d sum=%d want %d", i+1, e.Sum(), sums[i]))
		}
		if ev.Kind == snap.Rec {
			total += ev.V
		}
	}
	// 不变量 1：snap[1] 与批量参照一致（18）。
	if v, ok := e.Snapshot(1); !ok || v != 18 {
		fails = append(fails, fmt.Sprintf("invariant 1: snap[1]=%d ok=%v want 18", v, ok))
	}
	// 不变量 2：终值等于全部记录之和 28，无丢失无重复。
	if e.Sum() != total || e.Sum() != 28 {
		fails = append(fails, "invariant 2: final sum mismatch")
	}
	// 不变量 4：被拒整批不留痕。
	bad := []snap.Event{
		{Kind: snap.Rec, Ch: 0, V: 999},
		{Kind: snap.Bar, Ch: 0, V: -1}, // 非法：id 非正，前面的 +999 也不得落状态
	}
	if err := e.Feed(bad); !errors.Is(err, snap.ErrBarrierNonPositive) {
		fails = append(fails, "invariant 4: expected rejection")
	}
	if e.Sum() != 28 {
		fails = append(fails, "invariant 4: rejected batch changed sum")
	}
	if _, ok := e.Snapshot(-1); ok {
		fails = append(fails, "invariant 4: phantom snapshot")
	}
	if len(fails) > 0 {
		return fmt.Errorf("%w: %s", ErrSelfCheck, strings.Join(fails, "; "))
	}
	return nil
}

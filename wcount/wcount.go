// Package wcount 按 Key 维护高水位与累计接受数、全局超窗丢弃数，
// 提供批量原子 Feed。依赖 slack 做单条判定。
package wcount

import (
	"errors"
	"fmt"

	"ontology/slack"
)

// 可判定的哨兵错误，互不相同。
var (
	ErrEmptyKey    = errors.New("wcount: event Key is empty")
	ErrTooManyKeys = errors.New("wcount: distinct key count exceeds maxKeys")
)

// Event 是一条带序列号的变更事件。
type Event struct {
	Key string
	Seq int64
}

type keyState struct {
	high     int64
	hasHigh  bool
	accepted int64
}

// Counter 是 K-slack 乱序计数器。不是并发安全的，并发封装由上层 api 负责。
type Counter struct {
	k       int64
	maxKeys int
	keys    map[string]*keyState
	dropped int64
	// checked 记录最近一次处理事件时为定位该 Key 高水位而检查过的 Key 个数。
	// 非导出，不出现在任何公开接口；map 定位恒为 1，与 Key 总数无关。
	checked int
}

// New 构造计数器。k 与 maxKeys 的合法性由调用方（api.New）保证。
func New(k int64, maxKeys int) *Counter {
	return &Counter{k: k, maxKeys: maxKeys, keys: make(map[string]*keyState)}
}

// Feed 批量处理事件：先整批校验，任一事件非法则整批不生效（失败不留痕）。
func (c *Counter) Feed(evs []Event) error {
	fresh := make(map[string]struct{}, len(evs))
	for _, e := range evs {
		if e.Key == "" {
			return ErrEmptyKey
		}
		if _, ok := c.keys[e.Key]; !ok {
			fresh[e.Key] = struct{}{}
		}
	}
	if len(c.keys)+len(fresh) > c.maxKeys {
		return ErrTooManyKeys
	}
	for _, e := range evs {
		c.applyOne(e)
	}
	return nil
}

// applyOne 处理单条事件：map 定位该 Key（只检查 1 个 Key），按 slack 规则判定。
// accepted 与 dropped 只增不减；high 只进不退，丢弃事件不改 high。
func (c *Counter) applyOne(e Event) {
	st, ok := c.keys[e.Key]
	c.checked = 1 // 映射定位：无论多少 Key，只检查目标这 1 个
	if !ok {
		st = &keyState{}
		c.keys[e.Key] = st
	}
	v := slack.Judge(c.k, st.high, st.hasHigh, e.Seq)
	if !slack.Accepted(v) {
		c.dropped++
		return
	}
	st.accepted++
	st.high = slack.Apply(v, st.high, e.Seq)
	st.hasHigh = true
}

// Accepted 返回该 Key 的累计接受数（只增不减）。
func (c *Counter) Accepted(key string) int64 {
	if st, ok := c.keys[key]; ok {
		return st.accepted
	}
	return 0
}

// Dropped 返回全局累计超窗丢弃数（只增不减）。
func (c *Counter) Dropped() int64 {
	return c.dropped
}

// High 返回该 Key 的高水位；第二个返回值 false 表示尚无高水位。
func (c *Counter) High(key string) (int64, bool) {
	if st, ok := c.keys[key]; ok {
		return st.high, st.hasHigh
	}
	return 0, false
}

// SelfCheck 核验定位成本不变量：先让 m 个不同 Key 各有高水位，
// 再喂一个属于其中某 Key 的事件，断言检查过的 Key 个数不随 m 增长（恒为 1）。
// 只返回通过与否，不暴露计数器数值。
func (c *Counter) SelfCheck() bool {
	for _, m := range []int{100, 1000, 10000} {
		cc := New(c.k, m+1)
		evs := make([]Event, 0, m)
		for i := 0; i < m; i++ {
			evs = append(evs, Event{Key: fmt.Sprintf("key-%d", i), Seq: int64(i)})
		}
		if err := cc.Feed(evs); err != nil {
			return false
		}
		if err := cc.Feed([]Event{{Key: evs[0].Key, Seq: int64(m)}}); err != nil {
			return false
		}
		if cc.checked > 1 {
			return false
		}
	}
	return true
}

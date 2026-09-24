// Package api 是事件时间去重窗口的对外接口，依赖 dedup。
package api

import (
	"errors"
	"fmt"
	"reflect"
	"sync"

	"ontology/dedup"
)

// Event 是上游事件。
type Event = dedup.Event

// 三类哨兵错误（与 dedup 同一实例，可 errors.Is 判定）。
var (
	ErrInvalidParam = dedup.ErrInvalidParam
	ErrEmptyID      = dedup.ErrEmptyID
	ErrTooMany      = dedup.ErrTooMany
)

// Deduper 是并发安全的去重器。
type Deduper struct {
	mu      sync.Mutex
	t       *dedup.Table
	emitted []Event
}

// New 创建去重器：ttl、maxIDs 必须为正，delay 不得为负。
func New(ttl, delay int64, maxIDs int) (*Deduper, error) {
	t, err := dedup.NewTable(ttl, delay, maxIDs)
	if err != nil {
		return nil, err
	}
	return &Deduper{t: t}, nil
}

// Feed 处理一批事件；任一条被拒则整批不生效（水位线/记忆/计数/已输出全不变）。
func (d *Deduper) Feed(evs []Event) ([]Event, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	out, err := d.t.Apply(evs)
	if err != nil {
		return nil, err
	}
	d.emitted = append(d.emitted, out...)
	return append([]Event(nil), out...), nil
}

// Emitted 返回累计已输出新事件的副本。
func (d *Deduper) Emitted() []Event {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]Event(nil), d.emitted...)
}
func (d *Deduper) Dups() int64 { d.mu.Lock(); defer d.mu.Unlock(); return d.t.Dups() }
func (d *Deduper) Mem() map[string]int64 {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.t.Mem()
}
func (d *Deduper) Watermark() (int64, bool) { d.mu.Lock(); defer d.mu.Unlock(); return d.t.Watermark() }

// SelfCheck 用第三节内置十序列在独立新表上核验四条不变量，不改变接收者状态。
func (d *Deduper) SelfCheck() error {
	seq := []Event{{ID: "a", TS: 5}, {ID: "b", TS: 8}, {ID: "a", TS: 9}, {ID: "c", TS: 17},
		{ID: "a", TS: 14}, {ID: "b", TS: 20}, {ID: "d", TS: 4}, {ID: "d", TS: 6},
		{ID: "c", TS: 26}, {ID: "a", TS: 25}}
	c, err := dedup.NewTable(10, 2, 1000)
	if err != nil {
		return err
	}
	type hr struct {
		id string
		ts int64
	}
	var hist []hr // 朴素参照：全部历史新事件
	maxTS, have := int64(0), false
	for i, ev := range seq {
		out, e := c.Apply([]Event{ev})
		if e != nil {
			return e
		}
		if !have || ev.TS > maxTS {
			maxTS = ev.TS
		}
		have = true
		wm := maxTS - 2
		dup := false // 倒序找该 ID 最近新事件，未过期即重复
		for j := len(hist) - 1; j >= 0; j-- {
			if hist[j].id == ev.ID {
				dup = wm < hist[j].ts+10
				break
			}
		}
		if dup != (len(out) == 0) {
			return fmt.Errorf("step %d: 与朴素参照不一致", i+1) // 不变量1
		}
		if !dup {
			hist = append(hist, hr{ev.ID, ev.TS})
		}
		if gv, ok := c.Watermark(); !ok || gv != wm {
			return fmt.Errorf("step %d: 水位线不符/回退", i+1) // 不变量3
		}
		want := map[string]int64{} // 参照未过期项须恰好等于 Mem（不变量2）
		for _, h := range hist {
			if wm < h.ts+10 {
				want[h.id] = h.ts
			}
		}
		if !reflect.DeepEqual(c.Mem(), want) {
			return fmt.Errorf("step %d: 记忆不精确", i+1)
		}
	}
	if err := selfCheckAtomic(); err != nil { // 不变量4
		return err
	}
	return dedup.CheckProbeBound() // 复杂度：探测条数有界，计数器数值不经公开接口外泄
}

// selfCheckAtomic 核验三类拒绝互异、整批回滚、回滚后仍可正常使用。
func selfCheckAtomic() error {
	if errors.Is(ErrEmptyID, ErrTooMany) || errors.Is(ErrEmptyID, ErrInvalidParam) ||
		errors.Is(ErrTooMany, ErrInvalidParam) {
		return errors.New("哨兵错误不互异")
	}
	if _, e := New(0, 2, 1); !errors.Is(e, ErrInvalidParam) {
		return errors.New("参数非法未拒绝")
	}
	t, _ := New(10, 2, 3)
	t.Feed([]Event{{ID: "x", TS: 1}, {ID: "y", TS: 1}})
	m0, d0 := t.Mem(), t.Dups()
	w0, ok0 := t.Watermark()
	if _, e := t.Feed([]Event{{ID: "z", TS: 100}, {ID: "", TS: 0}}); !errors.Is(e, ErrEmptyID) {
		return errors.New("空 ID 未拒绝")
	}
	w1, ok1 := t.Watermark() // z 先清掉 x/y 后失败，须全部还原
	if !reflect.DeepEqual(t.Mem(), m0) || t.Dups() != d0 || w1 != w0 || ok1 != ok0 {
		return errors.New("被拒批留下痕迹")
	}
	t2, _ := New(10, 2, 1)
	if _, e := t2.Feed([]Event{{ID: "x", TS: 1}, {ID: "y", TS: 2}}); !errors.Is(e, ErrTooMany) || len(t2.Mem()) != 0 {
		return errors.New("超限未整体回滚")
	}
	if _, e := t.Feed([]Event{{ID: "q", TS: 1}}); e != nil {
		return errors.New("拒绝后无法继续正常使用")
	}
	return nil
}

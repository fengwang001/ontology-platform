// Package wagg 按 Key 增量维护滑动窗口内的有序事件集合与 Val 之和。依赖 win。
package wagg

import (
	"cmp"
	"errors"
	"ontology/win"
	"slices"
	"sync"
)

// 三类可判定且互不相同的哨兵错误。
var (
	ErrBadSize     = errors.New("wagg: size 必须为正")
	ErrEmptyKey    = errors.New("wagg: 事件 Key 为空串")
	ErrTooManyOpen = errors.New("wagg: 窗口内事件数超出 maxOpen")
)

type Event struct {
	Key     string
	TS, Val int64
}

type keyWin struct {
	wm, sum int64
	evs     []Event
	dropped int64
}

func cmpTS(ev Event, ts int64) int { return cmp.Compare(ev.TS, ts) }

// ingest 就地处理一条事件；出错时状态可能已变，调用方（Apply）必须保证出错即整体丢弃。
// 返回 advanced 表示 wm 是否推进；probes 表示本次推进为定位过期事件检查过的事件个数。
func (k *keyWin) ingest(e Event, size int64, maxOpen int) (advanced bool, probes int, err error) {
	if e.TS > k.wm { // 水位线只进不退；窗口外事件也按同一 max 规则推进
		k.wm, advanced = e.TS, true
		n := 0
		for n < len(k.evs) && win.Expired(k.evs[n].TS, k.wm, size) {
			k.sum -= k.evs[n].Val // 过期一个精确减一个
			n++
		}
		probes = min(n+1, len(k.evs)) // 有序切片从头扫到首个未过期即停
		k.evs = k.evs[n:]
	}
	if !win.InWindow(e.TS, k.wm, size) {
		k.dropped++ // 窗口外到达：丢弃并计数
	} else if maxOpen > 0 && len(k.evs) >= maxOpen {
		err = ErrTooManyOpen
	} else {
		k.sum += e.Val
		i, _ := slices.BinarySearchFunc(k.evs, e.TS, cmpTS)
		k.evs = slices.Insert(k.evs, i, e) // 同 TS 事件各自保留，不去重
	}
	return advanced, probes, err
}

// Aggregator 维护所有 Key 的窗口。零值不可用，须用 New。
type Aggregator struct {
	mu        sync.RWMutex
	size      int64
	maxOpen   int
	keys      map[string]*keyWin
	lastProbe int // 非导出：最近一次推进 wm 时为定位过期事件检查过的事件个数
}

// New 创建聚合器。size 必须为正；maxOpen<=0 表示不限制窗口内事件数。
func New(size int64, maxOpen int) (*Aggregator, error) {
	if size <= 0 {
		return nil, ErrBadSize
	}
	return &Aggregator{size: size, maxOpen: maxOpen, keys: map[string]*keyWin{}}, nil
}

// Apply 原子地喂入一批事件：任一条被拒，整批不生效（wm/sum/丢弃数/成员全不变）。
func (a *Aggregator) Apply(evs []Event) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	work := map[string]*keyWin{} // 先在克隆状态上试跑
	probe := 0
	for _, e := range evs {
		if e.Key == "" {
			return ErrEmptyKey
		}
		k, ok := work[e.Key]
		if !ok { // 深拷贝：试跑失败时真实状态分毫未动
			k = &keyWin{}
			if r := a.keys[e.Key]; r != nil {
				*k = *r
				k.evs = slices.Clone(r.evs)
			}
			work[e.Key] = k
		}
		adv, p, err := k.ingest(e, a.size, a.maxOpen)
		if err != nil {
			return err
		}
		if adv {
			probe = p
		}
	}
	for key, k := range work {
		a.keys[key] = k
	}
	a.lastProbe = probe
	return nil
}

// Snapshot 返回每个 Key 当前窗口内的 Val 之和，以及所有 Key 累计的「窗口外到达」丢弃数。
func (a *Aggregator) Snapshot() (view map[string]int64, dropped int64) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	view = make(map[string]int64, len(a.keys))
	for k, w := range a.keys {
		view[k] = w.sum
		dropped += w.dropped
	}
	return view, dropped
}

// SelfCheck 核验成员守恒：每个 Key 的 sum 等于窗口内成员 Val 之和，且 wm 等于成员最大 TS。
func (a *Aggregator) SelfCheck() bool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	for _, w := range a.keys {
		var sum int64
		for _, e := range w.evs {
			sum += e.Val
		}
		if sum != w.sum || len(w.evs) > 0 && w.evs[len(w.evs)-1].TS != w.wm {
			return false
		}
	}
	return true
}

// ProbeSelfTest 在包内构造场景读取非导出计数器 lastProbe，验证：wm 小步前进且无过期时
// 检查个数是与 m 无关的小常数；wm 大步前进恰好过期 k 个时不超过 k+1。
func ProbeSelfTest() bool {
	const m, k2, base = 5000, 100, int64(1) << 40
	a, _ := New(1<<30, 0)
	bad := func(err error, want int) bool { return err != nil || a.lastProbe != want }
	evs := make([]Event, m)
	for i := range evs {
		evs[i] = Event{Key: "k", TS: base + int64(i), Val: 1}
	}
	b1 := bad(a.Apply(evs), 1)                                                       // 大窗口内无过期：常数
	b2 := bad(a.Apply([]Event{{Key: "k", TS: base + m, Val: 1}}), 1)                 // wm 只前进 1
	b3 := bad(a.Apply([]Event{{Key: "k", TS: base + 1<<30 + k2 - 1, Val: 1}}), k2+1) // 恰好过期 k2 个
	return !b1 && !b2 && !b3
}

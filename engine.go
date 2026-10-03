package ontology

import (
	"errors"
	"sort"
	"sync"
	"sync/atomic"

	"ontology/agg"
	"ontology/view"
)

type Engine struct {
	mu       sync.RWMutex
	dims     map[string]int
	roles    map[string]int
	kByLevel [3]int
	cMax     int
	eMax     int

	// events 始终按 Ts 升序；每次追加以“复制并归并”生成新切片，
	// 旧快照不可变，可在锁外安全使用。
	events []agg.Event

	// examined 统计 Range 实际考察过的事件数（二分后范围内切片长度）。
	examined atomic.Int64
}

type Event = agg.Event

func New(dims map[string]int, roles map[string]int, kByLevel []int, cMax, eMax int) *Engine {
	if len(dims) < 2 || len(dims) > 8 {
		panic(ErrInvalidArgument)
	}
	if len(kByLevel) != 3 {
		panic(ErrInvalidArgument)
	}
	for name, lvl := range dims {
		if name == "" || lvl < 1 || lvl > 3 {
			panic(ErrInvalidArgument)
		}
	}
	for _, lvl := range roles {
		if lvl < 1 || lvl > 3 {
			panic(ErrInvalidArgument)
		}
	}
	for i := 0; i < 3; i++ {
		if kByLevel[i] < 1 {
			panic(ErrInvalidArgument)
		}
		if i > 0 && kByLevel[i] < kByLevel[i-1] {
			panic(ErrInvalidArgument)
		}
	}
	if cMax < 1 || eMax < 1 {
		panic(ErrInvalidArgument)
	}

	dc := make(map[string]int, len(dims))
	for name, lvl := range dims {
		dc[name] = lvl
	}
	rc := make(map[string]int, len(roles))
	for name, lvl := range roles {
		rc[name] = lvl
	}
	return &Engine{
		dims:     dc,
		roles:    rc,
		kByLevel: [3]int{kByLevel[0], kByLevel[1], kByLevel[2]},
		cMax:     cMax,
		eMax:     eMax,
	}
}

func (e *Engine) Append(batch []Event) error {
	if len(batch) < 1 || len(batch) > 1000 {
		return ErrInvalidArgument
	}
	for _, ev := range batch {
		if ev.Ts < 0 || ev.Ts > 1e13 {
			return ErrInvalidArgument
		}
		if len(ev.Dims) != len(e.dims) {
			return ErrInvalidArgument
		}
		for name := range e.dims {
			v, ok := ev.Dims[name]
			if !ok || v == "" || len(v) > 64 {
				return ErrInvalidArgument
			}
		}
		for name := range ev.Dims {
			if _, ok := e.dims[name]; !ok {
				return ErrInvalidArgument
			}
		}
	}

	e.mu.Lock()
	defer e.mu.Unlock()
	if len(e.events)+len(batch) > e.eMax {
		return ErrCapacity
	}

	in := make([]agg.Event, len(batch))
	copy(in, batch)
	sort.SliceStable(in, func(i, j int) bool { return in[i].Ts < in[j].Ts })

	merged := make([]agg.Event, 0, len(e.events)+len(in))
	i, j := 0, 0
	for i < len(e.events) || j < len(in) {
		if j >= len(in) || (i < len(e.events) && e.events[i].Ts <= in[j].Ts) {
			merged = append(merged, e.events[i])
			i++
		} else {
			merged = append(merged, in[j])
			j++
		}
	}
	e.events = merged
	return nil
}

func (e *Engine) Tabulate(role, rowDim, colDim string, from, to int64) (view.Table, error) {
	if from >= to || from < 0 || to > 1e13 {
		return view.Table{}, ErrInvalidArgument
	}
	rl, okR := e.dims[rowDim]
	cl, okC := e.dims[colDim]
	if !okR || !okC || rowDim == colDim {
		return view.Table{}, ErrInvalidArgument
	}
	clearance, ok := e.roles[role]
	if !ok {
		return view.Table{}, ErrUnknownRole
	}
	if rl > clearance || cl > clearance {
		return view.Table{}, ErrForbidden
	}

	level := rl
	if cl > level {
		level = cl
	}
	k := e.kByLevel[level-1]

	events := e.rangeEvents(from, to)
	tab, err := view.Tabulate(events, rowDim, colDim, k, e.cMax)
	if errors.Is(err, view.ErrTooLarge) {
		return view.Table{}, ErrTooLarge
	}
	return tab, err
}

// rangeEvents 在有序事件上二分裁剪 [from,to)，只取范围内切片，绝不扫描范围外事件。
func (e *Engine) rangeEvents(from, to int64) []agg.Event {
	e.mu.RLock()
	defer e.mu.RUnlock()

	snap := e.events
	lo := sort.Search(len(snap), func(i int) bool { return snap[i].Ts >= from })
	hi := sort.Search(len(snap), func(i int) bool { return snap[i].Ts >= to })
	inRange := snap[lo:hi]

	// 复制出独立切片返回，快照底层数组在追加后会被替换，这里拷贝保证安全。
	out := make([]agg.Event, len(inRange))
	copy(out, inRange)
	e.examined.Add(int64(len(out)))
	return out
}

func (e *Engine) examinedCount() int64 {
	return e.examined.Load()
}

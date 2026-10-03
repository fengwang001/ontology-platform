// Package pane 实现滑动窗口窗格的输出时间戳合并与水位保持器。
//
// 元素按其时间戳被分配到所有覆盖它的滑动窗口（窗格以窗口起点 ws 标识），
// 每个窗格按输出时间戳策略维护一个保持值 hold，并钳制到输出水位 O。
// 输入水位 I 推进时，到期窗格按 ws 升序发出为 ON_TIME 窗格，
// 输出水位 O 只进不退：O = max(O, min(I, 剩余窗格 hold 的最小值))。
package pane

import (
	"container/heap"
	"errors"
	"sort"
	"sync"
)

// Policy 为输出时间戳策略。
type Policy int

const (
	// Earliest 以窗格内最小元素时间戳作为原始输出时间戳。
	Earliest Policy = iota
	// Latest 以窗格内最大元素时间戳作为原始输出时间戳。
	Latest
	// End 以窗口结束 end-1 作为原始输出时间戳。
	End
)

// 参数取值上界。
const (
	MaxTS  int64 = 1_000_000_000_000_000 // ts 与 I' 的上界 10^15
	MaxVal int64 = 1_000_000_000         // |val| 上界 10^9
	MaxS   int64 = 1_000_000_000         // 窗口宽度上界 10^9
	MaxAL  int64 = 1_000_000_000         // 允许迟到上界 10^9
	MaxCap int64 = 1_000_000             // 窗格表容量上界 10^6
)

// 可区分的拒绝原因。
var (
	// ErrInvalidParam 参数非法（构造参数、ts、val、I' 越界）。
	ErrInvalidParam = errors.New("pane: invalid parameter")
	// ErrCapacity 窗格表容量不足（仅 Add）。
	ErrCapacity = errors.New("pane: pane table capacity exceeded")
	// ErrRegression 输入水位回退（仅 Advance）。
	ErrRegression = errors.New("pane: input watermark regression")
)

// PaneOut 为发出的窗格（ON_TIME 或迟到窗格）。
type PaneOut struct {
	WS    int64 // 窗口起点
	Count int64 // 元素个数（迟到窗格恒为 1）
	Sum   int64 // 元素值之和（迟到窗格恒为单个 val）
	TS    int64 // 输出时间戳
}

// Pane 为窗格表中的一个窗格。
type Pane struct {
	WS    int64 // 窗口起点
	Count int64 // 缓冲元素个数
	Sum   int64 // 缓冲元素值之和
	MinTS int64 // 缓冲元素最小时间戳
	MaxTS int64 // 缓冲元素最大时间戳
	Hold  int64 // 保持值 hold = max(raw, O)
}

// holdItem 为最小堆中的保持值项，采用惰性删除：
// 窗格被更新或移除后，旧堆项成为失效项，在求最小 hold 时被丢弃。
type holdItem struct {
	hold int64
	ws   int64
}

type holdHeap []holdItem

func (h holdHeap) Len() int { return len(h) }

func (h holdHeap) Less(i, j int) bool {
	if h[i].hold != h[j].hold {
		return h[i].hold < h[j].hold
	}
	return h[i].ws < h[j].ws
}

func (h holdHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }

func (h *holdHeap) Push(x any) { *h = append(*h, x.(holdItem)) }

func (h *holdHeap) Pop() any {
	old := *h
	n := len(old)
	it := old[n-1]
	old[n-1] = holdItem{}
	*h = old[:n-1]
	return it
}

// Merger 为输出时间戳合并与水位保持器。所有方法可并发调用，
// 效果等价于某个串行顺序；Add 是原子步骤。
type Merger struct {
	mu     sync.Mutex
	s      int64 // 窗口宽度 S
	d      int64 // 滑动步长 D
	policy Policy
	al     int64 // 允许迟到 AL
	cap    int64 // 窗格表容量 Cap

	in      int64           // 输入水位 I，初值 -1
	out     int64           // 输出水位 O，初值 -1
	panes   map[int64]*Pane // 窗格表，键为 ws
	holds   holdHeap        // 保持值最小堆（惰性删除）
	dropped int64           // 丢弃计数

	holdProbes int64 // 求最小 hold 时查看的堆项数（含失效项）
}

// New 构造保持器。参数非法时整体拒绝并返回 ErrInvalidParam。
// 约束：1 <= D <= S <= 16*D，S <= 10^9，0 <= AL <= 10^9，1 <= Cap <= 10^6。
func New(s, d int64, policy Policy, allowedLateness, capacity int64) (*Merger, error) {
	if d < 1 || d > s || s > 16*d || s > MaxS {
		return nil, ErrInvalidParam
	}
	if policy != Earliest && policy != Latest && policy != End {
		return nil, ErrInvalidParam
	}
	if allowedLateness < 0 || allowedLateness > MaxAL {
		return nil, ErrInvalidParam
	}
	if capacity < 1 || capacity > MaxCap {
		return nil, ErrInvalidParam
	}
	return &Merger{
		s:      s,
		d:      d,
		policy: policy,
		al:     allowedLateness,
		cap:    capacity,
		in:     -1,
		out:    -1,
		panes:  make(map[int64]*Pane),
	}, nil
}

// floorDiv 按数学定义向下取整（对负数同样成立），b 必须为正。
func floorDiv(a, b int64) int64 {
	q := a / b
	if a%b != 0 && a < 0 {
		q--
	}
	return q
}

// ceilDiv 按数学定义向上取整（对负数同样成立），b 必须为正。
func ceilDiv(a, b int64) int64 {
	return -floorDiv(-a, b)
}

// 窗格分类。
const (
	kindBuffer = iota // I < end：进入缓冲
	kindLate          // end <= I < end+AL：立即发出迟到窗格
	kindDrop          // I >= end+AL：丢弃
)

// Add 把元素 (ts, val) 分配到其所属的全部窗口（ws 升序处理）。
// 返回按 ws 升序的迟到窗格清单。
// 拒绝顺序：参数非法（ErrInvalidParam）-> 窗格表容量不足（ErrCapacity）。
// 被拒绝的 Add 不生效：不发出迟到窗格、不计丢弃、不改变任何状态。
func (m *Merger) Add(ts, val int64) ([]PaneOut, error) {
	if ts < 0 || ts > MaxTS || val < -MaxVal || val > MaxVal {
		return nil, ErrInvalidParam
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	// 元素属于所有满足 ws <= ts < ws+S 的窗口，ws = k*D，
	// k 从 ceil((ts-S+1)/D) 到 floor(ts/D)，取整按数学定义。
	lo := ceilDiv(ts-m.s+1, m.d)
	hi := floorDiv(ts, m.d)

	kinds := make([]int, hi-lo+1)
	var newPanes int64
	for k := lo; k <= hi; k++ {
		end := k*m.d + m.s
		switch {
		case m.in >= end+m.al:
			kinds[k-lo] = kindDrop
		case m.in >= end:
			kinds[k-lo] = kindLate
		default:
			kinds[k-lo] = kindBuffer
			if _, ok := m.panes[k*m.d]; !ok {
				newPanes++
			}
		}
	}
	// 容量检查在任何副作用之前：迟到窗格与丢弃不受容量影响，
	// 但一旦容量不足，整个 Add 被拒绝，迟到窗格也不发出、丢弃也不计数。
	if int64(len(m.panes))+newPanes > m.cap {
		return nil, ErrCapacity
	}

	var late []PaneOut
	for k := lo; k <= hi; k++ {
		ws := k * m.d
		end := ws + m.s
		switch kinds[k-lo] {
		case kindDrop:
			m.dropped++
		case kindLate:
			f := ts
			if m.policy == End {
				f = end - 1
			}
			late = append(late, PaneOut{WS: ws, Count: 1, Sum: val, TS: max(f, m.out)})
		case kindBuffer:
			p, ok := m.panes[ws]
			if !ok {
				p = &Pane{WS: ws, MinTS: ts, MaxTS: ts}
				m.panes[ws] = p
			}
			p.Count++
			p.Sum += val
			if ts < p.MinTS {
				p.MinTS = ts
			}
			if ts > p.MaxTS {
				p.MaxTS = ts
			}
			var raw int64
			switch m.policy {
			case Earliest:
				raw = p.MinTS
			case Latest:
				raw = p.MaxTS
			case End:
				raw = end - 1
			}
			p.Hold = max(raw, m.out)
			heap.Push(&m.holds, holdItem{hold: p.Hold, ws: ws})
		}
	}
	return late, nil
}

// Advance 把输入水位推进到 I'（要求 I' >= I），发出所有 end <= I' 的
// 窗格（按 ws 升序，ON_TIME），随后令 O = max(O, min(I', 剩余窗格 hold
// 的最小值))（无剩余窗格则取 I'）。I' 等于 I 时同样执行上述步骤。
// 拒绝顺序：参数非法（ErrInvalidParam）-> 水位回退（ErrRegression）。
func (m *Merger) Advance(ip int64) ([]PaneOut, error) {
	if ip < 0 || ip > MaxTS {
		return nil, ErrInvalidParam
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if ip < m.in {
		return nil, ErrRegression
	}
	m.in = ip

	var due []int64
	for ws := range m.panes {
		if ws+m.s <= ip {
			due = append(due, ws)
		}
	}
	sort.Slice(due, func(i, j int) bool { return due[i] < due[j] })
	var out []PaneOut
	for _, ws := range due {
		p := m.panes[ws]
		out = append(out, PaneOut{WS: ws, Count: p.Count, Sum: p.Sum, TS: p.Hold})
		delete(m.panes, ws)
	}

	bound := ip
	if h, ok := m.minHold(); ok && h < bound {
		bound = h
	}
	if bound > m.out {
		m.out = bound
	}
	return out, nil
}

// minHold 返回剩余窗格 hold 的最小值。惰性丢弃失效堆项，
// 每查看一个堆项（含失效项）holdProbes 加一。
func (m *Merger) minHold() (int64, bool) {
	for len(m.holds) > 0 {
		top := m.holds[0]
		m.holdProbes++
		p, ok := m.panes[top.ws]
		if !ok || p.Hold != top.hold {
			heap.Pop(&m.holds)
			continue
		}
		return top.hold, true
	}
	return 0, false
}

// Output 返回当前输出水位 O。
func (m *Merger) Output() int64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.out
}

// Input 返回当前输入水位 I。
func (m *Merger) Input() int64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.in
}

// Dropped 返回丢弃计数。
func (m *Merger) Dropped() int64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.dropped
}

// Panes 按 ws 升序列出窗格表（含 hold）。
func (m *Merger) Panes() []Pane {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Pane, 0, len(m.panes))
	for _, p := range m.panes {
		out = append(out, *p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].WS < out[j].WS })
	return out
}

// HoldProbes 返回求最小 hold 时累计查看的堆项数（含失效项）。
func (m *Merger) HoldProbes() int64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.holdProbes
}

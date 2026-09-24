// Package snap 在 align 单通道状态之上实现跨通道检查点屏障对齐、快照与在途记录处理。
package snap

import (
	"errors"

	"ontology/align"
)

// 四类可判定、互不相同的哨兵错误。
var (
	ErrInvalidChannels    = errors.New("snap: channels must be positive")
	ErrChannelOutOfRange  = errors.New("snap: channel id out of range")
	ErrBarrierNonPositive = errors.New("snap: barrier id must be positive")
	ErrBarrierOutOfOrder  = errors.New("snap: barrier id must strictly increase per channel")
)

type Kind int

const (
	Rec Kind = iota // 增量记录，V 为有符号 delta
	Bar             // 屏障 id：同通道严格递增、每 id 每通道恰一条
) // Event 是流上的一个事件。
type Event struct {
	Kind  Kind
	Ch, V int
}

// Engine 是对齐快照算子的全部进程内状态。
type Engine struct {
	channels       []align.Channel
	snaps          map[int]int // snap[id] = 对齐完成瞬间的 sum
	emitted        []int       // 已向下游发出的屏障 id（按发出顺序）
	next           [][]Event   // 每通道越过本世代屏障后的延迟事件尾（通道内保序）；nil=未密封
	nextMax        []int       // 每通道延迟尾中出现过的最大屏障 id
	sum            int         // 累加器，只含已入状态的记录
	alignID        int         // 正在对齐的屏障 id；0 表示无对齐
	blocked        int         // 已阻塞通道数；O(1) 完成判定靠它与通道数比较
	lastCheckCount int         // 非导出：最近一次完成判定检查过的通道个数
}

// NewEngine 创建 c 条输入通道的算子，c 非正则拒绝。
func NewEngine(c int) (*Engine, error) {
	if c <= 0 {
		return nil, ErrInvalidChannels
	}
	return &Engine{channels: make([]align.Channel, c), snaps: map[int]int{},
		next: make([][]Event, c), nextMax: make([]int, c)}, nil
}

func (e *Engine) Sum() int { return e.sum }

// Snapshot 返回 id 的快照；尚未对齐完成时 ok 为 false。
func (e *Engine) Snapshot(id int) (int, bool) { v, ok := e.snaps[id]; return v, ok }

// LastCheckConstantBound 只以布尔形式暴露完成判定检查通道数是否为与 m 无关的常数。
func (e *Engine) LastCheckConstantBound() bool { return e.lastCheckCount <= 1 } // Feed 先整批校验（只写局部副本，不动任何状态）通过后再落状态；任一条非法整批不生效。
func (e *Engine) Feed(evs []Event) error {
	last := make([]int, len(e.channels))
	for i := range e.channels {
		last[i] = e.channels[i].LastBarrier()
		if e.nextMax[i] > last[i] { // 已排队未重放的延迟屏障也算「见过」
			last[i] = e.nextMax[i]
		}
	}
	for _, ev := range evs {
		if ev.Ch < 0 || ev.Ch >= len(e.channels) {
			return ErrChannelOutOfRange
		}
		if ev.Kind == Bar {
			if ev.V <= 0 {
				return ErrBarrierNonPositive
			}
			if ev.V <= last[ev.Ch] {
				return ErrBarrierOutOfOrder
			}
			last[ev.Ch] = ev.V
		} else if ev.Kind != Rec {
			return ErrChannelOutOfRange
		}
	}
	for _, ev := range evs {
		if ev.Kind == Rec {
			e.applyRec(ev.Ch, ev.V)
		} else {
			e.applyBar(ev.Ch, ev.V)
		}
	}
	return nil
}

// applyRec：屏障只栅自己通道。未阻塞立即入状态；阻塞且已密封则进延迟尾，不被本次排空。
func (e *Engine) applyRec(ch, delta int) {
	c := &e.channels[ch]
	switch {
	case !c.Blocked():
		e.sum += delta
	case e.next[ch] != nil:
		e.next[ch] = append(e.next[ch], Event{Rec, ch, delta})
	default:
		c.Buffer(delta)
	}
}

// applyBar：只检查刚到达的这一条通道；是否全对齐由 blocked 计数整数比较得出，恒为 1。
func (e *Engine) applyBar(ch, id int) {
	c := &e.channels[ch]
	if c.Blocked() { // 对齐未完成：下一世代及以后事件进延迟尾保序
		e.next[ch] = append(e.next[ch], Event{Bar, ch, id})
		if id > e.nextMax[ch] {
			e.nextMax[ch] = id
		}
		return
	}
	c.Block(id)
	e.blocked++
	if e.alignID == 0 {
		e.alignID = id
	}
	e.lastCheckCount = 1
	if e.blocked == len(e.channels) {
		e.complete()
	}
}

// complete：先在 sum 不含任何在途记录时快照，再按在途顺序排空、解阻塞、发屏障，重放延迟尾。
func (e *Engine) complete() {
	id := e.alignID
	e.snaps[id] = e.sum
	for i := range e.channels {
		for _, delta := range e.channels[i].Drain() {
			e.sum += delta
		}
		e.channels[i].Unblock()
	}
	e.emitted = append(e.emitted, id)
	e.alignID, e.blocked = 0, 0
	tails := e.next // 取出延迟尾后重置；重放中形成的新尾属于再下一世代
	e.next = make([][]Event, len(e.channels))
	e.nextMax = make([]int, len(e.channels))
	for ch, tail := range tails { // 加法可交换、截断逐通道判定，按通道序重放即可
		for _, ev := range tail {
			if ev.Kind == Rec {
				e.applyRec(ch, ev.V)
			} else {
				e.applyBar(ch, ev.V)
			}
		}
	}
}

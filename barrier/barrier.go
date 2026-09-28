// Package barrier 实现双输入算子的检查点屏障对齐组件。
//
// 两个输入通道（编号 0、1）各自按 1,2,3… 的顺序接收检查点屏障。
// 某个通道收到屏障后即进入阻塞，之后到达的事件进入该通道的有序缓冲；
// 另一个未阻塞通道的记录到达即处理。当两个通道都收到同一编号的屏障时
// 对齐发生：对当前累加状态拍逐键一致快照、向下游转发两个屏障，随后按
// 全局到达顺序重放两侧缓冲；重放中遇到下一号屏障则该通道重新阻塞。
package barrier

import (
	"fmt"
	"sort"
	"sync"
)

// Record 是通道中流动的普通数据记录，Key 不允许为空。
type Record struct {
	Key   string
	Value string
}

// Event 表示一次输入事件：要么是普通记录（BarrierNo == 0），
// 要么是检查点屏障（BarrierNo >= 1，Record 字段忽略）。
type Event struct {
	Channel   int // 通道号，合法值为 0、1
	Record    Record
	BarrierNo int // 0 表示普通记录，>=1 表示屏障编号
}

// Output 是对齐组件向下游发出的一个输出事件。
type Output struct {
	Kind      string // "record" 或 "barrier"
	Channel   int
	Record    Record
	BarrierNo int
}

// Snapshot 是一次屏障对齐时拍下的逐键一致快照。
type Snapshot struct {
	BarrierNo int
	Records   []Record // 按 Key 排序的深拷贝
}

// DefaultBufferLimit 是每个通道允许缓冲的普通记录数默认上限。
const DefaultBufferLimit = 1024

type bufferedEvent struct {
	seq     int64 // 全局到达序号，保证重放按到达顺序合并
	channel int
	record  Record
	barrier int // 0 表示普通记录
}

type channelState struct {
	nextBarrier int // 该通道下一个期望的屏障编号，从 1 开始
	blocked     bool
	queue       []bufferedEvent
	buffered    int // queue 中普通记录（不含屏障）的数量
}

type alignerState struct {
	channels [2]channelState
	// accum 以 Key 为序记录“已处理”的值序列；每个键的元素按处理顺序追加。
	accum map[string][]string
	// snapshots 按编号保存对齐快照；编号连续，故以切片存储。
	snapshots []*Snapshot
	seq       int64
}

// Aligner 是双输入屏障对齐器，可被并发读取。
type Aligner struct {
	mu          sync.RWMutex
	state       *alignerState
	outputs     []Output
	bufferLimit int
}

// New 创建对齐器；bufferLimit <= 0 时使用 DefaultBufferLimit。
// bufferLimit 为每个通道允许缓冲的普通记录数上限（屏障本身不计数）。
func New(bufferLimit int) *Aligner {
	if bufferLimit <= 0 {
		bufferLimit = DefaultBufferLimit
	}
	s := &alignerState{accum: map[string][]string{}}
	s.channels[0].nextBarrier = 1
	s.channels[1].nextBarrier = 1
	return &Aligner{state: s, bufferLimit: bufferLimit}
}

// Apply 原子地提交一个输入批：整批先校验，任一事件非法则整批拒绝，
// 累加状态、缓冲、快照与输出流均不发生任何变化。
func (a *Aligner) Apply(events []Event) ([]Output, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	if len(events) == 0 {
		return nil, nil
	}

	trial := a.state.clone()
	outputs := make([]Output, 0, len(events)*2)
	for _, ev := range events {
		out, err := trial.ingest(ev, a.bufferLimit)
		if err != nil {
			return nil, err
		}
		outputs = append(outputs, out...)
	}

	a.state = trial
	a.outputs = append(a.outputs, outputs...)
	return append([]Output(nil), outputs...), nil
}

// Snapshots 返回已完成对齐的全部快照编号（按编号升序）。
func (a *Aligner) Snapshots() []int {
	a.mu.RLock()
	defer a.mu.RUnlock()
	numbers := make([]int, len(a.state.snapshots))
	for i, snap := range a.state.snapshots {
		numbers[i] = snap.BarrierNo
	}
	return numbers
}

// SnapshotAt 读取指定编号快照的深拷贝，可并发调用；不存在时返回 false。
func (a *Aligner) SnapshotAt(barrierNo int) (*Snapshot, bool) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if barrierNo < 1 || barrierNo > len(a.state.snapshots) {
		return nil, false
	}
	return a.state.snapshots[barrierNo-1].deepCopy(), true
}

// TakeOutputs 取走并清空自上次调用以来累积的全部下游输出（线程安全）。
func (a *Aligner) TakeOutputs() []Output {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := a.outputs
	a.outputs = nil
	return out
}

func (s *alignerState) ingest(ev Event, bufferLimit int) ([]Output, error) {
	if ev.Channel != 0 && ev.Channel != 1 {
		return nil, fmt.Errorf("%w: got %d", ErrInvalidChannel, ev.Channel)
	}
	ch := &s.channels[ev.Channel]

	if ev.BarrierNo != 0 {
		if ev.BarrierNo != ch.nextBarrier {
			return nil, fmt.Errorf("%w: channel %d expects %d, got %d",
				ErrInvalidBarrierNo, ev.Channel, ch.nextBarrier, ev.BarrierNo)
		}
		ch.nextBarrier++
		s.seq++
		if ch.blocked {
			// 阻塞期间到达的屏障：加入缓冲，等待重放时触发再次阻塞。
			ch.queue = append(ch.queue, bufferedEvent{
				seq: s.seq, channel: ev.Channel, barrier: ev.BarrierNo})
			return nil, nil
		}
		return s.onBarrier(ev.Channel, ev.BarrierNo), nil
	}

	if ev.Record.Key == "" {
		return nil, fmt.Errorf("%w: channel %d", ErrEmptyKey, ev.Channel)
	}
	s.seq++
	item := bufferedEvent{seq: s.seq, channel: ev.Channel, record: ev.Record}
	if ch.blocked {
		if ch.buffered >= bufferLimit {
			return nil, fmt.Errorf("%w: channel %d limit %d",
				ErrBufferLimitExceeded, ev.Channel, bufferLimit)
		}
		ch.queue = append(ch.queue, item)
		ch.buffered++
		return nil, nil
	}

	// 未阻塞通道的记录到达即处理；处理后可能推进之前缓冲的重放。
	s.processRecord(item)
	outputs := []Output{{Kind: "record", Channel: ev.Channel, Record: ev.Record}}
	return append(outputs, s.drain()...), nil
}

// onBarrier 处理“立即生效”的屏障：本通道进入阻塞，
// 若另一通道也已阻塞在同一编号上则完成对齐。
func (s *alignerState) onBarrier(channel, no int) []Output {
	s.channels[channel].blocked = true
	other := s.channels[1-channel]
	if !other.blocked {
		return nil
	}

	// 两侧都停在同号屏障上：拍快照，再转发两侧屏障。
	snap := &Snapshot{BarrierNo: no, Records: s.sortedAccum()}
	s.snapshots = append(s.snapshots, snap)

	s.channels[0].blocked = false
	s.channels[1].blocked = false

	outputs := []Output{
		{Kind: "barrier", Channel: 0, BarrierNo: no},
		{Kind: "barrier", Channel: 1, BarrierNo: no},
	}
	outputs = append(outputs, s.drain()...)
	return outputs
}

// drain 按全局到达顺序重放两侧缓冲，直到两侧队首都是
// 阻塞通道的普通记录（无法继续推进）为止。
func (s *alignerState) drain() []Output {
	var outputs []Output
	for {
		idx := -1
		var best int64
		for c := 0; c < 2; c++ {
			ch := &s.channels[c]
			if len(ch.queue) == 0 {
				continue
			}
			head := ch.queue[0]
			// 阻塞通道只能放行其队首屏障（用于再次对齐）；
			// 普通记录必须继续缓冲。
			if ch.blocked && head.barrier == 0 {
				continue
			}
			if idx == -1 || head.seq < best {
				idx, best = c, head.seq
			}
		}
		if idx == -1 {
			return outputs
		}
		ch := &s.channels[idx]
		head := ch.queue[0]
		ch.queue = ch.queue[1:]
		if head.barrier != 0 {
			outputs = append(outputs, s.onBarrier(idx, head.barrier)...)
		} else {
			ch.buffered--
			s.processRecord(head)
			outputs = append(outputs, Output{
				Kind: "record", Channel: idx, Record: head.record})
		}
	}
}

func (s *alignerState) processRecord(ev bufferedEvent) {
	s.accum[ev.record.Key] = append(s.accum[ev.record.Key], ev.record.Value)
}

func (s *alignerState) sortedAccum() []Record {
	keys := make([]string, 0, len(s.accum))
	for key := range s.accum {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	total := 0
	for _, key := range keys {
		total += len(s.accum[key])
	}
	records := make([]Record, 0, total)
	for _, key := range keys {
		for _, value := range s.accum[key] {
			records = append(records, Record{Key: key, Value: value})
		}
	}
	return records
}

func (s *Snapshot) deepCopy() *Snapshot {
	cp := &Snapshot{BarrierNo: s.BarrierNo, Records: make([]Record, len(s.Records))}
	copy(cp.Records, s.Records)
	return cp
}

func (s *alignerState) clone() *alignerState {
	cp := &alignerState{
		accum:     make(map[string][]string, len(s.accum)),
		snapshots: make([]*Snapshot, len(s.snapshots)),
		seq:       s.seq,
	}
	for key, values := range s.accum {
		cp.accum[key] = append([]string(nil), values...)
	}
	for i, snap := range s.snapshots {
		cp.snapshots[i] = snap.deepCopy()
	}
	for c := 0; c < 2; c++ {
		cp.channels[c] = s.channels[c]
		if len(s.channels[c].queue) > 0 {
			cp.channels[c].queue = append([]bufferedEvent(nil), s.channels[c].queue...)
		}
	}
	return cp
}

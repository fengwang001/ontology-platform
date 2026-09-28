package causalbuf

import (
	"fmt"
	"sort"
	"sync"
)

// Logger 是组件接受的最小日志接口，标准库 log.Logger 天然满足。
type Logger interface {
	Printf(format string, args ...any)
}

// Option 配置 Buffer。
type Option func(*Buffer)

// WithLogger 注入日志器；设置后每次 Receive 都会打印输入、判定依据与交付结果。
func WithLogger(l Logger) Option {
	return func(b *Buffer) { b.logf = l.Printf }
}

// Buffer 是并发安全的因果交付缓冲。
//
// 本地向量时钟 vc[i] 表示发送方 i 已交付的最新消息序号；
// pending 暂存因果前驱尚未到齐的消息，以 (发送方, 序号) 为键去重。
type Buffer struct {
	mu        sync.Mutex
	peers     int
	capacity  int
	vc        []int
	pending   map[bufKey]Message
	delivered []Message
	dupCount  int64
	logf      func(format string, args ...any)
}

// bufKey 是消息在缓冲中的身份键。
type bufKey struct {
	sender int
	seq    int
}

// New 创建一个 peers 个发送方、缓冲容量为 capacity 的因果交付缓冲。
// peers 与 capacity 都必须为正数，否则返回 ErrInvalidArgument。
func New(peers, capacity int, opts ...Option) (*Buffer, error) {
	if peers <= 0 || capacity <= 0 {
		return nil, fmt.Errorf("%w: peers and capacity must be positive (got peers=%d capacity=%d)",
			ErrInvalidArgument, peers, capacity)
	}
	b := &Buffer{
		peers:    peers,
		capacity: capacity,
		vc:       make([]int, peers),
		pending:  make(map[bufKey]Message),
	}
	for _, opt := range opts {
		opt(b)
	}
	return b, nil
}

// Receive 接收一条消息。
//
// 判定顺序（每一步非法都直接拒绝，不改变任何状态）：
//  1. 参数与向量校验：发送方越界 ErrSenderOutOfRange，向量长度不符或含负值
//     （含发送方自身分量不为正）ErrInvalidVector；
//  2. 重复判定：(sender, seq) 已交付或已在缓冲中 → 丢弃并计数，OutcomeDuplicate；
//  3. 因果判定：seq 恰好接上本地向量且其余分量不超前 → 交付并级联；
//  4. 否则需要缓冲：缓冲已满 → ErrBufferFull，未满则入队等待。
func (b *Buffer) Receive(m Message) (Result, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	// 1. 校验。所有拒绝分支都在修改任何状态之前返回。
	if m.Sender < 0 || m.Sender >= b.peers {
		b.log("RECV reject sender=%d vector=%v: sender out of range [0,%d)",
			m.Sender, m.Vector, b.peers)
		return Result{}, fmt.Errorf("%w: sender=%d peers=%d",
			ErrSenderOutOfRange, m.Sender, b.peers)
	}
	if m.Vector == nil || len(m.Vector) != b.peers {
		b.log("RECV reject sender=%d vector=%v: vector length must equal peers=%d",
			m.Sender, m.Vector, b.peers)
		return Result{}, fmt.Errorf("%w: vector length=%v want=%d",
			ErrInvalidVector, len(m.Vector), b.peers)
	}
	for i, v := range m.Vector {
		if v < 0 {
			b.log("RECV reject sender=%d vector=%v: component [%d]=%d is negative",
				m.Sender, m.Vector, i, v)
			return Result{}, fmt.Errorf("%w: vector[%d]=%d is negative",
				ErrInvalidVector, i, v)
		}
	}
	if m.Vector[m.Sender] <= 0 {
		b.log("RECV reject sender=%d vector=%v: own component [%d] must be >= 1",
			m.Sender, m.Vector, m.Sender)
		return Result{}, fmt.Errorf("%w: vector[%d]=%d must be >= 1 for its own message",
			ErrInvalidVector, m.Sender, m.Vector[m.Sender])
	}

	seq := m.Vector[m.Sender]
	key := bufKey{m.Sender, seq}

	// 2. 重复判定：已交付（序号不超过本地向量）或已在缓冲中。
	if seq <= b.vc[m.Sender] {
		b.dupCount++
		b.log("RECV duplicate sender=%d seq=%d vector=%v (already delivered, local vc=%v); dupCount=%d",
			m.Sender, seq, m.Vector, b.vc, b.dupCount)
		return Result{Outcome: OutcomeDuplicate, DupCount: b.dupCount}, nil
	}
	if _, ok := b.pending[key]; ok {
		b.dupCount++
		b.log("RECV duplicate sender=%d seq=%d vector=%v (already buffered); dupCount=%d",
			m.Sender, seq, m.Vector, b.dupCount)
		return Result{Outcome: OutcomeDuplicate, DupCount: b.dupCount}, nil
	}

	// 3. 可交付则交付，并级联排空缓冲中新变可交付的消息。
	if b.deliverable(m) {
		out := b.deliverAndCascade(m)
		b.log("RECV delivered sender=%d seq=%d vector=%v; cascade delivered=%v; local vc=%v",
			m.Sender, seq, m.Vector, msgsBrief(out.Delivered), b.vc)
		return out, nil
	}

	// 4. 暂不能交付：缓冲满则拒绝（此时尚未写入任何状态）。
	if len(b.pending) >= b.capacity {
		b.log("RECV reject sender=%d seq=%d vector=%v: buffer full (%d/%d); local vc=%v",
			m.Sender, seq, m.Vector, len(b.pending), b.capacity, b.vc)
		return Result{}, fmt.Errorf("%w: buffer capacity=%d reached",
			ErrBufferFull, b.capacity)
	}
	b.pending[key] = m
	reason := b.blockReason(m)
	b.log("RECV buffered sender=%d seq=%d vector=%v (%s); buffered=%d/%d local vc=%v",
		m.Sender, seq, m.Vector, reason, len(b.pending), b.capacity, b.vc)
	return Result{Outcome: OutcomeBuffered, Delivered: nil, DupCount: b.dupCount}, nil
}

// deliverable 报告消息是否满足交付条件，调用时持锁。
//
// 条件：发送方自身序号恰好等于本地分量 + 1（不超前也不落后），
// 且其余每个分量都不超过本地向量（因果前驱均已交付）。
func (b *Buffer) deliverable(m Message) bool {
	s := m.Sender
	if m.Vector[s] != b.vc[s]+1 {
		return false
	}
	for j := 0; j < b.peers; j++ {
		if j != s && m.Vector[j] > b.vc[j] {
			return false
		}
	}
	return true
}

// blockReason 解释消息为什么不能立即交付，仅用于日志，调用时持锁。
func (b *Buffer) blockReason(m Message) string {
	s := m.Sender
	if gap := m.Vector[s] - b.vc[s] - 1; gap > 0 {
		return fmt.Sprintf("waiting own gap: sender=%d missing %d message(s) before seq=%d",
			s, gap, m.Vector[s])
	}
	for j := 0; j < b.peers; j++ {
		if j != s && m.Vector[j] > b.vc[j] {
			return fmt.Sprintf("waiting causal predecessor: needs vc[%d]>=%d local=%d",
				j, m.Vector[j], b.vc[j])
		}
	}
	return "unknown"
}

// deliverAndCascade 交付触发消息，然后反复在缓冲中找出全部可交付消息，
// 每轮按发送方编号最小者依次交付，直到没有消息可交付为止。调用时持锁。
func (b *Buffer) deliverAndCascade(first Message) Result {
	order := make([]Message, 0, 1)
	order = append(order, first)
	b.applyDeliver(first)

	for {
		ready := make([]Message, 0)
		for _, pm := range b.pending {
			if b.deliverable(pm) {
				ready = append(ready, pm)
			}
		}
		if len(ready) == 0 {
			break
		}
		// 确定性裁决：同一发送方至多一条可交付消息（必须是其下一序号），
		// 按发送方最小者排序即可保证输出唯一。
		sort.Slice(ready, func(i, j int) bool {
			if ready[i].Sender != ready[j].Sender {
				return ready[i].Sender < ready[j].Sender
			}
			return ready[i].Vector[ready[i].Sender] < ready[j].Vector[ready[j].Sender]
		})
		for _, pm := range ready {
			delete(b.pending, bufKey{pm.Sender, pm.Vector[pm.Sender]})
			order = append(order, pm)
			b.applyDeliver(pm)
		}
	}
	return Result{Outcome: OutcomeDelivered, Delivered: order, DupCount: b.dupCount}
}

// applyDeliver 推进本地向量并追加交付序列，调用时持锁。
func (b *Buffer) applyDeliver(m Message) {
	s := m.Sender
	b.vc[s] = m.Vector[s]
	b.delivered = append(b.delivered, m)
}

// Vector 返回本地向量时钟的快照副本。
func (b *Buffer) Vector() []int {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]int, len(b.vc))
	copy(out, b.vc)
	return out
}

// Delivered 返回截至当前的完整交付序列快照副本。
func (b *Buffer) Delivered() []Message {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]Message, len(b.delivered))
	copy(out, b.delivered)
	return out
}

// BufferedCount 返回当前缓冲中的消息数。
func (b *Buffer) BufferedCount() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.pending)
}

// DupCount 返回累计重复消息计数。
func (b *Buffer) DupCount() int64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.dupCount
}

// log 输出一条日志（若配置了日志器）。
func (b *Buffer) log(format string, args ...any) {
	if b.logf != nil {
		b.logf(format, args...)
	}
}

// msgsBrief 把消息列表压缩成 (sender:seq) 形式用于日志。
func msgsBrief(ms []Message) string {
	out := make([][2]int, 0, len(ms))
	for _, m := range ms {
		out = append(out, [2]int{m.Sender, m.Vector[m.Sender]})
	}
	return fmt.Sprint(out)
}

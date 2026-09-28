// Package causal 实现基于向量时钟（vector clock）的因果交付缓冲。
//
// 乱序甚至重复到达的广播消息在此按因果顺序交付给上层：暂时不满足交付
// 条件的消息进入缓冲，待其因果前驱全部交付后再级联交付；重复消息被
// 丢弃并计数，不作为错误。
package causal

import (
	"fmt"
	"io"
	"log"
	"os"
	"strings"
	"sync"
)

// Message 是一条带发送方编号与向量时钟的广播消息。
type Message struct {
	// Sender 为发送方编号，取值范围 [0, N)。
	Sender int
	// Vector 为发送该消息时的向量时钟，长度必须等于进程数 N，
	// 各分量非负，且 Sender 对应分量（本消息的发送方序号）必须 >= 1。
	Vector []int
	// Payload 为上层负载，本组件不解释其内容，仅随交付原样返回。
	Payload any
}

// seq 返回消息在其发送方流中的序号（向量中 Sender 对应分量）。
func (m *Message) seq() int { return m.Vector[m.Sender] }

// Reason 标识一次接收被拒绝的可区分原因。
type Reason string

const (
	// ReasonInvalidArgument 参数非法（如消息为 nil）。
	ReasonInvalidArgument Reason = "invalid_argument"
	// ReasonSenderOutOfRange 发送方编号越界。
	ReasonSenderOutOfRange Reason = "sender_out_of_range"
	// ReasonInvalidVector 向量非法（长度不符、存在负分量或发送方分量 < 1）。
	ReasonInvalidVector Reason = "invalid_vector"
	// ReasonBufferFull 缓冲已满且消息当前无法立即交付。
	ReasonBufferFull Reason = "buffer_full"
)

// RejectError 表示 Receive 因明确原因拒绝了一条消息。
// 被拒绝的操作不会改变本地向量、缓冲、交付序列或重复计数。
type RejectError struct {
	Reason Reason
	detail string
}

func (e *RejectError) Error() string { return string(e.Reason) + ": " + e.detail }

// ReceiveResult 是一次 Receive 的判定结果。
type ReceiveResult struct {
	// Delivered 为本次调用直接交付或由其级联交付的消息，严格按因果序
	// 排列；消息被缓冲、是重复消息或被拒绝时为空。
	Delivered []Message
	// Duplicate 为 true 表示该消息是重复消息，已被丢弃并计入重复计数。
	// 重复不是错误，Err 必为 nil。
	Duplicate bool
	// Buffered 为 true 表示该消息因果前驱未齐，已进入缓冲等待。
	Buffered bool
}

// Buffer 是并发安全的因果交付缓冲。多个 goroutine 可并发调用 Receive；
// 每次调用在互斥区内完成“判定 + 状态变更”，因此状态机是接收序列的
// 确定性函数：同一输入序列反复计算得到完全相同的输出。
type Buffer struct {
	mu sync.Mutex

	n        int
	capacity int
	// clock 为本地向量时钟，clock[i] 表示已交付的进程 i 的最大序号。
	clock []int
	// pending 为因因果前驱未齐而等待交付的消息。
	pending []Message
	// delivered 为截至目前的交付序列（全局因果序）。
	delivered []Message
	dupCount  int

	logger *log.Logger
}

// New 创建一个进程数为 n、缓冲容量为 capacity 的因果交付缓冲，
// 判定过程日志写入 stderr。n 必须 >= 1，capacity 必须 >= 0。
func New(n, capacity int) (*Buffer, error) {
	return NewWithLogger(n, capacity, os.Stderr)
}

// NewWithLogger 与 New 相同，但允许指定日志输出；w 为 nil 时丢弃日志。
func NewWithLogger(n, capacity int, w io.Writer) (*Buffer, error) {
	if n < 1 {
		return nil, fmt.Errorf("n must be >= 1, got %d", n)
	}
	if capacity < 0 {
		return nil, fmt.Errorf("capacity must be >= 0, got %d", capacity)
	}
	var logger *log.Logger
	if w != nil {
		logger = log.New(w, "causal: ", log.LstdFlags|log.Lmicroseconds)
	} else {
		logger = log.New(io.Discard, "", 0)
	}
	return &Buffer{
		n:        n,
		capacity: capacity,
		clock:    make([]int, n),
		logger:   logger,
	}, nil
}

// Receive 接收一条消息并返回判定结果：
//
//   - 合法且交付条件满足：立即交付，并级联交付所有因此变得可交付的缓冲
//     消息，级联时每轮取可交付消息中发送方编号最小者，保证因果序唯一；
//   - 因果前驱未齐：进入缓冲（缓冲已满则以 ReasonBufferFull 拒绝）；
//   - 重复消息（该发送方序号已交付，或已在缓冲中）：丢弃并计数，返回
//     Duplicate=true 且 error 为 nil；
//   - 非法输入：返回 *RejectError，且不产生任何副作用。
func (b *Buffer) Receive(msg *Message) (ReceiveResult, error) {
	if msg == nil {
		return ReceiveResult{}, b.reject(nil, ReasonInvalidArgument, "message is nil")
	}
	if msg.Sender < 0 || msg.Sender >= b.n {
		return ReceiveResult{}, b.reject(msg, ReasonSenderOutOfRange,
			fmt.Sprintf("sender %d out of range [0,%d)", msg.Sender, b.n))
	}
	if len(msg.Vector) != b.n {
		return ReceiveResult{}, b.reject(msg, ReasonInvalidVector,
			fmt.Sprintf("vector length %d != process count %d", len(msg.Vector), b.n))
	}
	for i, c := range msg.Vector {
		if c < 0 {
			return ReceiveResult{}, b.reject(msg, ReasonInvalidVector,
				fmt.Sprintf("vector component [%d]=%d is negative", i, c))
		}
	}
	if msg.seq() < 1 {
		return ReceiveResult{}, b.reject(msg, ReasonInvalidVector,
			fmt.Sprintf("sender component vector[%d]=%d must be >= 1", msg.Sender, msg.seq()))
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	b.logger.Printf("receive  input  sender=%d seq=%d vector=%v payload=%v clock=%v",
		msg.Sender, msg.seq(), msg.Vector, msg.Payload, b.clock)

	// 已交付的重复：发送方序号不超过本地分量。
	if msg.seq() <= b.clock[msg.Sender] {
		b.dupCount++
		b.logger.Printf("discard  duplicate sender=%d seq=%d (already delivered, clock[%d]=%d) dupCount=%d",
			msg.Sender, msg.seq(), msg.Sender, b.clock[msg.Sender], b.dupCount)
		return ReceiveResult{Duplicate: true}, nil
	}

	// 恰好接上且其余分量不超前：可立即交付。
	if reason, ok := b.deliverable(msg); ok {
		delivered := b.deliverAndCascade(*msg)
		b.logger.Printf("accept   immediate + cascade delivered=%d %s clock=%v",
			len(delivered), summarize(delivered), b.clock)
		return ReceiveResult{Delivered: delivered}, nil
	} else {
		b.logger.Printf("hold     basis=%s", reason)
	}

	// 尚未交付过，但同一（发送方,序号）消息已在缓冲中：重复，丢弃计数。
	for i := range b.pending {
		if p := &b.pending[i]; p.Sender == msg.Sender && p.seq() == msg.seq() {
			b.dupCount++
			b.logger.Printf("discard  duplicate sender=%d seq=%d (already buffered) dupCount=%d",
				msg.Sender, msg.seq(), b.dupCount)
			return ReceiveResult{Duplicate: true}, nil
		}
	}

	if len(b.pending) >= b.capacity {
		b.logger.Printf("reject   buffer_full pending=%d capacity=%d (state unchanged)",
			len(b.pending), b.capacity)
		return ReceiveResult{}, &RejectError{
			Reason: ReasonBufferFull,
			detail: fmt.Sprintf("buffer full: %d pending, capacity %d", len(b.pending), b.capacity),
		}
	}

	b.pending = append(b.pending, *msg)
	b.logger.Printf("buffer   sender=%d seq=%d pending=%d/%d",
		msg.Sender, msg.seq(), len(b.pending), b.capacity)
	return ReceiveResult{Buffered: true}, nil
}

// deliverable 判定消息在当前本地向量下是否满足交付条件：
// 其发送方序号恰好接上（clock[s]+1 == v[s]），且其余分量均不超过本地
// 向量（v[i] <= clock[i], i != s）。调用方须持有 b.mu。
func (b *Buffer) deliverable(m *Message) (string, bool) {
	s := m.Sender
	if m.seq() != b.clock[s]+1 {
		return fmt.Sprintf("gap: vector[%d]=%d, expected %d", s, m.seq(), b.clock[s]+1), false
	}
	for i := 0; i < b.n; i++ {
		if i == s {
			continue
		}
		if m.Vector[i] > b.clock[i] {
			return fmt.Sprintf("ahead: vector[%d]=%d > clock[%d]=%d", i, m.Vector[i], i, b.clock[i]), false
		}
	}
	return "seq contiguous and all other components <= local clock", true
}

// deliverAndCascade 交付首条消息，随后反复扫描缓冲：每轮在所有可交付
// 消息中取发送方编号最小者交付（同一发送方至多一条可交付，故顺序唯一），
// 直到没有可交付消息为止。调用方须持有 b.mu。
func (b *Buffer) deliverAndCascade(first Message) []Message {
	out := make([]Message, 0, 1+len(b.pending))
	b.applyDelivery(first)
	out = append(out, first)

	for {
		idx := -1
		for i := range b.pending {
			if _, ok := b.deliverable(&b.pending[i]); !ok {
				continue
			}
			if idx == -1 || b.pending[i].Sender < b.pending[idx].Sender {
				idx = i
			}
		}
		if idx == -1 {
			break
		}
		m := b.pending[idx]
		b.pending = append(b.pending[:idx], b.pending[idx+1:]...)
		b.applyDelivery(m)
		out = append(out, m)
		b.logger.Printf("cascade  deliver sender=%d seq=%d vector=%v clock=%v",
			m.Sender, m.seq(), m.Vector, b.clock)
	}
	return out
}

// applyDelivery 交付一条消息：合并向量并追加到交付序列。调用方须持有 b.mu。
func (b *Buffer) applyDelivery(m Message) {
	for i := 0; i < b.n; i++ {
		if m.Vector[i] > b.clock[i] {
			b.clock[i] = m.Vector[i]
		}
	}
	b.delivered = append(b.delivered, m)
	b.logger.Printf("deliver  sender=%d seq=%d vector=%v clock=%v deliveredTotal=%d",
		m.Sender, m.seq(), m.Vector, b.clock, len(b.delivered))
}

// reject 记录拒绝日志并返回 *RejectError；调用发生在校验阶段、任何状态
// 变更之前，故拒绝不改变本地向量、缓冲、交付序列或重复计数。
func (b *Buffer) reject(msg *Message, reason Reason, detail string) error {
	sender, vector := -1, []int(nil)
	if msg != nil {
		sender = msg.Sender
		vector = msg.Vector
	}
	b.logger.Printf("reject   reason=%s sender=%d vector=%v detail=%s (state unchanged)",
		reason, sender, vector, detail)
	return &RejectError{Reason: reason, detail: detail}
}

// Vector 返回当前本地向量时钟的副本。
func (b *Buffer) Vector() []int {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]int, len(b.clock))
	copy(out, b.clock)
	return out
}

// Delivered 返回截至目前已交付消息序列的副本（全局因果序）。
func (b *Buffer) Delivered() []Message {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]Message(nil), b.delivered...)
}

// DuplicateCount 返回被丢弃的重复消息条数（含已交付重复与缓冲中重复）。
func (b *Buffer) DuplicateCount() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.dupCount
}

// PendingCount 返回当前缓冲中等待交付的消息条数。
func (b *Buffer) PendingCount() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.pending)
}

// summarize 生成已交付消息的简短摘要，如 "[0#1 1#1]"。
func summarize(ms []Message) string {
	ids := make([]string, len(ms))
	for i, m := range ms {
		ids[i] = fmt.Sprintf("%d#%d", m.Sender, m.seq())
	}
	return "[" + strings.Join(ids, " ") + "]"
}

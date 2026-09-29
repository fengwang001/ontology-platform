package termination

import "sync"

// Message 是网络中在途的基本消息。
type Message struct {
	ID   uint64 // 全局唯一、单调递增
	From int
	To   int
}

// Network 是可注入的异步网络：发送即注入，可任意延迟后再投递。
// 实现必须支持多 goroutine 并发调用。
type Network interface {
	// Inject 把消息放入网络，返回其在网络视角下的投递标识。
	Inject(msg Message)
	// Pending 返回当前在途消息的副本（顺序由实现决定）。
	Pending() []Message
	// Remove 取出并移除指定标识的消息；不存在时返回 ErrMessageNotFound。
	Remove(id uint64) (Message, error)
}

// MemoryNetwork 是 Network 的默认内存实现：按 FIFO 保存未投递消息，
// 所有方法均为并发安全。
type MemoryNetwork struct {
	mu   sync.Mutex
	msgs []Message
}

// NewMemoryNetwork 创建空的内存网络。
func NewMemoryNetwork() *MemoryNetwork {
	return &MemoryNetwork{}
}

func (n *MemoryNetwork) Inject(msg Message) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.msgs = append(n.msgs, msg)
}

func (n *MemoryNetwork) Pending() []Message {
	n.mu.Lock()
	defer n.mu.Unlock()
	out := make([]Message, len(n.msgs))
	copy(out, n.msgs)
	return out
}

func (n *MemoryNetwork) Remove(id uint64) (Message, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	for i, msg := range n.msgs {
		if msg.ID == id {
			n.msgs = append(n.msgs[:i], n.msgs[i+1:]...)
			return msg, nil
		}
	}
	return Message{}, ErrMessageNotFound
}

package deadlock

import (
	"sync"
	"time"
)

// Message 是沿一条等待边 from->to 投递的探测消息。
type Message struct {
	Probe Probe
	From  string
	To    string
}

// Probe 携带发起者与途经事务序列（Path[0] 即发起者，末元素为当前所在事务）。
type Probe struct {
	Initiator string
	Path      []string
}

// Network 是可注入的消息网络实现：测试可注入延迟、乱序或手动投递。
// Send 不得在调用方持锁期间同步回调 Manager.Deliver。
type Network interface {
	Send(msg Message)
}

// ManualNetwork 将消息排队，由调用方显式选择投递顺序，用于确定性重放。
type ManualNetwork struct {
	mu      sync.Mutex
	deliver func(Message)
	pending []Message
}

func NewManualNetwork(deliver func(Message)) *ManualNetwork {
	return &ManualNetwork{deliver: deliver}
}

func (n *ManualNetwork) Send(msg Message) {
	n.mu.Lock()
	n.pending = append(n.pending, msg)
	n.mu.Unlock()
}

// Pending 返回当前在途消息数。
func (n *ManualNetwork) Pending() int {
	n.mu.Lock()
	defer n.mu.Unlock()
	return len(n.pending)
}

// PendingMessages 返回在途消息快照（下标即 Deliver 的参数）。
func (n *ManualNetwork) PendingMessages() []Message {
	n.mu.Lock()
	defer n.mu.Unlock()
	out := make([]Message, len(n.pending))
	copy(out, n.pending)
	return out
}

// Deliver 投递第 i 条在途消息并将其移出队列，实现乱序投递。
func (n *ManualNetwork) Deliver(i int) bool {
	n.mu.Lock()
	if i < 0 || i >= len(n.pending) {
		n.mu.Unlock()
		return false
	}
	msg := n.pending[i]
	n.pending = append(n.pending[:i], n.pending[i+1:]...)
	n.mu.Unlock()
	n.deliver(msg)
	return true
}

// DeliverAll 按 FIFO 顺序投递直至无在途消息。
func (n *ManualNetwork) DeliverAll() {
	for n.Deliver(0) {
	}
}

// AsyncNetwork 为每条消息启动 goroutine，注入随机延迟后投递，用于并发压测。
type AsyncNetwork struct {
	deliver func(Message)
	delay   func() time.Duration
	wg      sync.WaitGroup
}

func NewAsyncNetwork(deliver func(Message), delay func() time.Duration) *AsyncNetwork {
	return &AsyncNetwork{deliver: deliver, delay: delay}
}

func (n *AsyncNetwork) Send(msg Message) {
	n.wg.Add(1)
	go func() {
		defer n.wg.Done()
		if n.delay != nil {
			time.Sleep(n.delay())
		}
		n.deliver(msg)
	}()
}

// Wait 等待全部已发送消息投递完成。
func (n *AsyncNetwork) Wait() { n.wg.Wait() }

package dining

import "sync"

// Network 是消息注入网络抽象。
// 同一有向信道先进先出，不同信道之间的消息可任意交错。
type Network interface {
	// Send 把消息追加到 from->to 有向信道队尾。
	Send(from, to int, msg Message)
	// Pop 取走 from->to 信道的队首消息；不存在时返回 false。
	Pop(from, to int) (Message, bool)
	// Peek 查看队首但不出队；不存在时返回 false。
	Peek(from, to int) (Message, bool)
	// Pending 返回全部待投递消息的快照（同一信道内保持 FIFO 顺序）。
	Pending() []Delivery
}

// QueueNetwork 是按有向信道维护 FIFO 队列的内存网络。
//
// 锁序约定：Coordinator 持自身互斥锁后再调用本网络的方法（锁序
// Coordinator.mu -> QueueNetwork.mu），网络从不回调协调器，故无死锁；
// 网络也可在协调器之外被独立并发使用。
type QueueNetwork struct {
	mu sync.Mutex
	q  map[[2]int][]Message
}

// NewQueueNetwork 创建空网络。
func NewQueueNetwork() *QueueNetwork {
	return &QueueNetwork{q: map[[2]int][]Message{}}
}

func (n *QueueNetwork) Send(from, to int, msg Message) {
	n.mu.Lock()
	defer n.mu.Unlock()
	k := [2]int{from, to}
	n.q[k] = append(n.q[k], msg)
}

func (n *QueueNetwork) Pop(from, to int) (Message, bool) {
	n.mu.Lock()
	defer n.mu.Unlock()
	k := [2]int{from, to}
	q := n.q[k]
	if len(q) == 0 {
		return Message{}, false
	}
	msg := q[0]
	if len(q) == 1 {
		delete(n.q, k)
	} else {
		n.q[k] = q[1:]
	}
	return msg, true
}

func (n *QueueNetwork) Peek(from, to int) (Message, bool) {
	n.mu.Lock()
	defer n.mu.Unlock()
	q := n.q[[2]int{from, to}]
	if len(q) == 0 {
		return Message{}, false
	}
	return q[0], true
}

func (n *QueueNetwork) Pending() []Delivery {
	n.mu.Lock()
	defer n.mu.Unlock()
	keys := make([][2]int, 0, len(n.q))
	for k := range n.q {
		keys = append(keys, k)
	}
	for i := 0; i < len(keys); i++ {
		for j := i + 1; j < len(keys); j++ {
			if keys[j][0] < keys[i][0] ||
				(keys[j][0] == keys[i][0] && keys[j][1] < keys[i][1]) {
				keys[i], keys[j] = keys[j], keys[i]
			}
		}
	}
	var out []Delivery
	for _, k := range keys {
		for _, msg := range n.q[k] {
			out = append(out, Delivery{From: k[0], To: k[1], Msg: msg})
		}
	}
	return out
}

// SliceLogger 把判定事件收集到内存切片，供测试打印与重放比对。
type SliceLogger struct {
	mu     sync.Mutex
	Events []Event
}

func (l *SliceLogger) Log(e Event) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.Events = append(l.Events, e)
}

// Snapshot 返回已记录事件的拷贝。
func (l *SliceLogger) Snapshot() []Event {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]Event, len(l.Events))
	copy(out, l.Events)
	return out
}

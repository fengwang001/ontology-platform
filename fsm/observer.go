package fsm

// observerBuffer 是每个观察者通道的缓冲大小。
const observerBuffer = 64

// addObserver 注册一个新的观察者并返回其状态变更通道。
//
// 通道只投递“成功转移后”的状态（含自转移、含进入终态）；
// 初始状态不投递，非法事件与 entry 失败也不投递。
func (m *Machine) addObserver() <-chan State {
	ch := make(chan State, observerBuffer)
	m.observers[ch] = struct{}{}
	return ch
}

// notifyObservers 向所有观察者非阻塞地投递一次状态变更。
//
// 丢弃规则（慢消费者不阻塞 Fire）：每个观察者拥有独立的带缓冲通道，
// 当某观察者的通道已满时，本次（以及后续溢出的）状态被丢弃，发送方
// 绝不阻塞。只有在“成功转移”时才按全局顺序尝试投递，因此观察者收
// 到的状态必然保持原顺序、无重复，构成完整状态序列的一个子序列：
// 通道尚未满时缓冲中保留的是完整序列的严格前缀；满后丢弃新状态，
// 消费者读腾出空位后又能接到后续状态。任何已交付状态与 Log() 中同
// 序转移的 To 一一对应，丢弃只会让观察者“少收到”，不会错位。
func (m *Machine) notifyObservers(s State) {
	for ch := range m.observers {
		select {
		case ch <- s:
		default:
			// 通道已满：丢弃本次状态，避免慢观察者阻塞 Fire。
		}
	}
}

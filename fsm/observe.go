package fsm

// Observe 订阅状态变更，可多次调用，每次返回一个独立通道。
//
// 通道带 observerBuf 大小的缓冲，只接收“订阅之后”成功迁移到
// 的新状态（含自转移），初始状态不推送。Fire 使用非阻塞发送：
// 某个观察者缓冲满时，这一次状态对该观察者直接丢弃，不影响
// 其他观察者，也不让 Fire 阻塞。丢弃整条状态而不重排，因此
// 每个观察者收到的序列始终是完整序列的一个有序子序列，序号
// 与 Log 中转移一一对应、绝不串位。机器生命周期内不关闭通道。
func (m *Machine) Observe() <-chan State {
	ch := make(chan State, observerBuf)
	m.mu.Lock()
	m.subs = append(m.subs, ch)
	m.mu.Unlock()
	return ch
}

// broadcast 以非阻塞方式把新状态发给所有观察者。
// 必须在持有 m.mu 时调用。
func (m *Machine) broadcast(s State) {
	for _, ch := range m.subs {
		select {
		case ch <- s:
		default:
			// 慢观察者：丢弃本次状态，保持 Fire 不阻塞、序列不错位。
		}
	}
}

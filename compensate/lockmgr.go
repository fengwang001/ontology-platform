package compensate

import "sync"

// 全部所有权/排队/持有的簿记都在 m.mu 一把锁下原子完成。goroutine 的阻塞等待发生在
// 临界区之外；唤醒信号在释放者完成“设定新 owner + 登记其 held 集合”之后才发出，
// 因此被唤醒者一旦从 Lock 返回，该槽位必然已计入其动作的 held 集合，
// UnlockAll 不可能漏放或提前释放任何槽位。

type slotWaiter struct {
	actionID string
	ch       chan struct{}
}

type slotState struct {
	owner  string                   // 空表示空闲
	queue  []slotWaiter             // FIFO 等待队列
	joined map[string]chan struct{} // 已排队动作 -> 共享等待事件（同动作去重）
}

// LockManager 提供按槽位的严格 2PL 锁：同一槽位跨动作互斥，锁由动作持有到全部补偿
// 结束（生长阶段一次性获取、补偿结束后统一释放）；重叠槽位把不同动作串行化，不相交
// 槽位允许完全并行，从而保证跨动作可串行化。同一动作对同一槽位去重，只占有一次。
type LockManager struct {
	mu    sync.Mutex
	slots map[string]*slotState
	held  map[string]map[string]bool // action -> 已占有的槽位集合
}

func newLockManager() *LockManager {
	return &LockManager{
		slots: map[string]*slotState{},
		held:  map[string]map[string]bool{},
	}
}

// Lock 为 actionID 占有槽位 key；被其它动作持有时按 FIFO 阻塞。
func (m *LockManager) Lock(actionID, key string) {
	m.mu.Lock()
	if set := m.held[actionID]; set != nil && set[key] {
		m.mu.Unlock()
		return // 同动作已占有：重入直接复用
	}
	st := m.slots[key]
	if st == nil {
		st = &slotState{joined: map[string]chan struct{}{}}
		m.slots[key] = st
	}
	if st.owner == "" {
		// 空闲：owner 与 held 在同一临界区原子登记。
		st.owner = actionID
		m.addHeld(actionID, key)
		m.mu.Unlock()
		return
	}
	// 同动作已有请求在排队：共享事件，队列中该动作保持唯一条目。
	if ch, ok := st.joined[actionID]; ok {
		m.mu.Unlock()
		<-ch
		return
	}
	ch := make(chan struct{}, 1)
	st.joined[actionID] = ch
	st.queue = append(st.queue, slotWaiter{actionID: actionID, ch: ch})
	m.mu.Unlock()
	<-ch
}

// UnlockAll 释放某动作占有的全部槽位；唤醒信号在退出临界区后发送。
func (m *LockManager) UnlockAll(actionID string) {
	m.mu.Lock()
	owned := m.held[actionID]
	delete(m.held, actionID)
	wake := []chan struct{}{}
	for key := range owned {
		st := m.slots[key]
		if len(st.queue) == 0 {
			st.owner = ""
			continue
		}
		// 严格 FIFO：只交接给队首动作（队列中每个动作唯一），持有集合登记一次。
		head := st.queue[0]
		st.queue = st.queue[1:]
		delete(st.joined, head.actionID)
		st.owner = head.actionID
		m.addHeld(head.actionID, key)
		wake = append(wake, head.ch)
	}
	m.mu.Unlock()
	for _, ch := range wake {
		ch <- struct{}{}
	}
}

// holds 报告某动作当前是否占有某槽位（供对象图层做不变量断言）。
func (m *LockManager) holds(actionID, key string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	set := m.held[actionID]
	return set != nil && set[key]
}

// addHeld 调用方持有 m.mu。
func (m *LockManager) addHeld(actionID, key string) {
	set := m.held[actionID]
	if set == nil {
		set = map[string]bool{}
		m.held[actionID] = set
	}
	set[key] = true
}

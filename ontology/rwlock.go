package ontology

import "sync"

// writerPriorityRWMutex 是写优先读写锁：
// 一旦有写者等待，新到的读者必须排队等待，避免设值+重算这类长写操作
// 被持续涌入的读者饿死。读者之间共享，同一时刻至多一个写者。
type writerPriorityRWMutex struct {
	mu          sync.Mutex
	readers     *sync.Cond
	writers     *sync.Cond
	activeRead  int
	activeWrite bool
	waitingW    int
}

func newWriterPriorityRWMutex() *writerPriorityRWMutex {
	m := &writerPriorityRWMutex{}
	m.readers = sync.NewCond(&m.mu)
	m.writers = sync.NewCond(&m.mu)
	return m
}

func (m *writerPriorityRWMutex) RLock() {
	m.mu.Lock()
	for m.activeWrite || m.waitingW > 0 {
		m.readers.Wait()
	}
	m.activeRead++
	m.mu.Unlock()
}

func (m *writerPriorityRWMutex) RUnlock() {
	m.mu.Lock()
	m.activeRead--
	if m.activeRead == 0 && m.waitingW > 0 {
		m.writers.Signal()
	}
	m.mu.Unlock()
}

func (m *writerPriorityRWMutex) Lock() {
	m.mu.Lock()
	m.waitingW++
	for m.activeWrite || m.activeRead > 0 {
		m.writers.Wait()
	}
	m.waitingW--
	m.activeWrite = true
	m.mu.Unlock()
}

func (m *writerPriorityRWMutex) Unlock() {
	m.mu.Lock()
	m.activeWrite = false
	if m.waitingW > 0 {
		m.writers.Signal()
	} else {
		m.readers.Broadcast()
	}
	m.mu.Unlock()
}

// rwLock 保留为便于测试替身的最小接口。
type rwLock interface {
	RLock()
	RUnlock()
	Lock()
	Unlock()
}

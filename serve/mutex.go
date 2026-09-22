package serve

import "sync"

// mutex 是对 sync.RWMutex 的薄封装，集中表达组装器的加锁意图：
// 写路径（Build、WriteTo 推进游标）取写锁；只读查询取读锁。
type mutex struct {
	rw sync.RWMutex
}

func (m *mutex) lock()    { m.rw.Lock() }
func (m *mutex) unlock()  { m.rw.Unlock() }
func (m *mutex) rlock()   { m.rw.RLock() }
func (m *mutex) runlock() { m.rw.RUnlock() }

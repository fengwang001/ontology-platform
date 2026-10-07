package ontology

import "sync"

// rwmutex 是 sync.RWMutex 的薄封装，便于在一处审阅并发策略：
// 全部公开方法都经由它串行化，提供严格可串行化语义。
type rwmutex struct{ m sync.RWMutex }

func (r *rwmutex) lock()    { r.m.Lock() }
func (r *rwmutex) unlock()  { r.m.Unlock() }
func (r *rwmutex) rLock()   { r.m.RLock() }
func (r *rwmutex) rUnlock() { r.m.RUnlock() }

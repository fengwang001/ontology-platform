// Package lock 维护 FIFO 等待队列并做写者优先放行调度，仅依赖 rw。
package lock

import (
	"errors"
	"sync"

	"ontology/rw"
)

var (
	ErrEmptyID       = errors.New("rwlock: requester id must not be empty")
	ErrNotHeld       = errors.New("rwlock: lock is not held by this requester")
	ErrDoubleRelease = errors.New("rwlock: lock already released by this requester")
)

type waiter struct {
	id    string
	write bool
	ch    chan struct{}
}
type L struct {
	mu         sync.Mutex
	st         rw.State
	q          []*waiter
	relR, relW map[string]struct{}
}

func New() *L {
	return &L{relR: map[string]struct{}{}, relW: map[string]struct{}{}, st: *rw.New()}
}

// request 是 acquire 内核：满足条件当场授予，否则带唤醒通道入队。
func (l *L) request(id string, write bool) (bool, *waiter, error) {
	if id == "" {
		return false, nil, ErrEmptyID
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if (!write && l.st.CanRead()) || (write && l.st.CanWrite()) {
		if write {
			l.st.GrantWrite(id)
			delete(l.relW, id)
		} else {
			l.st.GrantRead(id)
			delete(l.relR, id)
		}
		return true, nil, nil
	}
	w := &waiter{id: id, write: write, ch: make(chan struct{})}
	l.q = append(l.q, w)
	if write {
		l.st.IncWaitWriter()
	}
	return false, w, nil
}
func (l *L) wait(id string, write bool) error {
	g, w, err := l.request(id, write)
	if err != nil || g {
		return err
	}
	<-w.ch
	return nil
}
func (l *L) AcquireRead(id string) error  { return l.wait(id, false) }
func (l *L) AcquireWrite(id string) error { return l.wait(id, true) }
func (l *L) Try(id string, write bool) (bool, error) {
	g, _, err := l.request(id, write)
	return g, err
}

// pump 持锁时放行：可写则放行最早写者（越过被挡读者），否则 FIFO 批量放读者。
func (l *L) pump() {
	for len(l.q) > 0 {
		fi := -1
		if l.st.CanWrite() {
			for i, w := range l.q {
				if w.write {
					fi = i
					break
				}
			}
		}
		if fi >= 0 {
			w := l.q[fi]
			l.st.GrantWrite(w.id)
			l.st.DecWaitWriter()
			l.q = append(l.q[:fi], l.q[fi+1:]...)
			close(w.ch)
			return
		}
		if l.st.CanRead() && !l.q[0].write {
			w := l.q[0]
			l.st.GrantRead(w.id)
			l.q = l.q[1:]
			close(w.ch)
			continue
		}
		return
	}
}

// release 是释放内核：未持有/重复释放返回不同哨兵错误，成功后 pump。
func (l *L) release(id string, write bool) error {
	if id == "" {
		return ErrEmptyID
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	rel := l.relR
	ok := false
	if write {
		ok, rel = l.st.ReleaseWrite(id), l.relW
	} else {
		ok = l.st.ReleaseRead(id)
	}
	if !ok {
		if _, dup := rel[id]; dup {
			return ErrDoubleRelease
		}
		return ErrNotHeld
	}
	rel[id] = struct{}{}
	l.pump()
	return nil
}
func (l *L) ReleaseRead(id string) error  { return l.release(id, false) }
func (l *L) ReleaseWrite(id string) error { return l.release(id, true) }
func (l *L) View() (readers []string, writer string, waiting []string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	waiting = make([]string, len(l.q))
	for i, w := range l.q {
		waiting[i] = w.id + "R"
		if w.write {
			waiting[i] = w.id + "W"
		}
	}
	return l.st.Readers(), l.st.Writer(), waiting
}
func (l *L) Readers() []string { r, _, _ := l.View(); return r }
func (l *L) Writer() string    { _, w, _ := l.View(); return w }
func (l *L) Waiting() []string { _, _, q := l.View(); return q }

// FlagDecisionO1 转引 rw 标志判定自检，只返回布尔，不暴露计数器数值。
func FlagDecisionO1() bool { return rw.FlagDecisionIsO1([]int{100, 1000, 10000}) }

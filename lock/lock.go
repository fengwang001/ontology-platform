// Package lock 在 state 状态机之上提供自旋获取/释放循环与升级判定。
// 被阻塞的操作自旋等待直到条件满足；错误分类沿用 state 的哨兵错误。
package lock

import (
	"runtime"

	"ontology/state"
)

type Lock struct {
	st *state.State
}

func New(st *state.State) *Lock { return &Lock{st: st} }

// AcquireRead 自旋获读；重入立即返回 ErrReaderReentry。
// 进入等待先登记为等待读者，获读或出错后注销。
func (l *Lock) AcquireRead(o int) error {
	ok, err := l.st.TryRead(o)
	if ok || err != nil {
		return err
	}
	l.st.WaitReader(o, true)
	defer l.st.WaitReader(o, false)
	for {
		runtime.Gosched()
		if ok, err = l.st.TryRead(o); err != nil {
			return err
		} else if ok {
			return nil
		}
	}
}

// AcquireWrite 自旋获写；重入立即返回 ErrWriterReentry。
// 一旦进入等待即置「有写者等待」，此后新读者不得先获读（写者优先）。
func (l *Lock) AcquireWrite(o int) error {
	ok, err := l.st.TryWrite(o)
	if ok || err != nil {
		return err
	}
	l.st.WaitWriter(1)
	defer l.st.WaitWriter(-1)
	for {
		runtime.Gosched()
		if ok, err = l.st.TryWrite(o); err != nil {
			return err
		} else if ok {
			return nil
		}
	}
}

func (l *Lock) ReleaseRead(o int) error  { return l.st.ReleaseRead(o) }
func (l *Lock) ReleaseWrite(o int) error { return l.st.ReleaseWrite(o) }

// TryUpgrade 读升写：单次原子判定，不阻塞。
func (l *Lock) TryUpgrade(o int) error { return l.st.Upgrade(o) }

func (l *Lock) Snapshot() state.Snapshot { return l.st.Snapshot() }

// Package stm 提供软件事务内存：多个执行体可在共享整数变量上
// 运行可组合的事务，支持阻塞重试（Retry）与选择（OrElse）。
package stm

import (
	"errors"
	"sync"
)

// MaxVars 是一次事务执行允许访问的不同变量数上限。
const MaxVars = 64

// 误用与中止错误。事务体内触发时事务中止且不产生任何写，
// 由 Atomically 原样返回。
var (
	// ErrTxnDone 事务结束后仍使用其句柄。
	ErrTxnDone = errors.New("stm: transaction handle used after completion")
	// ErrNested 在事务体内再启动事务。
	ErrNested = errors.New("stm: nested transaction")
	// ErrForeignVar 使用属于另一个内存实例的变量。
	ErrForeignVar = errors.New("stm: variable belongs to another STM instance")
	// ErrTooManyVars 一次执行访问的不同变量数超过 MaxVars。
	ErrTooManyVars = errors.New("stm: too many variables accessed in one execution")
	// ErrPermanentlyBlocked 读集为空的重试，永久阻塞。
	ErrPermanentlyBlocked = errors.New("stm: retry with empty read set would block forever")
)

// STM 是一个内存实例，持有一组共享整数变量。
type STM struct {
	mu     sync.Mutex
	cond   sync.Cond
	active map[int64]bool // 正在执行事务体的 goroutine，用于嵌套检测
}

// New 创建一个内存实例。
func New() *STM {
	s := &STM{active: make(map[int64]bool)}
	s.cond.L = &s.mu
	return s
}

// TVar 是属于某个 STM 实例的共享整数变量。
type TVar struct {
	owner   *STM
	value   int
	version uint64 // 每次被提交写入都递增（写入相同值也递增）
}

// NewVar 在内存实例上创建变量。
func (s *STM) NewVar(v int) *TVar {
	return &TVar{owner: s, value: v}
}

// Txn 是一次事务执行的句柄，仅在本次执行期间有效。
type Txn struct {
	stm    *STM
	reads  map[*TVar]uint64 // 读集：变量 -> 读时的版本
	values map[*TVar]int    // 读集：变量 -> 读到的值
	writes map[*TVar]int    // 写集：提交前仅本事务可见
	nvars  int              // 本次执行访问的不同变量数
	done   bool
}

// 控制信号，以 panic 形式在事务体内部传播，由框架捕获。
type retrySignal struct{}   // 主动重试
type restartSignal struct{} // 读集已失效，丢弃本次执行并重跑

// outcome 是一次事务体执行的结果。
type outcome struct {
	kind   int
	err    error
	panick any
}

const (
	outCommit = iota
	outRetry
	outRestart
	outError
	outPanic
)

// execute 运行一次事务体并捕获控制信号与用户恐慌。
func execute(tx *Txn, body func(*Txn) error) (o outcome) {
	defer func() {
		if r := recover(); r != nil {
			switch r.(type) {
			case retrySignal:
				o = outcome{kind: outRetry}
			case restartSignal:
				o = outcome{kind: outRestart}
			default:
				if err, ok := r.(error); ok && isMisuse(err) {
					// 误用：事务中止，错误原样返回
					o = outcome{kind: outError, err: err}
				} else {
					o = outcome{kind: outPanic, panick: r}
				}
			}
		}
	}()
	if err := body(tx); err != nil {
		return outcome{kind: outError, err: err}
	}
	return outcome{kind: outCommit}
}

// isMisuse 判断 panic 值是否为框架误用错误。
func isMisuse(err error) bool {
	switch err {
	case ErrTxnDone, ErrNested, ErrForeignVar, ErrTooManyVars, ErrPermanentlyBlocked:
		return true
	}
	return false
}

// Atomically 运行事务体：正常返回即提交（全部写同时对外可见）；
// 返回错误中止并原样返回该错误；恐慌中止并原样上抛。
func (s *STM) Atomically(body func(*Txn) error) error {
	gid := goid()
	s.mu.Lock()
	if s.active[gid] {
		s.mu.Unlock()
		return ErrNested
	}
	s.active[gid] = true
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.active, gid)
		s.mu.Unlock()
	}()

	for {
		tx := &Txn{
			stm:    s,
			reads:  make(map[*TVar]uint64),
			values: make(map[*TVar]int),
			writes: make(map[*TVar]int),
		}
		o := execute(tx, body)
		tx.done = true

		switch o.kind {
		case outError:
			return o.err // 中止：任何写都不生效，错误原样返回
		case outPanic:
			panic(o.panick) // 中止：恐慌原样上抛
		case outRestart:
			continue // 读集已失效，丢弃本次读写并重跑
		case outRetry:
			if len(tx.reads) == 0 {
				return ErrPermanentlyBlocked
			}
			// 阻塞到读集中某个变量被别的事务提交写入。
			// 校验与等待在同一把锁上进行：写入若发生在读之后、
			// 阻塞之前，版本已变化，校验失败会立即重跑，不丢失唤醒。
			s.mu.Lock()
			for tx.readsValidLocked() {
				s.cond.Wait()
			}
			s.mu.Unlock()
			continue
		default: // outCommit
			s.mu.Lock()
			if !tx.readsValidLocked() {
				s.mu.Unlock()
				continue
			}
			for v, x := range tx.writes {
				v.value = x
				v.version++ // 写入相同值也算一次写入
			}
			if len(tx.writes) > 0 {
				s.cond.Broadcast()
			}
			s.mu.Unlock()
			return nil
		}
	}
}

// readsValidLocked 检查读集中的变量自读取后是否都未被提交写入。
// 调用方必须持有 t.stm.mu。
func (t *Txn) readsValidLocked() bool {
	for v, ver := range t.reads {
		if v.version != ver {
			return false
		}
	}
	return true
}

// checkUsable 按固定顺序做误用检查：句柄已结束、外部变量、变量数超限。
func (t *Txn) checkUsable(v *TVar) {
	if t.done {
		panic(ErrTxnDone)
	}
	if v.owner != t.stm {
		panic(ErrForeignVar)
	}
	if _, ok := t.writes[v]; !ok {
		if _, ok := t.reads[v]; !ok {
			if t.nvars >= MaxVars {
				panic(ErrTooManyVars)
			}
			t.nvars++
		}
	}
}

// readVersion 返回变量在本次执行中的值，必要时把变量纳入读集。
// 首次访问时在锁内校验整个读集，保证本次执行观察到的全部值取自
// 同一个一致时刻；已失效则丢弃本次执行并从头重跑。
func (t *Txn) readVersion(v *TVar) int {
	if x, ok := t.writes[v]; ok {
		return x // 读己之写
	}
	if x, ok := t.values[v]; ok {
		return x
	}
	s := t.stm
	s.mu.Lock()
	if !t.readsValidLocked() {
		s.mu.Unlock()
		panic(restartSignal{})
	}
	t.reads[v] = v.version
	t.values[v] = v.value
	x := v.value
	s.mu.Unlock()
	return x
}

// Read 读取变量；读己之写。
func (t *Txn) Read(v *TVar) int {
	t.checkUsable(v)
	return t.readVersion(v)
}

// Write 写变量，提交前仅本事务可见。
func (t *Txn) Write(v *TVar, x int) {
	t.checkUsable(v)
	t.writes[v] = x
}

// Retry 丢弃本次执行的全部写，阻塞到读集中某个变量被别的事务
// 提交写入后重新执行；读集为空则以 ErrPermanentlyBlocked 中止。
func (t *Txn) Retry() {
	if t.done {
		panic(ErrTxnDone)
	}
	panic(retrySignal{})
}

// OrElse 返回一个组合事务体：先执行左分支；左分支正常返回则不执行
// 右分支；左分支重试则撤销它的全部写（它读过的变量仍计入本次读集），
// 再执行右分支且右分支看不到左分支的写；两分支都重试则整体重试，
// 读集为二者并集；左分支返回错误或恐慌则整个事务中止且不试右分支。
func OrElse(left, right func(*Txn) error) func(*Txn) error {
	return func(tx *Txn) error {
		saved := tx.writes
		tx.writes = make(map[*TVar]int) // 左分支的写放入暂存区
		o := execute(tx, left)
		pending := tx.writes
		tx.writes = saved

		switch o.kind {
		case outCommit:
			for v, x := range pending {
				saved[v] = x // 左分支成功，写并入本次执行
			}
			return nil
		case outRetry:
			// 撤销左分支的写（丢弃暂存区），其读集已并入 tx.reads；
			// 右分支直接写入本次执行的写集，看不到左分支的写。
			return runRight(tx, right)
		case outRestart:
			panic(restartSignal{})
		case outError:
			return o.err
		default: // outPanic
			panic(o.panick)
		}
	}
}

// runRight 执行右分支；右分支重试则整体重试（读集已是两分支并集）。
func runRight(tx *Txn, right func(*Txn) error) error {
	o := execute(tx, right)
	switch o.kind {
	case outCommit:
		return nil
	case outRetry:
		panic(retrySignal{})
	case outRestart:
		panic(restartSignal{})
	case outError:
		return o.err
	default: // outPanic
		panic(o.panick)
	}
}

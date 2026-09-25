// Package api 是 ticket 自旋退避锁的对外接口：New/Acquire/Release/
// TryAcquire/State/SelfCheck。只依赖 spin（进而依赖 tick），依赖方向单向。
package api

import (
	"errors"
	"fmt"
	"runtime"
	"time"

	"ontology/spin"
)

// 三类互不相同的可判定哨兵错误：参数非法 / 号不符 / 超时。
var (
	ErrInvalidMaxBackoff = errors.New("ticklock: maxBackoff must be positive")
	ErrWrongTicket       = errors.New("ticklock: release ticket does not match serving")
	ErrTimeout           = spin.ErrTimeout
)

// Lock 是公平的 ticket 自旋退避锁，全部状态在进程内存、仅用标准库。
// api 只依赖 spin（spin → tick），依赖方向严格单向。
type Lock struct {
	s *spin.Spinner
}

// New 以退避上限创建锁；maxBackoff 非正整体失败、不创建任何状态。
func New(maxBackoff time.Duration) (*Lock, error) {
	if maxBackoff <= 0 {
		return nil, ErrInvalidMaxBackoff
	}
	return &Lock{s: spin.NewOwn(maxBackoff)}, nil
}

// Acquire 原子取号并自旋直到轮到本号，返回票号；授予严格按号递增。
func (l *Lock) Acquire() int { return l.s.Acquire() }

// Release 必须校验 ticket==serving：不符返回 ErrWrongTicket 且绝不改 serving；
// 相符才把 serving 原子加 1，交给下一个号。
func (l *Lock) Release(ticket int) error {
	if !l.s.Handover(ticket) {
		return ErrWrongTicket
	}
	return nil
}

// TryAcquire 在 deadline 内尝试获锁；超时返回 ErrTimeout，不获锁、不改状态。
func (l *Lock) TryAcquire(deadline time.Duration) (int, error) {
	return l.s.TryAcquire(deadline)
}

// State 返回 (next, serving)，仅供测试与 SelfCheck；差值即等待者数。
func (l *Lock) State() (next, serving int) { return l.s.Next(), l.s.Serving() }

// SelfCheck 对内置的第三节八步序列核验四条不变量，返回首个失败的描述。
// A/B/C 在三个 goroutine 中并发取号自旋，主 goroutine 严格按步驱动释放。
func (l *Lock) SelfCheck() error {
	const wait = time.Second
	got := make([]chan int, 3)
	for i := range got {
		got[i] = make(chan int, 1)
	}
	wantState := func(step, wn, ws int) error {
		if n, s := l.State(); n != wn || s != ws {
			return fmt.Errorf("step %d: next=%d serving=%d, want %d,%d", step, n, s, wn, ws)
		}
		return nil
	}
	waitFor := func(cond func() bool) error {
		deadline := time.Now().Add(wait)
		for !cond() {
			if time.Now().After(deadline) {
				return errors.New("ticklock: selfcheck timed out waiting for expected state")
			}
			runtime.Gosched()
		}
		return nil
	}
	// 步 1-3：A、B、C 依次（取号完成一个再启动下一个）取号 0/1/2，serving 恒为 0。
	for i := 0; i < 3; i++ {
		go func(ch chan<- int) { ch <- l.Acquire() }(got[i])
		if err := waitFor(func() bool { n, _ := l.State(); return n == i+1 }); err != nil {
			return fmt.Errorf("step %d: %w", i+1, err)
		}
		if err := wantState(i+1, i+1, 0); err != nil {
			return err
		}
	}
	// 步 4：A Release(0)，serving 0->1。
	a := <-got[0]
	if a != 0 {
		return fmt.Errorf("step 1: A granted %d, want 0", a)
	}
	if err := l.Release(0); err != nil {
		return fmt.Errorf("step 4: %w", err)
	}
	if err := wantState(4, 3, 1); err != nil {
		return err
	}
	// 步 5：B（号 1）获锁。
	b := <-got[1]
	if b != 1 {
		return fmt.Errorf("step 5: B granted %d, want 1 (FIFO)", b)
	}
	if err := wantState(5, 3, 1); err != nil {
		return err
	}
	// 步 6：B Release(1)。
	if err := l.Release(b); err != nil {
		return fmt.Errorf("step 6: %w", err)
	}
	if err := wantState(6, 3, 2); err != nil {
		return err
	}
	// 步 7：C（号 2）获锁。
	c := <-got[2]
	if c != 2 {
		return fmt.Errorf("step 7: C granted %d, want 2 (FIFO)", c)
	}
	if err := wantState(7, 3, 2); err != nil {
		return err
	}
	// 步 8：C Release(2)，锁全空。
	if err := l.Release(c); err != nil {
		return fmt.Errorf("step 8: %w", err)
	}
	return wantState(8, 3, 3)
}

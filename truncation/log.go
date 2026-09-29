package truncation

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"time"
)

// ErrCrashed 实例已被 InjectCrash 模拟崩溃，拒绝后续写操作。
var ErrCrashed = errors.New("truncation: instance crashed")

// Entry 日志条目，Offset 为连续偏移序号。
type Entry struct {
	Offset uint64
	Data   []byte
}

// Status 某一时刻的一致性快照，用于自检与测试日志打印。
type Status struct {
	Start   uint64 // 实际起始偏移（当前可见第一条）
	End     uint64 // 已追加的最大偏移（下一条为 End+1）
	Durable uint64 // 持久化位点：<= Durable 的条目均已落盘
	Marker  uint64 // 已落盘的截断标记
	Entries int    // 当前可见条目数
}

// Log 位点一致维护器。读与自检走 RLock，追加/截断走 Lock。
type Log struct {
	mu      sync.RWMutex
	dir     string
	start   uint64  // 实际起始偏移
	entries []Entry // 连续偏移的可见条目，entries[0].Offset == start
	durable uint64  // 持久化位点，只进不退
	marker  uint64  // 截断标记，已落盘
	crashed bool    // 模拟崩溃后未恢复时拒绝写操作

	// crashHook 在截断标记落盘之后、物理删除之前触发，用于测试注入崩溃。
	crashHook func()
}

// Open 打开（或创建）位于 dir 的日志，并执行崩溃恢复判定。
//
// 判定规则（M 为截断标记，S 为实际起始偏移，语义为“偏移 <= M 的条目应被截断”）：
//   - S == M+1：一致，干净；
//   - S <= M：截断中途崩溃，补删 (S, M] 前缀收敛；
//   - S > M+1：越删，报告损坏并拒绝打开。
func Open(dir string) (*Log, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	start, entries, err := readLog(dir)
	if err != nil {
		return nil, err
	}
	marker, err := readMarker(dir)
	if err != nil {
		return nil, err
	}
	l := &Log{dir: dir, start: start, entries: entries, marker: marker}
	if err := l.recover(); err != nil {
		return nil, err
	}
	return l, nil
}

// recover 崩溃恢复双判定。Open 时单线程执行，无需持锁。
func (l *Log) recover() error {
	switch {
	case l.start == l.marker+1:
		// 干净：标记与实际起始一致。
		return nil
	case l.start <= l.marker:
		// 截断中途崩溃：标记已推进但前缀未删完，补删收敛。
		if err := l.deletePrefix(l.marker); err != nil {
			return err
		}
		return nil
	default:
		// 越删：实际起始越过标记，日志已损坏，整体拒绝。
		return &OpError{
			Op:     "Recover",
			Reason: ErrOverDelete,
			Detail: fmt.Sprintf("start=%d marker=%d", l.start, l.marker),
		}
	}
}

// deletePrefix 物理删除偏移 <= target 的前缀（磁盘重写 + 内存裁剪）。
// 调用方需持有写锁或处于 Open 恢复路径。
func (l *Log) deletePrefix(target uint64) error {
	keep := make([]Entry, 0, len(l.entries))
	for _, e := range l.entries {
		if e.Offset > target {
			keep = append(keep, e)
		}
	}
	newStart := target + 1
	if newStart < l.start {
		newStart = l.start
	}
	if err := rewriteLog(l.dir, newStart, keep); err != nil {
		return err
	}
	l.entries = keep
	l.start = newStart
	return nil
}

// Append 追加一条数据，返回分配的连续偏移。
func (l *Log) Append(data []byte) (uint64, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.crashed {
		return 0, &OpError{Op: "Append", Reason: ErrCrashed, Detail: "instance unusable after crash"}
	}
	offset := l.end() + 1
	e := Entry{Offset: offset, Data: append([]byte(nil), data...)}
	if err := appendEntry(l.dir, l.start, e); err != nil {
		return 0, err
	}
	l.entries = append(l.entries, e)
	return offset, nil
}

// DeclareDurable 声明持久化位点：只进不退，且不得超过已追加最大偏移。
// 先校验后落盘，失败不改变日志、持久化位点与标记。
func (l *Log) DeclareDurable(offset uint64) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.crashed {
		return &OpError{Op: "DeclareDurable", Reason: ErrCrashed, Detail: "instance unusable after crash"}
	}
	if offset > l.end() {
		return opErr("DeclareDurable", ErrDurableOutOfRange,
			"offset=%d beyond end=%d", offset, l.end())
	}
	if offset < l.durable {
		return opErr("DeclareDurable", ErrDurableRegression,
			"offset=%d below durable=%d", offset, l.durable)
	}
	if err := syncLog(l.dir); err != nil {
		return err
	}
	l.durable = offset
	return nil
}

// Truncate 两步截断：先落截断标记，再物理删除 [start, target] 前缀。
// 任一前置校验失败都不改变日志、持久化位点与标记。
func (l *Log) Truncate(target uint64) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.crashed {
		return &OpError{Op: "Truncate", Reason: ErrCrashed, Detail: "instance unusable after crash"}
	}
	// 前置校验：全部通过前不做任何修改。
	if target < l.start || target > l.end() {
		return opErr("Truncate", ErrTruncateOutOfRange,
			"target=%d not in [%d, %d]", target, l.start, l.end())
	}
	if target > l.durable {
		return opErr("Truncate", ErrTruncateNotDurable,
			"target=%d beyond durable=%d", target, l.durable)
	}
	// 第一步：落截断标记。
	if err := writeMarker(l.dir, target); err != nil {
		return err
	}
	l.marker = target
	// 崩溃注入点：标记已落盘、前缀未删，模拟截断中途崩溃。
	if l.crashHook != nil {
		l.crashHook()
	}
	if l.crashed {
		return &OpError{Op: "Truncate", Reason: ErrCrashed, Detail: "crashed after marker, before physical delete"}
	}
	// 第二步：物理删除前缀。
	return l.deletePrefix(target)
}

// ReadRange 读取 [from, to] 闭区间的连贯条目快照。
// 与追加、截断并发安全；快照要么完整要么报错，绝不返回部分可见的中间态。
func (l *Log) ReadRange(from, to uint64) ([]Entry, error) {
	l.mu.RLock()
	defer l.mu.RUnlock()
	if from > to || from < l.start || to > l.end() {
		return nil, opErr("ReadRange", ErrRangeUnavailable,
			"range=[%d,%d] available=[%d,%d]", from, to, l.start, l.end())
	}
	out := make([]Entry, 0, to-from+1)
	for _, e := range l.entries[from-l.start : to-l.start+1] {
		out = append(out, Entry{Offset: e.Offset, Data: append([]byte(nil), e.Data...)})
	}
	return out, nil
}

// Check 并发安全的自检：返回一致性快照，并校验内部不变量。
func (l *Log) Check() (Status, error) {
	l.mu.RLock()
	defer l.mu.RUnlock()
	st := l.statusLocked()
	for i, e := range l.entries {
		if want := l.start + uint64(i); e.Offset != want {
			return st, fmt.Errorf("truncation: self-check failed: offset gap at index %d, want %d got %d", i, want, e.Offset)
		}
	}
	if l.durable > l.end() {
		return st, fmt.Errorf("truncation: self-check failed: durable=%d beyond end=%d", l.durable, l.end())
	}
	if l.marker > 0 && l.start < l.marker+1 {
		return st, fmt.Errorf("truncation: self-check failed: start=%d behind marker=%d", l.start, l.marker)
	}
	return st, nil
}

// Status 返回当前一致性快照（并发安全）。
func (l *Log) Status() Status {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.statusLocked()
}

func (l *Log) statusLocked() Status {
	return Status{
		Start:   l.start,
		End:     l.end(),
		Durable: l.durable,
		Marker:  l.marker,
		Entries: len(l.entries),
	}
}

// end 已追加的最大偏移；空日志时为 start-1。
func (l *Log) end() uint64 {
	if n := len(l.entries); n > 0 {
		return l.entries[n-1].Offset
	}
	return l.start - 1
}

// InjectCrash 模拟进程崩溃：实例此后拒绝写操作，磁盘状态保持当前样子。
// 仅测试使用：要么在 SetCrashHook 的钩子内调用（钩子已在写锁内执行），
// 要么在无并发操作时调用。
func (l *Log) InjectCrash() {
	l.crashed = true
}

// SetCrashHook 设置截断崩溃注入钩子（仅测试用）：在标记落盘后、物理删除前调用。
func (l *Log) SetCrashHook(hook func()) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.crashHook = hook
}

// ReclaimDurable 回收一次：把已持久化的前缀截断到 durable 位点。
// durable 未越过 start 或日志为空时为空操作。
func (l *Log) ReclaimDurable() error {
	st := l.Status()
	target := st.Durable
	if target > st.End {
		target = st.End
	}
	if st.Entries == 0 || target < st.Start {
		return nil
	}
	return l.Truncate(target)
}

// StartReclaimer 启动周期回收：每隔 interval 截断到当前持久化位点。
// 返回的函数用于停止回收并等待退出。
func (l *Log) StartReclaimer(ctx context.Context, interval time.Duration) (stop func()) {
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				_ = l.ReclaimDurable()
			}
		}
	}()
	return func() {
		<-done
	}
}

// Close 关闭日志。
func (l *Log) Close() error {
	return nil
}

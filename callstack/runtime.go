package callstack

import (
	"fmt"
	"io"
	stdlog "log"
	"os"
)

// Call 从空栈开始执行一次顶层调用。任何被拒绝的调用都不改变
// 帧栈、折叠计数、配额使用量与任何统计量。
func (in *Interpreter) Call(name string, args []int64) (value int64, err error) {
	in.execMu.Lock()
	defer in.execMu.Unlock()

	in.mu.Lock()
	defer in.mu.Unlock()

	if in.closed {
		in.logLocked("input call %s%v -> rejected: instance closed", name, args)
		return 0, newErrorf(ErrClosed, "interpreter is closed")
	}

	// 顶层调用：空栈压入第一帧。它豁免深度（此时帧数为 0），
	// 但不豁免配额。
	fn, callErr := in.checkCallLocked(name, args)
	if callErr != nil {
		in.logLocked("input call %s%v -> rejected: %s (priority: undefined>arity>depth>quota)",
			name, args, callErr.Kind)
		return 0, callErr
	}
	in.pushLocked(fn, args, 0)
	in.logLocked("input call %s%v -> accepted top-level; basis empty-stack depth=%d slots=%d",
		name, args, len(in.frames), in.slots)
	in.fireBoundaryLocked()

	// 单次调用全程持锁：读取者只能拿到“调用边界”上的一致快照，
	// 不可能观察到半个调用的中间态。
	defer func() {
		if r := recover(); r != nil {
			switch x := r.(type) {
			case *Error:
				err = x
			case *thrown:
				// 异常未被任何区域处理。非尾帧已在 enter 的展开中逐帧
				// 登记并弹出；尾调用链在物理上只剩当前一帧，它在本层
				// 补上自身的折叠计数，从而不遗漏“当前帧”。
				if len(in.frames) > 0 {
					top := in.frames[len(in.frames)-1]
					x.path = append(x.path, FrameInfo{
						Function: top.fn.Name,
						Folded:   top.folded,
					})
				}
				// path 自内向外收集，反转后外层在前，与 Backtrace 一致。
				trace := reversePath(x.path)
				err = &Error{
					Kind:    ErrUnhandled,
					Message: "exception propagated through every frame without a handler",
					Payload: x.payload,
					Trace:   trace,
				}
			default:
				panic(r)
			}
		}
		// 顶层调用结束时栈必然已空（正常返回弹首帧；异常逐帧弹出）。
		in.frames = nil
		in.slots = 0
		in.regions = nil
		stats := in.statsLocked()
		if err != nil {
			in.logLocked("input call %s%v -> output error %v; stats depth=%d slots=%d reuses=%d maxDepth=%d",
				name, args, err, stats.Depth, stats.Slots, stats.TailReuses, stats.MaxDepth)
		} else {
			in.logLocked("input call %s%v -> output %d; stats depth=%d slots=%d reuses=%d maxDepth=%d",
				name, args, value, stats.Depth, stats.Slots, stats.TailReuses, stats.MaxDepth)
		}
	}()

	value = in.runOneFrame()
	return value, nil
}

func reversePath(p []FrameInfo) []FrameInfo {
	out := make([]FrameInfo, len(p))
	for i, f := range p {
		out[len(p)-1-i] = f
	}
	return out
}

// Stats 返回某一瞬间一致的四项统计。
func (in *Interpreter) Stats() Stats {
	in.mu.Lock()
	defer in.mu.Unlock()
	return in.statsLocked()
}

// Backtrace 返回当前调用回溯（自底向上，外层在前）。
func (in *Interpreter) Backtrace() []FrameInfo {
	in.mu.Lock()
	defer in.mu.Unlock()
	return in.snapshotTraceLocked()
}

// Close 关闭实例；关闭后的调用返回 ErrClosed，且不改变任何状态。
func (in *Interpreter) Close() {
	in.execMu.Lock()
	defer in.execMu.Unlock()
	in.mu.Lock()
	in.closed = true
	in.logLocked("interpreter closed")
	in.mu.Unlock()
}

// runOneFrame 执行栈顶帧，处理该帧函数体中的全部尾调用（逐个原子
// 复用本帧），直到返回正常值或抛出异常（异常向上穿透本帧）。
func (in *Interpreter) runOneFrame() int64 {
	for {
		top := in.frames[len(in.frames)-1]
		var result int64
		var tail *tailInvoke

		func() {
			defer func() {
				if r := recover(); r != nil {
					if t, ok := r.(*tailInvoke); ok {
						tail = t
						return
					}
					panic(r)
				}
			}()
			result = in.eval(top.fn.Body)
		}()

		if tail != nil {
			// 校验已在 eval 中完成；此处原子提交复用事务。
			in.applyTailLocked(tail)
			in.fireBoundaryLocked()
			continue
		}
		return result
	}
}

// enter 执行一次非尾调用：压入新帧并求值，无论正常返回还是异常
// 传播都弹出该帧（异常路径登记一帧传播记录）。
func (in *Interpreter) enter(fn *Function, args []int64) int64 {
	// 新帧的折叠计数从零开始；此前的尾调用省略都归属在它之前。
	in.pushLocked(fn, args, 0)
	in.logLocked("non-tail enter %s; basis new frame depth=%d slots=%d",
		fn.Name, len(in.frames), in.slots)
	in.fireBoundaryLocked()

	defer func() {
		if r := recover(); r != nil {
			if t, ok := r.(*thrown); ok {
				popped := in.frames[len(in.frames)-1]
				t.path = append(t.path, FrameInfo{
					Function: popped.fn.Name,
					Folded:   popped.folded,
				})
				in.popLocked()
			} else {
				in.popLocked()
			}
			panic(r)
		}
		in.popLocked()
	}()

	return in.runOneFrame()
}

// checkCallLocked 按固定优先级校验一次调用。
//  1. 未定义函数（最高）；
//  2. 参数个数不匹配，优先于深度与配额；
//  3. 深度超限（仅非尾调用；复用帧不加深栈）；
//  4. 配额超限。深度与配额同时成立时报深度错误。
//
// 顶层调用时帧数为 0，天然通过深度检查。
func (in *Interpreter) checkCallLocked(name string, args []int64) (*Function, *Error) {
	fn, ok := in.funcs[name]
	if !ok {
		return nil, newErrorf(ErrUndefinedFunction, "function %q is not defined", name)
	}
	if len(args) != len(fn.Params) {
		return nil, newErrorf(ErrArity, "function %q expects %d args, got %d",
			name, len(fn.Params), len(args))
	}
	if in.maxDepth > 0 && len(in.frames) >= in.maxDepth {
		return nil, newErrorf(ErrDepth, "depth limit %d reached before call to %q",
			in.maxDepth, name)
	}
	if in.maxSlots > 0 && in.slots+fn.Slots > in.maxSlots {
		return nil, newErrorf(ErrQuota, "quota %d exceeded: need %d slots, %d in use",
			in.maxSlots, fn.Slots, in.slots)
	}
	return fn, nil
}

func (in *Interpreter) pushLocked(fn *Function, args []int64, folded int) {
	in.nextFrame++
	f := newFrame(in.nextFrame, fn, args)
	f.folded = folded
	in.frames = append(in.frames, f)
	in.slots += fn.Slots
	if len(in.frames) > in.histMax {
		in.histMax = len(in.frames)
	}
}

func (in *Interpreter) popLocked() *frame {
	n := len(in.frames)
	f := in.frames[n-1]
	in.frames = in.frames[:n-1]
	in.slots -= f.fn.Slots
	in.dropRegionsLocked(f.id)
	return f
}

// applyTailLocked 在同一临界区内原子完成“释放旧帧 + 分配新帧”。
// 可能失败的校验全部在求值阶段先完成，因此本函数只提交、不失败，
// 复用帧不增加深度，失败时根本不会走到这里（原帧完好）。
func (in *Interpreter) applyTailLocked(t *tailInvoke) {
	old := in.frames[len(in.frames)-1]
	newFolded := old.folded + 1

	in.slots -= old.fn.Slots
	in.nextFrame++
	fresh := newFrame(in.nextFrame, t.fn, t.args)
	fresh.folded = newFolded
	in.frames[len(in.frames)-1] = fresh
	in.slots += t.fn.Slots
	if len(in.frames) > in.histMax {
		in.histMax = len(in.frames)
	}
	in.reuses++

	// 原帧登记的保护区域立即失效。
	in.dropRegionsLocked(old.id)
	in.logLocked("tail reuse %s -> %s; folded=%d (basis: syntactic tail position)",
		old.fn.Name, t.fn.Name, newFolded)
}

// dropRegionsLocked 移除区域栈顶部所有属于已失效帧身份的区域。
func (in *Interpreter) dropRegionsLocked(frameID uint64) {
	for len(in.regions) > 0 && in.regions[len(in.regions)-1].frameID == frameID {
		in.regions = in.regions[:len(in.regions)-1]
	}
}

// popRegion 移除区域栈顶部指定的登记项。嵌套区域按 LIFO 退出。
func (in *Interpreter) popRegion(entry *regionEntry) {
	for i := len(in.regions) - 1; i >= 0; i-- {
		if in.regions[i] == entry {
			in.regions = append(in.regions[:i], in.regions[i+1:]...)
			return
		}
	}
}

// regionActive 判断区域是否仍登记，且登记它的帧身份仍是当前栈顶帧。
// 尾调用复用帧会产生新的帧身份，从而使旧区域失效。
func (in *Interpreter) regionActive(entry *regionEntry) bool {
	for i := len(in.regions) - 1; i >= 0; i-- {
		if in.regions[i] == entry {
			top := in.frames[len(in.frames)-1]
			return entry.frameID == top.id
		}
	}
	return false
}

func (in *Interpreter) statsLocked() Stats {
	return Stats{
		Depth:      len(in.frames),
		Slots:      in.slots,
		TailReuses: in.reuses,
		MaxDepth:   in.histMax,
	}
}

func (in *Interpreter) fireBoundaryLocked() {
	if in.boundary != nil {
		// 钩子在锁内执行：只传入快照数据，不得回调同一实例。
		in.boundary(in.statsLocked(), in.snapshotTraceLocked())
	}
}

// logLocked 输出一条诊断日志：包含输入、实际输出与判定依据。
// 调用方必须持有 in.mu（日志可能与一次调用的状态变更交错出现，
// 但每条记录自身描述的是一个已完成的判定）。
func (in *Interpreter) logLocked(format string, args ...any) {
	if in.logger == nil {
		return
	}
	in.logger.Logf(format, args...)
}

// NewLogger 把日志写入给定 Writer（典型为 os.Stderr 或测试缓冲）。
func NewLogger(w io.Writer) Logger {
	if w == nil {
		w = io.Discard
	}
	return &stdLogger{l: stdlog.New(w, "[callstack] ", stdlog.LstdFlags|stdlog.Lmicroseconds)}
}

type stdLogger struct{ l *stdlog.Logger }

func (s *stdLogger) Logf(format string, args ...any) {
	s.l.Output(2, fmt.Sprintf(format, args...))
}

// StdLogger 写入标准错误的默认日志实例。
var StdLogger = NewLogger(os.Stderr)

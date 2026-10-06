package callstack

import (
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync"
)

// instance.go 是运行时核心：解释器实例、调用事务、帧栈协作、
// 异常传播、统计快照与日志。
//
// 并发模型：
//   - 一个实例上的调用推进只允许一个执行流（同一条 goroutine 递归）；
//   - 帧栈/统计的每次变更都是调用边界处的一个短临界区（mu.Lock），
//     临界区之外栈始终停在合法的调用边界状态；
//   - 统计与回溯读取走 mu.RLock 并一次性拷贝快照，因而读到的
//     深度/配额/复用/最大深度必然对应同一瞬间；
//   - 多个实例各自持有独立的 Stack 与锁，天然互不干扰。

// Stats 是某一瞬间四项统计的一致快照。
type Stats struct {
	Depth     int // 当前帧数
	SlotsUsed int // 当前各帧槽位总数
	Reuses    int // 累计尾复用次数（实例生命周期内）
	MaxDepth  int // 历史最大帧数
}

// Logger 记录每条调用输入、实际输出与判定依据。实现须自身并发安全。
type Logger interface {
	Log(input, output, basis string)
}

// Config 构造实例的参数。负值表示不限；0 是合法的显式零限额
// （任何额外帧都立即失败）。
type Config struct {
	MaxDepth int
	MaxSlots int
	Logger   Logger
}

// Instance 是一个独立解释器实例。
type Instance struct {
	prog     *Program
	maxDepth int
	maxSlots int
	logger   Logger

	mu     sync.RWMutex
	closed bool
	stack  *Stack

	reuses   int
	maxHist  int
	mainFunc *Func
}

// NewInstance 为同一程序创建独立实例。
func NewInstance(prog *Program, cfg Config) *Instance {
	md, ms := cfg.MaxDepth, cfg.MaxSlots
	if md < 0 {
		md = int(^uint(0) >> 1)
	}
	if ms < 0 {
		ms = int(^uint(0) >> 1)
	}
	return &Instance{
		prog:     prog,
		maxDepth: md,
		maxSlots: ms,
		logger:   cfg.Logger,
		mainFunc: &Func{Name: "<main>", NSlots: prog.MainSlots},
	}
}

// Close 关闭实例；关闭后发起的运行/调用报 ErrClosed。
func (in *Instance) Close() {
	in.mu.Lock()
	in.closed = true
	in.mu.Unlock()
}

// IsClosed 报告实例是否已关闭（一致读）。
func (in *Instance) IsClosed() bool {
	in.mu.RLock()
	defer in.mu.RUnlock()
	return in.closed
}

// Stats 读取同一瞬间的四项统计。
func (in *Instance) Stats() Stats {
	in.mu.RLock()
	defer in.mu.RUnlock()
	d, u := 0, 0
	if in.stack != nil {
		d, u = in.stack.depth(), in.stack.used
	}
	return Stats{
		Depth:     d,
		SlotsUsed: u,
		Reuses:    in.reuses,
		MaxDepth:  in.maxHist,
	}
}

// Backtrace 读取当前回溯（最内层帧在前），开销与折叠帧数无关。
func (in *Instance) Backtrace() []BacktraceFrame {
	in.mu.RLock()
	defer in.mu.RUnlock()
	if in.stack == nil {
		return []BacktraceFrame{}
	}
	return in.stack.backtrace()
}

func (in *Instance) topFrame() *Frame {
	in.mu.RLock()
	defer in.mu.RUnlock()
	if in.stack == nil || in.stack.depth() == 0 {
		return nil
	}
	return in.stack.top()
}

// withBacktrace 在已持有写锁的拒绝路径上附上拒绝瞬间的回溯快照。
// 因为拒绝发生在任何修改之前，该回溯就是原帧栈。
func (in *Instance) withBacktrace(e *Error) *Error {
	e.Backtrace = in.stack.backtrace()
	return e
}

// ---------- 内部异常与错误信号 ----------

type exception struct {
	value int64
	trace []BacktraceFrame
}

type aborted struct{ err *Error }

// ---------- 求值器（尾调用为循环，非尾调用才递归） ----------

// RunMain 运行程序主表达式。返回整数值或分类错误。
func (in *Instance) RunMain() (result int64, retErr error) {
	in.mu.Lock()
	if in.closed {
		in.mu.Unlock()
		return 0, newError(ErrClosed, "instance is closed")
	}
	if in.stack != nil && in.stack.depth() != 0 {
		in.mu.Unlock()
		return 0, newError(ErrClosed, "an execution is already in progress on this instance")
	}
	if 1 > in.maxDepth {
		in.mu.Unlock()
		return 0, newError(ErrDepth, "main frame exceeds max depth %d", in.maxDepth)
	}
	if in.mainFunc.NSlots > in.maxSlots {
		in.mu.Unlock()
		return 0, newError(ErrQuota, "main frame needs %d slots but quota is %d", in.mainFunc.NSlots, in.maxSlots)
	}
	in.stack = &Stack{}
	in.stack.push(in.mainFunc, nil)
	if in.maxHist < 1 {
		in.maxHist = 1
	}
	in.mu.Unlock()

	defer func() {
		in.mu.Lock()
		if in.stack.depth() > 0 {
			in.stack.pop()
		}
		in.mu.Unlock()
		if r := recover(); r != nil {
			switch x := r.(type) {
			case exception:
				retErr = &Error{Kind: ErrUnhandled, Message: "exception escaped all protected regions", Value: x.value, Backtrace: x.trace}
			case aborted:
				retErr = x.err
			default:
				panic(r)
			}
		}
	}()

	result = in.eval(in.prog.Main)
	return result, nil
}

func (in *Instance) eval(e Expr) int64 {
	for {
		switch n := e.(type) {
		case *NumExpr:
			return n.Value
		case *VarExpr:
			return in.topFrame().Slots[n.Slot]
		case *BinExpr:
			l := in.eval(n.Lhs)
			r := in.eval(n.Rhs)
			return applyBin(n.Op, l, r)
		case *IfExpr:
			if in.eval(n.Cond) != 0 {
				e = n.Then
			} else {
				e = n.Else
			}
		case *SeqExpr:
			for _, x := range n.Exprs[:len(n.Exprs)-1] {
				in.eval(x)
			}
			e = n.Exprs[len(n.Exprs)-1]
		case *LetExpr:
			v := in.eval(n.Init)
			in.topFrame().Slots[n.Slot] = v
			e = n.Body
		case *TryExpr:
			return in.evalTry(n)
		case *ThrowExpr:
			v := in.eval(n.Arg)
			in.mu.RLock()
			bt := in.stack.backtrace()
			in.mu.RUnlock()
			panic(exception{value: v, trace: bt})
		case *CallExpr:
			args := make([]int64, len(n.Args))
			for i, a := range n.Args {
				args[i] = in.eval(a)
			}
			fn, action, basis, err := in.commitCall(n, args)
			in.logCall(n, args, action, basis, err)
			if err != nil {
				panic(aborted{err})
			}
			if n.Tail {
				e = fn.Body
				continue
			}
			return in.enterNonTail(fn, args)
		default:
			panic(aborted{newError(ErrUndefined, "unknown expression node")})
		}
	}
}

// enterNonTail 压入新帧、求值、无论正常返回还是异常传播都弹出该帧。
func (in *Instance) enterNonTail(fn *Func, args []int64) (v int64) {
	func() {
		defer func() {
			in.mu.Lock()
			in.stack.pop()
			in.mu.Unlock()
		}()
		v = in.eval(fn.Body)
	}()
	return v

}

// evalTry 在当前帧内执行保护区域；异常在当前帧被捕获时，
// 不会弹出当前帧（处理区域属于当前帧）。
func (in *Instance) evalTry(t *TryExpr) (v int64) {
	defer func() {
		if r := recover(); r != nil {
			ex, ok := r.(exception)
			if !ok {
				panic(r)
			}
			in.topFrame().Slots[t.ExcSlot] = ex.value
			v = in.eval(t.Handler)
			return
		}
	}()
	return in.eval(t.Body)
}

// commitCall 在调用边界的单一临界区里完成：
// 关闭检查 -> 未定义 -> 参数 -> 深度 -> 配额 -> 落盘修改。
// 任何拒绝都发生在修改之前，因此帧栈/折叠计数/配额/统计原样不动。
func (in *Instance) commitCall(c *CallExpr, args []int64) (fn *Func, action, basis string, err *Error) {
	in.mu.Lock()
	defer in.mu.Unlock()

	if in.closed {
		return nil, "rejected", "instance closed before lookup",
			in.withBacktrace(newError(ErrClosed, "instance is closed"))
	}
	fn, ok := in.prog.Funcs[c.Name]
	if !ok {
		return nil, "rejected", "function table lookup: not found",
			in.withBacktrace(newError(ErrUndefined, "undefined function %q", c.Name))
	}
	if len(args) != len(fn.Params) {
		return nil, "rejected",
			fmt.Sprintf("arity check: got %d want %d", len(args), len(fn.Params)),
			in.withBacktrace(newError(ErrArity, "function %q expects %d arguments, got %d",
				c.Name, len(fn.Params), len(args)))
	}

	if c.Tail {
		if !in.stack.canReplace(fn, in.maxSlots) {
			top := in.stack.top()
			return nil, "rejected",
				fmt.Sprintf("atomic tail-reuse quota check: release %d then allocate %d over quota %d (used %d)",
					top.Func.NSlots, fn.NSlots, in.maxSlots, in.stack.used),
				in.withBacktrace(newError(ErrQuota, "tail call to %q needs %d slots; reuse would exceed quota %d",
					c.Name, fn.NSlots, in.maxSlots))
		}
		in.stack.replaceTail(fn, args)
		in.reuses++
		return fn, "reused-frame",
			fmt.Sprintf("tail-position=%v (%s); reuse current frame; depth unchanged", c.Tail, c.TailReason), nil
	}

	depthOK, quotaOK := in.stack.canPush(fn, in.maxDepth, in.maxSlots)
	if !depthOK {
		return nil, "rejected",
			fmt.Sprintf("depth check: %d+1 > %d (checked before quota)", in.stack.depth(), in.maxDepth),
			in.withBacktrace(newError(ErrDepth, "non-tail call to %q would exceed max depth %d",
				c.Name, in.maxDepth))
	}
	if !quotaOK {
		return nil, "rejected",
			fmt.Sprintf("quota check: %d+%d > %d", in.stack.used, fn.NSlots, in.maxSlots),
			in.withBacktrace(newError(ErrQuota, "non-tail call to %q needs %d slots; would exceed quota %d",
				c.Name, fn.NSlots, in.maxSlots))
	}
	in.stack.push(fn, args)
	if in.stack.depth() > in.maxHist {
		in.maxHist = in.stack.depth()
	}
	return fn, "pushed-frame",
		fmt.Sprintf("tail-position=false (%s); new frame pushed; depth=%d slots=%d",
			c.TailReason, in.stack.depth(), in.stack.used), nil
}

func applyBin(op string, l, r int64) int64 {
	switch op {
	case "+":
		return l + r
	case "-":
		return l - r
	case "*":
		return l * r
	case "/":
		if r == 0 {
			panic(aborted{newError(ErrUnhandled, "division by zero is not raised in this minimal language")})
		}
		return l / r
	case "<":
		if l < r {
			return 1
		}
		return 0
	case "=":
		if l == r {
			return 1
		}
		return 0
	default:
		panic(aborted{newError(ErrUndefined, "unknown operator %q", op)})
	}
}

// ---------- 日志 ----------

func (in *Instance) logCall(c *CallExpr, args []int64, action, basis string, err *Error) {
	if in.logger == nil {
		return
	}
	parts := make([]string, len(args))
	for i, a := range args {
		parts[i] = strconv.FormatInt(a, 10)
	}
	input := "(" + c.Name + " " + strings.Join(parts, " ") + ")"
	output := action
	if err != nil {
		output = "rejected:" + err.Kind.String()
	}
	in.logger.Log(input, output, basis)
}

// BufferLogger 是线程安全的内存日志，供测试断言与人工检查。
type BufferLogger struct {
	mu  sync.Mutex
	b   strings.Builder
	all []string
}

func (l *BufferLogger) Log(input, output, basis string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	line := "input=" + input + " | output=" + output + " | basis=" + basis
	l.b.WriteString(line)
	l.b.WriteByte('\n')
	l.all = append(l.all, line)
}

// Lines 返回日志行的副本。
func (l *BufferLogger) Lines() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.all...)
}

func (l *BufferLogger) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

// WriterLogger 把每条日志以一行写入给定 Writer（带互斥）。
type WriterLogger struct {
	mu sync.Mutex
	w  io.Writer
}

func NewWriterLogger(w io.Writer) *WriterLogger { return &WriterLogger{w: w} }

func (l *WriterLogger) Log(input, output, basis string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	fmt.Fprintf(l.w, "input=%s | output=%s | basis=%s\n", input, output, basis)
}

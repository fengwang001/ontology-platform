package callstack

import "sync"

// Logger 接收诊断日志。每条日志包含输入、实际输出与判定依据。
type Logger interface {
	Logf(format string, args ...any)
}

// BoundaryHook 在每个调用边界（调用被接受且栈已稳定之后）被调用，
// 便于测试断言中间时刻的深度、配额、折叠计数与回溯。trace 是当时
// 刻的回溯快照拷贝；钩子在实例锁内执行，因此参数之外不得回调该实例。
type BoundaryHook func(Stats, []FrameInfo)

// Config 构造解释器实例。MaxDepth 按帧数计，MaxSlots 按各帧槽位
// 总数计；零表示对应上限不限。
type Config struct {
	MaxDepth  int
	MaxSlots  int
	Functions []*Function
	Logger    Logger
}

// Stats 是某一瞬间的四项统计。它们由同一把锁、在同一次读取中取出，
// 因此必然对应同一个调用边界，不会出现“深度已更新而配额未更新”。
type Stats struct {
	Depth      int
	Slots      int
	TailReuses int
	MaxDepth   int
}

// Interpreter 是一个独立解释器实例。多个实例的帧栈互不干扰；
// 同一实例上的调用串行推进，统计与回溯读取可并发进行。
type Interpreter struct {
	mu        sync.Mutex
	closed    bool
	maxDepth  int
	maxSlots  int
	funcs     map[string]*Function
	logger    Logger
	frames    []*frame
	slots     int
	reuses    int
	histMax   int
	nextFrame uint64
	execMu    sync.Mutex
	boundary  BoundaryHook

	// regions 是当前执行流登记的保护区域栈，仅在调用推进期间访问；
	// 读取者只能看到调用边界，因此不对外暴露。
	regions []*regionEntry
}

// 内部控制流信号：尾调用以 panic 从 eval 弹到帧循环。用户异常由
// thrown 承载，二者互不混淆且都不会泄漏到解释器之外。
type tailInvoke struct {
	fn   *Function
	args []int64
	name string
}

type thrown struct {
	payload int64
	// path 自内向外收集传播经过的帧。
	path []FrameInfo
}

// New 创建一个独立实例。
func New(cfg Config) *Interpreter {
	in := &Interpreter{
		maxDepth: cfg.MaxDepth,
		maxSlots: cfg.MaxSlots,
		funcs:    map[string]*Function{},
		logger:   cfg.Logger,
	}
	for _, fn := range cfg.Functions {
		in.funcs[fn.Name] = fn
	}
	return in
}

// SetBoundaryHook 安装调用边界钩子（测试用）。
func (in *Interpreter) SetBoundaryHook(h BoundaryHook) {
	in.mu.Lock()
	in.boundary = h
	in.mu.Unlock()
}

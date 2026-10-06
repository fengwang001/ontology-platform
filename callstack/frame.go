package callstack

// frame.go 是帧管理模块：帧的数据形态、入栈、尾复用、弹出、
// 配额核算与回溯快照。所有方法都不自带锁；调用方（instance.go）
// 必须持有实例互斥锁，从而保证「检查—修改」是单不可分事务。

// BacktraceFrame 是回溯中的一帧诊断信息。
// FoldedBefore 表示该帧位置之前被尾调用省略（折叠）掉的帧数：
// 连续尾调用之间累加，一旦发生非尾调用就归属到新帧之前并重新归零。
type BacktraceFrame struct {
	Func         string
	FoldedBefore int
	Slots        int
}

// Frame 是一次活动调用占用的帧。
// Slots 同时容纳参数与局部变量，槽位在载入期静态分配。
// Folded 记录折叠计数（本帧之前省略的帧数）。
type Frame struct {
	Func   *Func
	Slots  []int64
	Folded int
}

func newFrame(fn *Func, args []int64, folded int) *Frame {
	s := make([]int64, fn.NSlots)
	copy(s, args)
	return &Frame{Func: fn, Slots: s, Folded: folded}
}

func (f *Frame) snapshot() BacktraceFrame {
	return BacktraceFrame{Func: f.Func.Name, FoldedBefore: f.Folded, Slots: f.Func.NSlots}
}

// Stack 是一个实例的帧栈（栈底在下标 0）。
type Stack struct {
	frames []*Frame
	used   int // 当前全部帧的槽位总和（配额使用量）
}

func (s *Stack) depth() int { return len(s.frames) }

func (s *Stack) top() *Frame { return s.frames[len(s.frames)-1] }

// canPush 在不修改状态的前提下核算深度与配额。
// 深度与配额同时不满足时，深度优先（由调用方按此顺序取用）。
func (s *Stack) canPush(fn *Func, maxDepth, maxSlots int) (depthOK, quotaOK bool) {
	depthOK = len(s.frames)+1 <= maxDepth
	quotaOK = s.used+fn.NSlots <= maxSlots
	return depthOK, quotaOK
}

// push 压入一次非尾调用的新帧，折叠计数从零开始。
func (s *Stack) push(fn *Func, args []int64) {
	f := newFrame(fn, args, 0)
	s.frames = append(s.frames, f)
	s.used += fn.NSlots
}

// replaceTail 用一次尾调用复用栈顶帧：先释放旧帧再占用新帧。
// 调用方必须先调用 canReplace 完成配额预检；此处只在预检通过后执行，
// 因此预检失败时调用方根本不进入本方法，原帧天然完好（事务性）。
func (s *Stack) replaceTail(fn *Func, args []int64) {
	top := s.frames[len(s.frames)-1]
	s.used -= top.Func.NSlots
	folded := top.Folded + 1
	s.frames[len(s.frames)-1] = newFrame(fn, args, folded)
	s.used += fn.NSlots
}

// canReplace 预检尾复用的配额（复用不增加帧数，故无深度检查）。
func (s *Stack) canReplace(fn *Func, maxSlots int) bool {
	top := s.frames[len(s.frames)-1]
	return s.used-top.Func.NSlots+fn.NSlots <= maxSlots
}

// pop 弹出栈顶帧（非尾调用返回或异常逐帧传播时使用），释放其配额。
func (s *Stack) pop() *Frame {
	n := len(s.frames)
	f := s.frames[n-1]
	s.frames[n-1] = nil
	s.frames = s.frames[:n-1]
	s.used -= f.Func.NSlots
	return f
}

// backtrace 复制当前回溯，最内层帧在前。
// 开销为 O(当前物理帧数)，与被省略（折叠）的帧数无关。
func (s *Stack) backtrace() []BacktraceFrame {
	bt := make([]BacktraceFrame, len(s.frames))
	for i := range s.frames {
		bt[len(s.frames)-1-i] = s.frames[i].snapshot()
	}
	return bt
}

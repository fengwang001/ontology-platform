package callstack

// frame 是运行期调用帧。
//
// 一个 frame 同时承载：
//   - fn/slots：函数声明与静态槽位数（内存配额占用）；
//   - locals：参数与局部变量槽位，长度等于 slots；
//   - folded：此帧之前被连续尾调用省略的帧数。非尾调用入栈时归 0，
//     尾调用复用本帧时累加；一旦发生非尾调用，计数归属到新帧并重新
//     从零开始；
//   - id：帧身份，用于区域栈识别尾调用后失效的保护区域。
type frame struct {
	id     uint64
	fn     *Function
	locals []int64
	folded int
}

func newFrame(id uint64, fn *Function, args []int64) *frame {
	f := &frame{
		id:     id,
		fn:     fn,
		locals: make([]int64, fn.Slots),
	}
	copy(f.locals, args)
	return f
}

// regionEntry 是一个动态登记的异常保护区域。frameID 标识登记它的帧；
// 该帧被尾调用替换后，其登记的区域立即失效。
type regionEntry struct {
	frameID uint64
	try     *Try
}

// snapshotTraceLocked 在已持锁状态下复制当前帧栈为诊断回溯，顺序为
// 自底向上（外层在前、当前帧在末尾）。
// 因为每帧只保存一个 folded 整数（省略帧数不产生链表），本函数开销为
// O(当前帧数)，与被省略的总帧数无关。
func (in *Interpreter) snapshotTraceLocked() []FrameInfo {
	trace := make([]FrameInfo, len(in.frames))
	for i, f := range in.frames {
		trace[i] = FrameInfo{Function: f.fn.Name, Folded: f.folded}
	}
	return trace
}

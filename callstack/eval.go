package callstack

// eval 求值一个表达式。尾位置调用（Call.Tail 由登记期预标注）在通过
// 全部优先级校验后，以 tailInvoke 信号弹到帧循环做原子复用；校验失败
// 时直接抛 *Error，此刻尚未触碰帧栈、折叠计数与配额。
func (in *Interpreter) eval(e Expr) int64 {
	switch n := e.(type) {
	case *Int:
		return n.Value

	case *Var:
		top := in.frames[len(in.frames)-1]
		return top.locals[n.Index]

	case *Add:
		return in.eval(n.Left) + in.eval(n.Right)

	case *Call:
		// 参数位置的调用一律为非尾调用（markTail 已保证其 Tail=false）。
		args := make([]int64, len(n.Args))
		for i, arg := range n.Args {
			args[i] = in.eval(arg)
		}

		fn, callErr := in.checkCallLocked(n.Name, args)
		if callErr != nil {
			in.logLocked("call %s%v -> rejected: %s", n.Name, args, callErr.Kind)
			panic(callErr)
		}

		if n.Tail {
			// 尾调用：配额按“先释放旧帧”的净增量判定；旧帧在此之后
			// 才会被替换，因此拒绝时原帧完好。
			top := in.frames[len(in.frames)-1]
			if in.maxSlots > 0 && (in.slots-top.fn.Slots)+fn.Slots > in.maxSlots {
				quotaErr := newErrorf(ErrQuota,
					"tail call to %q would exceed quota %d: need %d slots after releasing %d",
					n.Name, in.maxSlots, fn.Slots, top.fn.Slots)
				in.logLocked("tail call %s%v -> rejected: %s (frame kept intact)",
					n.Name, args, quotaErr.Kind)
				panic(quotaErr)
			}
			in.logLocked("tail call %s%v -> accepted; basis syntactic tail position, no new frame",
				n.Name, args)
			panic(&tailInvoke{fn: fn, args: args, name: n.Name})
		}

		in.logLocked("call %s%v -> accepted non-tail; basis not in tail position", n.Name, args)
		return in.enter(fn, args)

	case *If:
		if in.eval(n.Cond) != 0 {
			return in.eval(n.Then)
		}
		return in.eval(n.Else)

	case *Let:
		value := in.eval(n.Bound)
		top := in.frames[len(in.frames)-1]
		top.locals[n.LocalIndex] = value
		return in.eval(n.Body)

	case *Seq:
		var last int64
		for _, item := range n.Items {
			last = in.eval(item)
		}
		return last

	case *Throw:
		in.logLocked("throw raised")
		panic(&thrown{payload: in.eval(n.Payload)})

	case *Try:
		entry := &regionEntry{
			frameID: in.frames[len(in.frames)-1].id,
			try:     n,
		}
		in.regions = append(in.regions, entry)

		var out int64
		func() {
			defer func() {
				rec := recover()
				if rec == nil {
					// 正常退出：注销本区域。
					in.popRegion(entry)
					return
				}
				t, ok := rec.(*thrown)
				if !ok {
					in.popRegion(entry)
					panic(rec)
				}
				// 区域仍登记且仍属于当前帧才有效。被尾调用替换掉的旧帧
				// 所登记区域会因 frameID 不匹配而被跳过，异常继续传播。
				if !in.regionActive(entry) {
					in.popRegion(entry)
					panic(t)
				}
				// 决定处理：本区域即刻注销，再求值处理分支（处理分支
				// 处于一个新的求值上下文，其内部调用与本区域无关）。
				in.popRegion(entry)
				top := in.frames[len(in.frames)-1]
				top.locals[n.CaughtIndex] = t.payload
				in.logLocked("exception %d caught; basis active region in frame %s",
					t.payload, top.fn.Name)
				out = in.eval(n.Handler)
			}()
			out = in.eval(n.Protected)
		}()
		return out

	default:
		panic("callstack: unknown expression")
	}
}

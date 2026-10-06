package callstack

// naive 是不做帧复用的参照解释器：每一次调用（无论是否处于尾位置）
// 都压入一个全新的帧，并受与真实解释器完全相同的深度上限（按帧数）
// 和槽位配额（按参数+局部槽位总数）约束。它故意忽略 Call.Tail。
//
// 对照语义：随机生成的调用序列在朴素模型与真实模型上，“是否溢出”
// 允许不同（真实模型的尾调用不加深栈），但只要朴素模型成功返回，
// 真实模型必须成功且返回值相同；朴素模型报深度/配额错误时，真实
// 模型允许成功或报同类资源错误，但不得产生不同的正常结果。
type naive struct {
	funcs    map[string]*Function
	maxDepth int
	maxSlots int
	depth    int
	slots    int
}

func naiveCall(funcs map[string]*Function, maxDepth, maxSlots int, name string, args []int64) (int64, error) {
	n := &naive{funcs: funcs, maxDepth: maxDepth, maxSlots: maxSlots}
	value, err := n.call(name, args)
	return value, err
}

func (n *naive) call(name string, args []int64) (value int64, err error) {
	fn, ok := n.funcs[name]
	if !ok {
		return 0, newErrorf(ErrUndefinedFunction, "function %q is not defined", name)
	}
	if len(args) != len(fn.Params) {
		return 0, newErrorf(ErrArity, "function %q expects %d args, got %d",
			name, len(fn.Params), len(args))
	}
	if n.maxDepth > 0 && n.depth >= n.maxDepth {
		return 0, newErrorf(ErrDepth, "depth limit %d reached before call to %q", n.maxDepth, name)
	}
	if n.maxSlots > 0 && n.slots+fn.Slots > n.maxSlots {
		return 0, newErrorf(ErrQuota, "quota %d exceeded by call to %q", n.maxSlots, name)
	}

	locals := make([]int64, fn.Slots)
	copy(locals, args)
	n.depth++
	n.slots += fn.Slots
	defer func() {
		n.depth--
		n.slots -= fn.Slots
		if r := recover(); r != nil {
			if e, isErr := r.(*Error); isErr {
				err = e
				return
			}
			panic(r)
		}
	}()
	return n.eval(fn.Body, locals), nil
}

type naiveThrown struct{ payload int64 }

func (n *naive) eval(e Expr, locals []int64) int64 {
	switch x := e.(type) {
	case *Int:
		return x.Value
	case *Var:
		return locals[x.Index]
	case *Add:
		return n.eval(x.Left, locals) + n.eval(x.Right, locals)
	case *Call:
		args := make([]int64, len(x.Args))
		for i, arg := range x.Args {
			args[i] = n.eval(arg, locals)
		}
		// 关键：即便 x.Tail 为真也照常压帧——朴素模型不复用。
		value, err := n.call(x.Name, args)
		if err != nil {
			panic(err)
		}
		return value
	case *If:
		if n.eval(x.Cond, locals) != 0 {
			return n.eval(x.Then, locals)
		}
		return n.eval(x.Else, locals)
	case *Let:
		locals[x.LocalIndex] = n.eval(x.Bound, locals)
		return n.eval(x.Body, locals)
	case *Seq:
		var last int64
		for _, item := range x.Items {
			last = n.eval(item, locals)
		}
		return last
	case *Throw:
		panic(naiveThrown{payload: n.eval(x.Payload, locals)})
	case *Try:
		var out int64
		func() {
			defer func() {
				if r := recover(); r != nil {
					if t, ok := r.(naiveThrown); ok {
						locals[x.CaughtIndex] = t.payload
						out = n.eval(x.Handler, locals)
						return
					}
					panic(r)
				}
			}()
			out = n.eval(x.Protected, locals)
		}()
		return out
	default:
		panic("callstack: unknown expression in naive model")
	}
}

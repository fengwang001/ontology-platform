package callstack

// markTail 对函数体做一次自顶向下的尾位置标注。这是纯粹的语法位置
// 判定：只看调用在语法树中所处的位置，与被调函数是谁无关。
//
// 规则：
//   - 函数体最后求值的调用是尾位置；
//   - If 的两个分支各自最后求值的调用是尾位置（条件不是）；
//   - Seq 最后一项中的调用是尾位置（前序项不是）；
//   - Let 体中的调用是尾位置（绑定表达式不是）；
//   - Try 处理分支中的调用是尾位置，但受保护区内的任何调用都不是，
//     即使它位于区域最末——区域出口尚待执行；
//   - 参数位置的调用、其结果还要参与运算（Add/Throw 等）的调用不是。
//
// 标注后，运行期判定一次调用只需读取 Call.Tail，为 O(1)。
func markTail(e Expr, tailCtx bool) {
	switch n := e.(type) {
	case *Int, *Var:
	case *Add:
		markTail(n.Left, false)
		markTail(n.Right, false)
	case *Call:
		n.Tail = tailCtx
		for _, arg := range n.Args {
			markTail(arg, false)
		}
	case *If:
		markTail(n.Cond, false)
		markTail(n.Then, tailCtx)
		markTail(n.Else, tailCtx)
	case *Let:
		markTail(n.Bound, false)
		markTail(n.Body, tailCtx)
	case *Seq:
		for i, item := range n.Items {
			markTail(item, tailCtx && i == len(n.Items)-1)
		}
	case *Throw:
		markTail(n.Payload, false)
	case *Try:
		// 受保护区：强制非尾上下文。
		markTail(n.Protected, false)
		// 只有进入处理分支后，其最后一个调用才是尾调用。
		markTail(n.Handler, tailCtx)
	default:
		panic("callstack: unknown expression")
	}
}

// countLocals 自顶向下解析变量到帧槽位的映射，并返回“局部绑定”个数
// （不含参数）。每遇到一个 Let 或 Try 的捕获绑定就分配一个槽位；
// Var 按当前作用域解析，最内层绑定遮蔽外层同名绑定。
// 开销在函数登记时一次性支付，运行期变量访问为直接索引。
func countLocals(fn *Function, e Expr) int {
	for i, p := range fn.Params {
		fn.localIndex[p] = i
	}
	next := len(fn.Params)

	var walk func(Expr, map[string]int)
	walk = func(x Expr, scope map[string]int) {
		switch n := x.(type) {
		case *Int:
		case *Var:
			n.Index = scope[n.Name] // 未定义变量解析为 0 槽；测试可避免使用
		case *Add:
			walk(n.Left, scope)
			walk(n.Right, scope)
		case *Call:
			for _, arg := range n.Args {
				walk(arg, scope)
			}
		case *If:
			walk(n.Cond, scope)
			walk(n.Then, scope)
			walk(n.Else, scope)
		case *Let:
			n.LocalIndex = next
			next++
			walk(n.Bound, scope)
			inner := extendScope(scope, n.Name, n.LocalIndex)
			walk(n.Body, inner)
		case *Seq:
			for _, item := range n.Items {
				walk(item, scope)
			}
		case *Throw:
			walk(n.Payload, scope)
		case *Try:
			walk(n.Protected, scope)
			n.CaughtIndex = next
			next++
			handlerScope := extendScope(scope, n.CaughtName, n.CaughtIndex)
			walk(n.Handler, handlerScope)
		default:
			panic("callstack: unknown expression")
		}
	}

	walk(e, fn.localIndex)
	return next - len(fn.Params)
}

func extendScope(parent map[string]int, name string, idx int) map[string]int {
	inner := make(map[string]int, len(parent)+1)
	for k, v := range parent {
		inner[k] = v
	}
	inner[name] = idx
	return inner
}
